// Package cli implements the lite-deploy command line (SPEC-v0.md, section 6).
package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/toxic-sunflower/lite-deploy/internal/config"
	"github.com/toxic-sunflower/lite-deploy/internal/executor"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

// Version is set by a release build: -ldflags "-X .../internal/cli.Version=v1.2.3".
var Version = "0.0.0-dev"

// Schema identifiers.
const (
	ProjectSchema = project.SchemaID
	PlanSchema    = plan.SchemaID
)

// Env is everything external the CLI depends on; replaced in tests.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	FS     host.FS
	Paths  plan.Paths
	Config string // the config.json path; empty: config.DefaultPath
	// paths of the executor key and the secret store; empty: the defaults from the secrets package
	KeyPath, PubPath, SecretsDir string
	IsRoot                       func() bool
	// CurrentUser is the process user name; nil: os/user
	CurrentUser func() string
	// AgentCall calls the agent (replaced in tests); nil: agent.Call
	AgentCall func(ctx context.Context, sock, method, path string, body []byte) ([]byte, error)
	Now       func() time.Time
	// for apply and project create: running commands, HTTP checks, randomness, the interactive terminal flag, uid
	Runner      host.Runner
	HTTP        host.HTTPClient
	Rand        io.Reader
	Interactive func() bool
	UID         int
	// for deploy, rollback, status, gate
	Lock   host.Locker
	Sleep  func(time.Duration)
	Getenv func(string) string
	// PluginDir is the directory of external plugins; empty: plugin.Dir
	PluginDir string
	// LoginUID is the login uid (/proc/self/loginuid); ok=false if it is not set
	LoginUID func() (int, bool)
}

// Run executes a command in the real environment and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunEnv(args, realEnv(stdout, stderr))
}

// realEnv is the real process environment.
func realEnv(stdout, stderr io.Writer) Env {
	return Env{Stdin: os.Stdin, Stdout: stdout, Stderr: stderr, FS: host.OSFS{}, Paths: plan.DefaultPaths(), Config: config.DefaultPath, Now: time.Now, IsRoot: func() bool { return os.Geteuid() == 0 },
		Runner: host.ExecRunner{}, HTTP: host.NetHTTP{}, Rand: rand.Reader, UID: os.Geteuid(), Lock: host.OSLocker{}, Sleep: time.Sleep, Getenv: os.Getenv, LoginUID: loginUID,
		Interactive: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }} //nolint:gosec // the terminal descriptor fits into an int
}

// result is the outcome of a command: data or an error, plus text for people.
type result struct {
	command string
	data    any
	text    string
	warn    []Warning
	err     *ErrorBody
	// raw is a ready JSON envelope (the agent answer): printed as is, the exit code is taken from it
	raw []byte
}

func fail(command, code, msg string) result {
	return result{command: command, err: &ErrorBody{Code: code, Message: msg}}
}

// RunEnv executes a command and returns the exit code (SPEC-v0.md, 6.3).
func RunEnv(args []string, env Env) int {
	jsonOut := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
			continue
		}
		rest = append(rest, a)
	}
	// the gatekeeper: the response format is set by the CI key command (`status --json`), not by the arguments of the forced command
	if len(rest) > 0 && rest[0] == "gate" && env.Getenv != nil && strings.HasSuffix(env.Getenv("SSH_ORIGINAL_COMMAND"), " --json") {
		jsonOut = true
	}
	var r result
	if isHelp(rest) {
		r = cmdHelp(env)
	} else if cmd := directRoot(rest, env); cmd != "" {
		r = result{command: cmd, err: &ErrorBody{Code: "permission_denied",
			Message: "lite does not work when logged in as root: log in as a regular user and use sudo",
			Hint:    "no such user: create it with lite init --user NAME (init is the only command that runs as root)"}}
	} else {
		r = dispatch(rest, env)
	}
	if r.raw != nil {
		_, _ = env.Stdout.Write(append(r.raw, '\n'))
		var e Envelope
		if json.Unmarshal(r.raw, &e) == nil && e.Error != nil {
			return ExitCode(e.Error.Code)
		}
		return 0
	}
	if jsonOut {
		writeJSON(env.Stdout, Envelope{OK: r.err == nil, APIVersion: APIVersion, Command: r.command, Data: r.data, Warnings: r.warn, Error: r.err})
	} else if r.err != nil {
		_, _ = fmt.Fprint(env.Stdout, r.text)
		_, _ = fmt.Fprintf(env.Stderr, "ERROR [%s]: %s\n", r.err.Code, r.err.Message)
		for _, d := range r.err.Details {
			_, _ = fmt.Fprintf(env.Stderr, "  %s: %s (%s)\n", d.Path, d.Message, d.Code)
		}
		if r.err.Hint != "" {
			_, _ = fmt.Fprintf(env.Stderr, "Hint: %s\n", r.err.Hint)
		}
	} else {
		_, _ = fmt.Fprint(env.Stdout, r.text)
		for _, w := range r.warn {
			_, _ = fmt.Fprintf(env.Stderr, "Warning [%s]: %s\n", w.Code, w.Message)
		}
	}
	if r.err != nil {
		return ExitCode(r.err.Code)
	}
	return 0
}

