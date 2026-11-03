// Package cli реализует командную строку lite-deploy (SPEC-v0.md, раздел 6).
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/config"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// Версия и идентификаторы схем.
const (
	Version       = "0.0.0-dev"
	ProjectSchema = project.SchemaID
	PlanSchema    = plan.SchemaID
)

// Env всё внешнее, от чего зависит CLI; в тестах подменяется.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	FS     host.FS
	Paths  plan.Paths
	Config string // путь config.json; пусто: config.DefaultPath
	Now    func() time.Time
}

// Run выполняет команду в настоящем окружении и возвращает код выхода.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunEnv(args, Env{Stdin: os.Stdin, Stdout: stdout, Stderr: stderr, FS: host.OSFS{}, Paths: plan.DefaultPaths(), Config: config.DefaultPath, Now: time.Now})
}

// result итог команды: данные или ошибка, плюс текст для людей.
type result struct {
	command string
	data    any
	text    string
	warn    []Warning
	err     *ErrorBody
}

func fail(command, code, msg string) result {
	return result{command: command, err: &ErrorBody{Code: code, Message: msg}}
}

// RunEnv выполняет команду и возвращает код выхода (SPEC-v0.md, 6.3).
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
	r := dispatch(rest, env)
	if jsonOut {
		writeJSON(env.Stdout, Envelope{OK: r.err == nil, APIVersion: APIVersion, Command: r.command, Data: r.data, Warnings: r.warn, Error: r.err})
	} else if r.err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "ОШИБКА [%s]: %s\n", r.err.Code, r.err.Message)
		for _, d := range r.err.Details {
			_, _ = fmt.Fprintf(env.Stderr, "  %s: %s (%s)\n", d.Path, d.Message, d.Code)
		}
		if r.err.Hint != "" {
			_, _ = fmt.Fprintf(env.Stderr, "Подсказка: %s\n", r.err.Hint)
		}
	} else {
		_, _ = fmt.Fprint(env.Stdout, r.text)
		for _, w := range r.warn {
			_, _ = fmt.Fprintf(env.Stderr, "Предупреждение [%s]: %s\n", w.Code, w.Message)
		}
	}
	if r.err != nil {
		return ExitCode(r.err.Code)
	}
	return 0
}

func dispatch(args []string, env Env) result {
	if len(args) == 0 {
		return fail("usage", CodeUsage, "использование: lite-deploy <команда>; команды: version, schema, project validate|plan|list, apply")
	}
	switch args[0] {
	case "version":
		return result{command: "version",
			data: map[string]any{"version": Version, "schemas": []string{ProjectSchema, PlanSchema}},
			text: fmt.Sprintf("lite-deploy %s\nсхемы: %s, %s\n", Version, ProjectSchema, PlanSchema)}
	case "schema":
		return cmdSchema(args[1:])
	case "project":
		if len(args) < 2 {
			return fail("project", CodeUsage, "использование: lite-deploy project validate|plan|list")
		}
		switch args[1] {
		case "validate":
			return cmdValidate(args[2:], env)
		case "plan":
			return cmdPlan(args[2:], env)
		case "list":
			return cmdList(env)
		case "create":
			return fail("project.create", CodeNotImplemented, "project create появится в S4 (применение плана)")
		}
		return fail("project", CodeUsage, "неизвестная подкоманда project: "+args[1])
	case "apply":
		return cmdApply(args[1:], env)
	case "secret", "deploy", "rollback", "status", "gate", "init":
		return fail(args[0], CodeNotImplemented, "команда "+args[0]+" появится в следующих сессиях (см. PLAN.md)")
	}
	return fail("usage", CodeUsage, "неизвестная команда: "+args[0])
}

// listFlag повторяемый флаг.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// parseArgs разбирает флаги, стоящие в любом месте среди позиционных аргументов.
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

// loadConfig читает config.json; ошибки файла превращаются в precondition_failed.
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
		r := result{command: command, err: &ErrorBody{Code: plan.CodePrecondition, Message: "файл " + path + " не прошёл проверку", Details: issues}}
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
		return fail("schema", CodeUsage, "использование: lite-deploy schema <project|plan|result|error>")
	}
	b, ok := Schemas()[args[0]]
	if !ok {
		return fail("schema", CodeUsage, "неизвестная схема "+args[0]+"; есть: "+strings.Join(SchemaNames, ", "))
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
		r := fail(command, CodeUsage, "использование: lite-deploy "+strings.ReplaceAll(command, ".", " ")+" <project.json|-> [--out файл] [--ciphertext имя=файл] [--rotate-password]")
		return nil, nil, &r
	}
	data, err := readInput(env, pos[0])
	if err != nil {
		r := fail(command, CodeIO, "не удалось прочитать описание проекта: "+err.Error())
		return nil, nil, &r
	}
	cfg, bad := loadConfig(env, command)
	if bad != nil {
		return nil, nil, bad
	}
	var p *project.Project
	var issues []schema.Issue
	if len(data) > cfg.Limits.ProjectBytes {
		issues = []schema.Issue{{Path: "/", Code: "too_large", Message: fmt.Sprintf("файл больше %d байт", cfg.Limits.ProjectBytes)}}
	} else {
		p, issues = project.Parse(data)
	}
	if len(issues) == 0 {
		issues = project.Validate(p)
	}
	if len(issues) > 0 {
		r := result{command: command, err: &ErrorBody{Code: plan.CodeInvalidProject, Message: "описание проекта не прошло проверку", Details: issues, Hint: "исправьте поля из details"}}
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
		text: fmt.Sprintf("Описание проекта %s верно: сервисов %d, сайтов %d.\n", p.Name, len(p.Services), len(p.Sites))}
}