// directRoot returns the command name if it was run when logged in as root (loginuid 0) and is forbidden to do so.
// Through sudo from a regular user loginuid stays that user's uid; for systemd services it is not set.
func directRoot(args []string, env Env) string {
	if len(args) == 0 || env.LoginUID == nil || !env.root() {
		return ""
	}
	switch args[0] {
	case "init", "version", "schema", "agent", "exec":
		return ""
	}
	if uid, ok := env.LoginUID(); ok && uid == 0 {
		return args[0]
	}
	return ""
}

func dispatch(args []string, env Env) result {
	if isHelp(args) {
		return cmdHelp(env)
	}
	if len(args) == 0 {
		return fail("usage", CodeUsage, "usage: lite-deploy <command>; commands: version, schema, project validate|plan|create|list, apply, deploy, rollback, status, firewall (lite --help lists everything)")
	}
	switch args[0] {
	case "version":
		return result{command: "version",
			data: map[string]any{"version": Version, "schemas": []string{ProjectSchema, PlanSchema}},
			text: fmt.Sprintf("lite-deploy %s\n", Version)}
	case "schema":
		return cmdSchema(args[1:])
	case "project":
		if len(args) < 2 {
			return fail("project", CodeUsage, "usage: lite-deploy project validate|plan|list")
		}
		switch args[1] {
		case "validate":
			return cmdValidate(args[2:], env)
		case "plan":
			return cmdPlan(args[2:], env)
		case "list":
			return cmdList(env)
		case "create":
			return cmdProjectCreate(args[2:], env)
		case "bundle":
			return cmdProjectBundle(args[2:], env)
		}
		return fail("project", CodeUsage, "unknown project subcommand: "+args[1])
	case "apply":
		return cmdApply(args[1:], env)
	case "init":
		return cmdInit(args[1:], env)
	case "secret":
		return cmdSecret(args[1:], env)
	case "deploy":
		return cmdDeploy(args[1:], env)
	case "rollback":
		return cmdRollback(args[1:], env)
	case "status":
		return cmdStatus(args[1:], env)
	case "publish":
		return cmdPublish(args[1:], env)
	case "update":
		return cmdUpdate(args[1:], env)
	case "gate":
		return cmdGate(args[1:], env)
	case "agent":
		return cmdAgent(args[1:], env)
	case "exec":
		return cmdExec(env)
	case "call":
		return cmdCall(args[1:], env)
	case "plugin":
		return cmdPlugin(args[1:], env)
	case "info":
		return cmdInfo(env, "info")
	case "fix":
		return cmdFix(args[1:], env)
	case "verify":
		if len(args) > 1 && args[1] == "--autofix" {
			return cmdFix(args[2:], env)
		}
		return cmdVerify(env)
	}
	// a plugin command: lite PLUGIN COMMAND
	if r, _ := registry(env); r != nil {
		if _, ok := r.Get(args[0]); ok {
			return cmdPluginRun(args[0], args[1:], env)
		}
	}
	return fail("usage", CodeUsage, "unknown command: "+args[0]+" (plugins: lite plugin list)")
}

// listFlag is a repeatable flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// parseArgs parses flags that stand anywhere among positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, nil
}

// loadConfig reads config.json; file errors become precondition_failed.
func loadConfig(env Env, command string) (config.Config, *result) {
	path := env.Config
	if path == "" {
		path = config.DefaultPath
	}
	c, issues, err := config.Load(env.FS, path)
	if err != nil {
		r := fail(command, plan.CodePrecondition, err.Error())
		return c, &r
	}
	if len(issues) > 0 {
		r := result{command: command, err: &ErrorBody{Code: plan.CodePrecondition, Message: "file " + path + " did not pass validation", Details: issues}}
		return c, &r
	}
	return c, nil
}

func readInput(env Env, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(env.Stdin, 4*1024*1024))
	}
	return os.ReadFile(path)
}

func cmdSchema(args []string) result {
	if len(args) != 1 {
		return fail("schema", CodeUsage, "usage: lite-deploy schema <project|plan|result|error>")
	}
	b, ok := Schemas()[args[0]]
	if !ok {
		return fail("schema", CodeUsage, "unknown schema "+args[0]+"; available: "+strings.Join(SchemaNames, ", "))
	}
	var data any
	_ = json.Unmarshal(b, &data)
	return result{command: "schema", data: data, text: string(b)}
}

func loadProject(env Env, command string, args []string) (*project.Project, []string, *result) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	out := fs.String("out", "", "")
	rotate := fs.Bool("rotate-password", false, "")
	var cts listFlag
	fs.Var(&cts, "ciphertext", "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		r := fail(command, CodeUsage, "usage: lite-deploy "+strings.ReplaceAll(command, ".", " ")+" <project.json|-> [--out file] [--ciphertext name=file] [--rotate-password]")
		return nil, nil, &r
	}
	data, err := readInput(env, pos[0])
	if err != nil {
		r := fail(command, CodeIO, "could not read the project description: "+err.Error())
		return nil, nil, &r
	}
	cfg, bad := loadConfig(env, command)
	if bad != nil {
		return nil, nil, bad
	}
	var p *project.Project
	var issues []schema.Issue
	if len(data) > cfg.Limits.ProjectBytes {
		issues = []schema.Issue{{Path: "/", Code: "too_large", Message: fmt.Sprintf("the file is larger than %d bytes", cfg.Limits.ProjectBytes)}}
	} else {
		p, issues = project.Parse(data)
	}
	if len(issues) == 0 {
		issues = project.Validate(p)
	}
	if len(issues) > 0 {
		r := result{command: command, err: &ErrorBody{Code: plan.CodeInvalidProject, Message: "the project description did not pass validation", Details: issues, Hint: "fix the fields listed in details"}}
		return nil, nil, &r
	}
	extra := append([]string{*out, fmt.Sprint(*rotate)}, cts...)
	return p, extra, nil
}

func cmdValidate(args []string, env Env) result {
	p, _, bad := loadProject(env, "project.validate", args)
	if bad != nil {
		return *bad
	}
	return result{command: "project.validate", data: map[string]any{"valid": true},
		text: fmt.Sprintf("Project %s is valid: services %d, sites %d.\n", p.Name, len(p.Services), len(p.Sites))}
}