type stepView struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Class   string `json:"class,omitempty"`
	State   string `json:"state,omitempty"`
	Summary string `json:"summary,omitempty"`
}

func cmdPlan(args []string, env Env) result {
	p, extra, bad := loadProject(env, "project.plan", args)
	if bad != nil {
		return *bad
	}
	out, rotate, cts := extra[0], extra[1] == "true", extra[2:]

	st, err := plan.LoadState(env.FS, env.Paths)
	if err != nil {
		return fail("project.plan", plan.CodePrecondition, err.Error())
	}
	cfg, badCfg := loadConfig(env, "project.plan")
	if badCfg != nil {
		return *badCfg
	}
	st.Now = env.Now()
	st.TTL = time.Duration(cfg.Limits.PlanTTLHours) * time.Hour
	st.PortRange = [2]int{cfg.Limits.PortRange[0], cfg.Limits.PortRange[1]}
	st.MaxProjects = cfg.Limits.Projects
	st.RotatePassword = rotate
	st.Ciphertexts = map[string]string{}
	for _, c := range cts {
		name, path, ok := strings.Cut(c, "=")
		if !ok || name == "" || path == "" {
			return fail("project.plan", CodeUsage, "--ciphertext ожидает имя=файл")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return fail("project.plan", CodeIO, "не удалось прочитать шифртекст "+name+": "+err.Error())
		}
		st.Ciphertexts[name] = strings.TrimSpace(string(b))
	}

	pl, berr := plan.Build(p, st)
	if berr != nil {
		return result{command: "project.plan", err: &ErrorBody{Code: berr.Code, Message: berr.Message, Details: berr.Issues, Hint: berr.Hint}}
	}
	if out != "" {
		b, _ := json.MarshalIndent(pl, "", "  ")
		if err := os.WriteFile(out, append(b, '\n'), 0o600); err != nil {
			return fail("project.plan", CodeIO, "не удалось записать план: "+err.Error())
		}
	}
	steps := make([]stepView, 0, len(pl.Steps))
	var sb strings.Builder
	fmt.Fprintf(&sb, "План %s для проекта %s\nХэш: %s\nДействует до: %s\n\n", pl.ID, pl.Project, pl.Hash, pl.ExpiresAt)
	for _, s := range pl.Steps {
		steps = append(steps, stepView{ID: s.ID, Action: s.Action, Class: s.Class, Summary: s.Summary})
		fmt.Fprintf(&sb, "  %s  %-20s %s\n", s.ID, s.Action, s.Summary)
	}
	if out != "" {
		fmt.Fprintf(&sb, "\nПлан записан в %s. Изменений на сервере нет: план только описывает их.\n", out)
	}
	return result{command: "project.plan", data: map[string]any{"plan": pl, "steps": steps}, text: sb.String()}
}

func cmdApply(args []string, env Env) result {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		return fail("apply", CodeUsage, "использование: lite-deploy apply <plan.json|-> [--dry-run] [--yes]")
	}
	if !*dry {
		return fail("apply", CodeNotImplemented, "применение плана появится в S4; сейчас доступен только --dry-run (проверка плана)")
	}
	data, err := readInput(env, pos[0])
	if err != nil {
		return fail("apply", CodeIO, "не удалось прочитать план: "+err.Error())
	}
	st, err := plan.LoadState(env.FS, env.Paths)
	if err != nil {
		return fail("apply", plan.CodePrecondition, err.Error())
	}
	cfg, badCfg := loadConfig(env, "apply")
	if badCfg != nil {
		return *badCfg
	}
	lim := plan.DefaultLimits()
	lim.PlanBytes, lim.PlanSteps = cfg.Limits.PlanBytes, cfg.Limits.PlanSteps
	pl, verr := plan.Verify(data, plan.VerifyOptions{Now: env.Now(), ServerID: st.ServerID, Limits: lim})
	if verr != nil {
		return result{command: "apply", err: &ErrorBody{Code: verr.Code, Message: verr.Message, Details: verr.Issues}}
	}
	steps := make([]stepView, 0, len(pl.Steps))
	var sb strings.Builder
	fmt.Fprintf(&sb, "План %s проверен: хэш %s, шагов %d.\nСравнение с системой (would_change/unchanged) добавит S4; здесь шаги только проверены.\n\n", pl.ID, pl.Hash, len(pl.Steps))
	for _, s := range pl.Steps {
		steps = append(steps, stepView{ID: s.ID, Action: s.Action, State: "pending", Summary: s.Summary})
		fmt.Fprintf(&sb, "  %s  %-20s %s\n", s.ID, s.Action, s.Summary)
	}
	return result{command: "apply", data: map[string]any{"plan_id": pl.ID, "plan_hash": pl.Hash, "dry_run": true, "steps": steps}, text: sb.String()}
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
		sb.WriteString("Проектов под управлением lite-deploy нет.\n")
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