type stepView struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Class   string `json:"class,omitempty"`
	State   string `json:"state,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// prepared is the built plan and everything needed to apply it.
type prepared struct {
	pl      *plan.Plan
	project *project.Project
	out     string
}

// preparePlan parses the arguments of `project plan` and `project create` and builds a plan (without writing to disk).
func preparePlan(command string, args []string, env Env) (*prepared, *result) {
	p, extra, bad := loadProject(env, command, args)
	if bad != nil {
		return nil, bad
	}
	out, rotate, cts := extra[0], extra[1] == "true", extra[2:]
	ciphertexts := map[string]string{}
	for _, c := range cts {
		name, path, ok := strings.Cut(c, "=")
		if !ok || name == "" || path == "" {
			r := fail(command, CodeUsage, "--ciphertext expects name=file")
			return nil, &r
		}
		b, err := os.ReadFile(path)
		if err != nil {
			r := fail(command, CodeIO, "could not read the ciphertext "+name+": "+err.Error())
			return nil, &r
		}
		ciphertexts[name] = strings.TrimSpace(string(b))
	}
	return buildPlan(command, p, out, rotate, ciphertexts, env)
}

// buildPlan builds a plan from a ready description and ciphertexts (the shared part of the file and bundle paths).
func buildPlan(command string, p *project.Project, out string, rotate bool, ciphertexts map[string]string, env Env) (*prepared, *result) {
	st, err := plan.LoadState(env.FS, env.Paths)
	if err != nil {
		r := fail(command, plan.CodePrecondition, err.Error())
		return nil, &r
	}
	cfg, badCfg := loadConfig(env, command)
	if badCfg != nil {
		return nil, badCfg
	}
	st.Now = env.Now()
	st.TTL = time.Duration(cfg.Limits.PlanTTLHours) * time.Hour
	st.PortRange = [2]int{cfg.Limits.PortRange[0], cfg.Limits.PortRange[1]}
	st.MaxProjects = cfg.Limits.Projects
	st.RotatePassword = rotate
	st.Ciphertexts = ciphertexts

	pl, berr := plan.Build(p, st)
	if berr != nil {
		r := result{command: command, err: &ErrorBody{Code: berr.Code, Message: berr.Message, Details: berr.Issues, Hint: berr.Hint}}
		return nil, &r
	}
	return &prepared{pl: pl, project: p, out: out}, nil
}

func cmdPlan(args []string, env Env) result {
	pr, bad := preparePlan("project.plan", args, env)
	if bad != nil {
		return *bad
	}
	pl, out := pr.pl, pr.out
	if out != "" {
		b, _ := json.MarshalIndent(pl, "", "  ")
		if err := os.WriteFile(out, append(b, '\n'), 0o600); err != nil {
			return fail("project.plan", CodeIO, "could not write the plan: "+err.Error())
		}
	}
	steps := make([]stepView, 0, len(pl.Steps))
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan %s for project %s\nHash: %s\nValid until: %s\n\n", pl.ID, pl.Project, pl.Hash, pl.ExpiresAt)
	for _, s := range pl.Steps {
		steps = append(steps, stepView{ID: s.ID, Action: s.Action, Class: s.Class, Summary: s.Summary})
		fmt.Fprintf(&sb, "  %s  %-20s %s\n", s.ID, s.Action, s.Summary)
	}
	if out != "" {
		fmt.Fprintf(&sb, "\nPlan written to %s. Nothing on the server has changed: a plan only describes the changes.\n", out)
	}
	return result{command: "project.plan", data: map[string]any{"plan": pl, "steps": steps}, text: sb.String()}
}

func cmdApply(args []string, env Env) result {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	yes := fs.Bool("yes", false, "")
	projFile := fs.String("project-file", "", "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		return fail("apply", CodeUsage, "usage: lite-deploy apply <plan.json|-> [--dry-run] [--yes] [--project-file project.json]")
	}
	if !env.root() {
		return fail("apply", "permission_denied", "apply must run as root: sudo lite-deploy apply …")
	}
	data, err := readInput(env, pos[0])
	if err != nil {
		return fail("apply", CodeIO, "could not read the plan: "+err.Error())
	}
	var projJSON []byte
	if *projFile != "" {
		if projJSON, err = os.ReadFile(*projFile); err != nil {
			return fail("apply", CodeIO, "could not read the project description: "+err.Error())
		}
	}
	return applyPlan("apply", env, data, *dry, *yes, projJSON)
}

// applyPlan is the shared path of apply and project create: plan check, dry-run, confirmation, applying.
func applyPlan(command string, env Env, data []byte, dry, yes bool, projJSON []byte) result {
	st, err := plan.LoadState(env.FS, env.Paths)
	if err != nil {
		return fail(command, plan.CodePrecondition, err.Error())
	}
	cfg, badCfg := loadConfig(env, command)
	if badCfg != nil {
		return *badCfg
	}
	lim := plan.DefaultLimits()
	lim.PlanBytes, lim.PlanSteps, lim.PlanTTLHours = cfg.Limits.PlanBytes, cfg.Limits.PlanSteps, cfg.Limits.PlanTTLHours
	pl, verr := plan.Verify(data, plan.VerifyOptions{Now: env.Now(), ServerID: st.ServerID, Limits: lim})
	if verr != nil {
		return result{command: command, err: &ErrorBody{Code: verr.Code, Message: verr.Message, Details: verr.Issues}}
	}
	if len(projJSON) > 0 {
		// A copy of the description in /etc/lite-deploy/projects is written only if it matches the plan.
		if p, issues := project.Parse(projJSON); len(issues) == 0 {
			if h, herr := plan.ProjectHash(p); herr != nil || h != pl.ProjectHash {
				projJSON = nil
			}
		} else {
			projJSON = nil
		}
	}
	ex := executor.Env{Runner: env.Runner, FS: env.FS, HTTP: env.HTTP, Secrets: secrets.NewStoreAt(env.FS, env.secretsDir()),
		KeyPath: env.keyPath(), StateDir: env.Paths.StateDir, Rand: env.Rand, Now: env.Now, UID: env.UID, ProjectJSON: projJSON}
	ctx := context.Background()

	if !dry {
		if !yes {
			if env.Interactive == nil || !env.Interactive() {
				return fail(command, "confirmation_required", "without a terminal, applying requires the --yes flag")
			}
			preview, err := executor.Apply(ctx, ex, pl, true)
			if err != nil {
				return fail(command, CodeInternal, err.Error())
			}
			_, _ = fmt.Fprint(env.Stderr, renderReport(preview, "Preview (nothing changed):"))
			_, _ = fmt.Fprint(env.Stderr, "\nType yes to apply: ")
			line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
			if strings.TrimSpace(line) != "yes" {
				return fail(command, "confirmation_required", "applying was canceled: no confirmation received")
			}
		}
	}
	rep, err := executor.Apply(ctx, ex, pl, dry)
	if err != nil {
		return fail(command, plan.CodePrecondition, err.Error())
	}
	text := renderReport(rep, "")
	data2 := map[string]any{"plan_id": rep.PlanID, "plan_hash": rep.PlanHash, "dry_run": dry, "steps": rep.Steps}
	if rep.Failed != "" && !dry {
		details := []schema.Issue{}
		for _, s := range rep.Steps {
			if s.State == executor.StateFailed {
				details = append(details, schema.Issue{Path: "/steps/" + s.ID, Code: s.Action, Message: s.Error})
			}
		}
		return result{command: command, text: text, err: &ErrorBody{Code: "step_failed", Message: "step " + rep.Failed + " failed; steps already done stay in place, running apply again will continue", Details: details,
			Hint: "fix the cause and run apply with the same plan (while it has not expired)"}, data: data2}
	}
	r := result{command: command, data: data2, text: text}
	if rep.Failed != "" {
		r.warn = append(r.warn, Warning{Code: "would_fail", Message: "step " + rep.Failed + " would have failed"})
	}
	return r
}

func renderReport(rep *executor.Report, title string) string {
	var sb strings.Builder
	if title != "" {
		sb.WriteString(title + "\n")
	}
	fmt.Fprintf(&sb, "Plan %s, hash %s\n\n", rep.PlanID, rep.PlanHash)
	for _, s := range rep.Steps {
		fmt.Fprintf(&sb, "  %s  %-20s %-13s %s\n", s.ID, s.Action, s.State, joinNonEmpty(s.Detail, s.Error))
	}
	return sb.String()
}

func joinNonEmpty(a, b string) string {
	switch {
	case a != "" && b != "":
		return a + "; " + b
	case a != "":
		return a
	}
	return b
}

// cmdProjectCreate: plan, dry-run and apply in one command (SPEC-v0.md, 6.1).
func cmdProjectCreate(args []string, env Env) result {
	fs := flag.NewFlagSet("project.create", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	fs.String("out", "", "")
	rotate := fs.Bool("rotate-password", false, "")
	var cts listFlag
	fs.Var(&cts, "ciphertext", "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		return fail("project.create", CodeUsage, "usage: lite-deploy project create <project.json|-> [--yes] [--ciphertext name=file] [--rotate-password]")
	}
	if !env.root() {
		return forwardProjectCreate(env, pos[0], *yes, *rotate, cts)
	}
	// stdin is read once: "-" cannot be used both for the description and for the confirmation
	var rest []string
	for _, a := range args {
		if a != "--yes" {
			rest = append(rest, a)
		}
	}
	pr, bad := preparePlan("project.create", rest, env)
	if bad != nil {
		return *bad
	}
	planBytes, err := json.Marshal(pr.pl)
	if err != nil {
		return fail("project.create", CodeInternal, err.Error())
	}
	var projJSON []byte
	if pos[0] != "-" {
		projJSON, _ = os.ReadFile(pos[0])
	}
	res := applyPlan("project.create", env, planBytes, false, *yes, projJSON)
	return withCreateHint(res, pr.pl.Project)
}

// withCreateHint adds what to do after a project is created.
func withCreateHint(r result, name string) result {
	if r.err == nil && name != "" {
		r.text += "Next: deploy a version of the project: lite deploy " + name + " FULL_40_CHARACTER_COMMIT_HASH\n"
	}
	return r
}

func cmdList(env Env) result {
	st, err := plan.LoadState(env.FS, env.Paths)
	if err != nil {
		return fail("project.list", plan.CodePrecondition, err.Error())
	}
	type item struct {
		Name     string `json:"name"`
		Hash     string `json:"hash"`
		Deployed bool   `json:"deployed"`
	}
	items := []item{}
	var sb strings.Builder
	for _, n := range sortedProjectNames(st) {
		ps := st.Projects[n]
		items = append(items, item{n, ps.Hash, ps.Deployed})
		fmt.Fprintf(&sb, "%s\t%s\n", n, ps.Hash)
	}
	if len(items) == 0 {
		sb.WriteString("No projects are managed by lite-deploy.\n")
	}
	return result{command: "project.list", data: map[string]any{"projects": items}, text: sb.String()}
}

func sortedProjectNames(st plan.State) []string {
	out := make([]string, 0, len(st.Projects))
	for n := range st.Projects {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// loginUID reads the login uid. 4294967295 means "not set" (a service, a container).
func loginUID() (int, bool) {
	b, err := os.ReadFile("/proc/self/loginuid")
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 32)
	if err != nil || n == 4294967295 {
		return 0, false
	}
	return int(n), true
}
