package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
	"github.com/toxic-sunflower/lite-deploy/internal/serverconfig"
	"github.com/toxic-sunflower/lite-deploy/internal/setup"
)

// CodeInvalidSecret is an invalid secret name or value.
const CodeInvalidSecret = "invalid_secret"

func (e Env) keyPath() string {
	if e.KeyPath != "" {
		return e.KeyPath
	}
	return secrets.DefaultKeyPath
}

func (e Env) pubPath() string {
	if e.PubPath != "" {
		return e.PubPath
	}
	return secrets.DefaultPubPath
}

func (e Env) secretsDir() string {
	if e.SecretsDir != "" {
		return e.SecretsDir
	}
	return secrets.DefaultStoreDir
}

func (e Env) root() bool { return e.IsRoot != nil && e.IsRoot() }

// readSecretValue reads a secret value: hidden from the terminal or from standard input.
// The value is never taken from arguments or environment variables (SPEC-v0.md, 6.1).
func readSecretValue(env Env) ([]byte, error) {
	if f, ok := env.Stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) { //nolint:gosec // the terminal descriptor fits into an int
		_, _ = fmt.Fprint(env.Stderr, "Secret value (input hidden): ")
		b, err := term.ReadPassword(int(f.Fd())) //nolint:gosec // see above
		_, _ = fmt.Fprintln(env.Stderr)
		return b, err
	}
	b, err := io.ReadAll(io.LimitReader(env.Stdin, secrets.MaxValueBytes+3))
	if err != nil {
		return nil, err
	}
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
		if len(b) > 0 && b[len(b)-1] == '\r' {
			b = b[:len(b)-1]
		}
	}
	return b, nil
}

const initUsage = "usage: lite init [--config server.json] [--user name[,name…]] [--all-users] [--ufw keep|off|http|max] [--plugins caddy,podman,pg,redis] [--no-security] [--yes]"

// cmdInit prepares the server for work: the executor key and directories, administrator users with SSH keys,
// SSH login by key only, the firewall mode, the plugins. The only command that may be run directly as root.
func cmdInit(args []string, env Env) result {
	fl := flag.NewFlagSet("init", flag.ContinueOnError)
	var users listFlag
	fl.Var(&users, "user", "")
	allUsers := fl.Bool("all-users", false, "")
	ufw := fl.String("ufw", "max", "")
	plugins := fl.String("plugins", "", "")
	noSecurity := fl.Bool("no-security", false, "")
	configFile := fl.String("config", "", "")
	yes := fl.Bool("yes", false, "")
	pos, err := parseArgs(fl, args)
	if err != nil || len(pos) != 0 {
		return fail("init", CodeUsage, initUsage)
	}
	if !env.root() {
		return fail("init", "permission_denied", "init must run as root: lite init")
	}
	pub, created, err := secrets.EnsureKey(env.FS, env.keyPath(), env.pubPath())
	if err != nil {
		return fail("init", plan.CodePrecondition, err.Error())
	}
	for _, d := range []struct {
		path string
		perm uint32
	}{
		{"/etc/lite-deploy/projects", 0o755},
		{env.Paths.StateDir + "/projects", 0o755},
		{env.secretsDir(), 0o700},
	} {
		if err := env.FS.MkdirAll(d.path, os.FileMode(d.perm)); err != nil {
			return fail("init", CodeIO, "could not create "+d.path+": "+err.Error())
		}
	}
	split := func(vals []string) []string {
		var out []string
		for _, v := range vals {
			for _, x := range strings.Split(v, ",") {
				if x = strings.TrimSpace(x); x != "" {
					out = append(out, x)
				}
			}
		}
		return out
	}
	projects := projectNames(env)
	current := ""
	if env.Getenv != nil {
		current = env.Getenv("SUDO_USER")
	}
	stdin := env.Stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	se := setup.Env{Runner: env.Runner, FS: env.FS, In: bufio.NewReader(stdin), Out: env.Stderr, Projects: projects, Sleep: env.Sleep, Now: env.Now}
	opts := setup.Options{Users: split(users), AllUsers: *allUsers, Current: current, Firewall: *ufw, Plugins: withSecurity(split([]string{*plugins}), *noSecurity), Yes: *yes, WaitLogin: !*yes}
	var cfg *serverconfig.Config
	if *configFile != "" {
		raw, err := env.FS.ReadFile(*configFile)
		if err != nil {
			return fail("init", CodeIO, "could not read the configuration "+*configFile+": "+err.Error())
		}
		var bad []string
		if cfg, bad = serverconfig.Parse(raw); cfg == nil {
			return fail("init", CodeUsage, "the server configuration is not valid:\n  - "+strings.Join(bad, "\n  - "))
		}
		given := map[string]bool{}
		fl.Visit(func(f *flag.Flag) { given[f.Name] = true })
		opts = applyServerConfig(opts, cfg, given, *noSecurity)
	}
	if err := se.Apply(context.Background(), opts); err != nil {
		return fail("init", plan.CodePrecondition, err.Error())
	}
	if cfg != nil && cfg.Backup != nil {
		args := []string{cfg.Backup.Recipient}
		if cfg.Backup.EncryptDumps {
			args = append(args, "--encrypt-dumps")
		}
		pe := env.pluginEnv()
		if _, err := (builtin.Backup{}).Run(context.Background(), pe, "setup", args); err != nil {
			return fail("init", plan.CodePrecondition, "backups from the configuration: "+err.Error())
		}
		if cfg.Backup.Schedule {
			if _, err := (builtin.Backup{}).Run(context.Background(), pe, "schedule", []string{"on"}); err != nil {
				return fail("init", plan.CodePrecondition, "the backup schedule from the configuration: "+err.Error())
			}
		}
	}
	text := "The executor key already exists and was not changed.\n"
	if created {
		text = "Executor key created: " + env.keyPath() + " (mode 600).\n"
	}
	text += "Public key (safe to show): " + pub + "\n"
	return result{command: "init", data: map[string]any{"public_key": pub, "created": created}, text: text}
}

func cmdSecret(args []string, env Env) result {
	if len(args) == 0 {
		return fail("secret", CodeUsage, "usage: lite-deploy secret pubkey|encrypt|set|list|put|remove")
	}
	switch args[0] {
	case "pubkey":
		return secretPubkey(env)
	case "encrypt":
		return secretEncrypt(args[1:], env)
	case "set":
		return secretSet(args[1:], env)
	case "list":
		return secretList(args[1:], env)
	case "put":
		return secretPut(args[1:], env)
	case "remove":
		return secretRemove(args[1:], env)
	}
	return fail("secret", CodeUsage, "unknown secret subcommand: "+args[0])
}

func secretPubkey(env Env) result {
	b, err := env.FS.ReadFile(env.pubPath())
	if err != nil {
		return fail("secret.pubkey", plan.CodePrecondition, "public key not found in "+env.pubPath()+": run sudo lite-deploy init on the server")
	}
	pub := strings.TrimSpace(string(b))
	return result{command: "secret.pubkey", data: map[string]any{"public_key": pub}, text: pub + "\n"}
}

func secretErr(command string, err error) result {
	switch {
	case errors.Is(err, secrets.ErrInvalid):
		return fail(command, CodeInvalidSecret, err.Error())
	default:
		return fail(command, CodeIO, err.Error())
	}
}

func secretEncrypt(args []string, env Env) result {
	fs := flag.NewFlagSet("secret.encrypt", flag.ContinueOnError)
	pubFlag := fs.String("pubkey", "", "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 2 {
		return fail("secret.encrypt", CodeUsage, "usage: lite-deploy secret encrypt <project> <name> [--pubkey age1…]; the value is read from standard input or hidden from the terminal")
	}
	pub := strings.TrimSpace(*pubFlag)
	if pub == "" {
		b, err := env.FS.ReadFile(env.pubPath())
		if err != nil {
			return fail("secret.encrypt", plan.CodePrecondition, "no executor public key: pass --pubkey or run sudo lite-deploy init on the server")
		}
		pub = string(b)
	}
	val, err := readSecretValue(env)
	if err != nil {
		return fail("secret.encrypt", CodeIO, "could not read the value: "+err.Error())
	}
	ct, err := secrets.Encrypt(pub, pos[0], pos[1], val, env.Now())
	if err != nil {
		return secretErr("secret.encrypt", err)
	}
	return result{command: "secret.encrypt", data: map[string]any{"project": pos[0], "name": pos[1], "ciphertext": ct}, text: ct + "\n"}
}

func secretSet(args []string, env Env) result {
	fs := flag.NewFlagSet("secret.set", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 2 {
		return fail("secret.set", CodeUsage, "usage: lite-deploy secret set <project> <name>; the value is read from standard input or hidden from the terminal")
	}
	if !env.root() {
		return fail("secret.set", "permission_denied", "secret set must run as root: the secret store is root-only")
	}
	val, err := readSecretValue(env)
	if err != nil {
		return fail("secret.set", CodeIO, "could not read the value: "+err.Error())
	}
	changed, err := secrets.NewStoreAt(env.FS, env.secretsDir()).Put(pos[0], pos[1], val)
	if err != nil {
		return secretErr("secret.set", err)
	}
	word := "unchanged"
	if changed {
		word = "saved"
	}
	// the value and the ciphertext are not printed
	return result{command: "secret.set", data: map[string]any{"project": pos[0], "name": pos[1], "changed": changed},
		text: fmt.Sprintf("Secret %s of project %s %s.\n", pos[1], pos[0], word)}
}

// scope maps the name the interface uses for the shared secrets ("shared") to the store that holds them.
func scope(project string) string {
	if project == "shared" {
		return secrets.SharedProject
	}
	return project
}

func secretList(args []string, env Env) result {
	fs := flag.NewFlagSet("secret.list", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		return fail("secret.list", CodeUsage, "usage: lite-deploy secret list <project|shared>")
	}
	pos[0] = scope(pos[0])
	type item struct {
		Name      string  `json:"name"`
		UpdatedAt *string `json:"updated_at"`
	}
	items := []item{}
	if entries, err := secrets.NewStoreAt(env.FS, env.secretsDir()).List(pos[0]); err == nil {
		for _, e := range entries {
			ts := e.UpdatedAt.Format(time.RFC3339)
			items = append(items, item{e.Name, &ts})
		}
	} else if errors.Is(err, secrets.ErrInvalid) {
		return fail("secret.list", CodeInvalidSecret, err.Error())
	} else {
		// without root the secrets directory is not accessible: names are taken from the non-secret project state (D-49), there is no time there
		st, serr := plan.LoadState(env.FS, env.Paths)
		if serr != nil {
			return fail("secret.list", plan.CodePrecondition, serr.Error())
		}
		ps, ok := st.Projects[pos[0]]
		if !ok {
			return fail("secret.list", CodeProjectMissing, "project "+pos[0]+" not found")
		}
		for _, n := range ps.Secrets {
			items = append(items, item{n, nil})
		}
	}
	var sb strings.Builder
	for _, it := range items {
		when := "time unknown"
		if it.UpdatedAt != nil {
			when = *it.UpdatedAt
		}
		fmt.Fprintf(&sb, "%s\t%s\n", it.Name, when)
	}
	if len(items) == 0 {
		sb.WriteString("No secrets.\n")
	}
	shared := []string{}
	if entries, err := secrets.NewStoreAt(env.FS, env.secretsDir()).List(secrets.SharedProject); err == nil {
		for _, e := range entries {
			shared = append(shared, e.Name)
		}
	}
	return result{command: "secret.list", data: map[string]any{"secrets": items, "needed": neededSecrets(env, pos[0]), "shared": shared}, text: sb.String()}
}

var secretRefRe = regexp.MustCompile(`secret://((?:shared/)?[a-z][a-z0-9_]{1,63})`)

// neededSecrets are the secrets the description of a project refers to (names only), without the internal ones.
func neededSecrets(env Env, project string) []string {
	b, err := env.FS.ReadFile("/etc/lite-deploy/projects/" + project + ".json")
	if err != nil {
		return []string{}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, m := range secretRefRe.FindAllStringSubmatch(string(b), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// secretPut stores a secret that arrived encrypted with the executor key (the interface encrypts it on the person's computer).
// The value is read from the ciphertext here, as root, and written to the store; it never appears in an answer.
func secretPut(args []string, env Env) result {
	const command = "secret.put"
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 2 {
		return fail(command, CodeUsage, "usage: lite-deploy secret put <project> <name> (the age ciphertext from `secret encrypt` on standard input)")
	}
	if !env.root() {
		return fail(command, "permission_denied", "secret put runs as root, through the agent")
	}
	if !secrets.ValidName(pos[1]) || strings.HasPrefix(pos[1], "_") {
		return fail(command, CodeInvalidSecret, "invalid secret name")
	}
	pos[0] = scope(pos[0])
	if pos[0] != secrets.SharedProject {
		if _, err := env.FS.Stat("/etc/lite-deploy/projects/" + pos[0] + ".json"); err != nil {
			return fail(command, CodeProjectMissing, "project "+pos[0]+" not found")
		}
	}
	raw, err := io.ReadAll(io.LimitReader(env.Stdin, secrets.MaxCiphertextSize+1))
	if err != nil || len(raw) > secrets.MaxCiphertextSize {
		return fail(command, CodeInvalidSecret, "the ciphertext is missing or too large")
	}
	key, err := env.FS.ReadFile(env.keyPath())
	if err != nil {
		return fail(command, plan.CodePrecondition, "the executor key could not be read: run lite init")
	}
	val, err := secrets.Decrypt(string(key), strings.TrimSpace(string(raw)), pos[0], pos[1])
	if err != nil {
		return secretErr(command, err)
	}
	changed, err := secrets.NewStoreAt(env.FS, env.secretsDir()).Put(pos[0], pos[1], val)
	if err != nil {
		return secretErr(command, err)
	}
	return result{command: command, data: map[string]any{"project": pos[0], "name": pos[1], "changed": changed}, text: "The secret " + pos[1] + " of " + pos[0] + " is saved.\nNext: deploy the project again so that the new value is used.\n"}
}

// secretRemove deletes one secret of a project.
func secretRemove(args []string, env Env) result {
	const command = "secret.remove"
	pos := args
	if len(pos) != 2 {
		return fail(command, CodeUsage, "usage: lite-deploy secret remove <project> <name>")
	}
	if !env.root() {
		return fail(command, "permission_denied", "secret remove runs as root, through the agent")
	}
	pos[0] = scope(pos[0])
	if !secrets.ValidName(pos[1]) || strings.HasPrefix(pos[1], "_") || !secrets.ValidProject(pos[0]) {
		return fail(command, CodeInvalidSecret, "invalid project or secret name")
	}
	p := env.secretsDir() + "/" + pos[0] + "/" + pos[1]
	if err := env.FS.Remove(p); err != nil {
		return fail(command, CodeIO, "the secret is not there or could not be removed: "+err.Error())
	}
	return result{command: command, data: map[string]any{"removed": true}, text: "The secret " + pos[1] + " was removed.\n"}
}

// withSecurity adds the security plugin (SSH by key only, fail2ban, automatic updates, kernel parameters) to the plugin
// list unless the person asked not to: a server is protected right after init (D-72).
func withSecurity(plugins []string, skip bool) []string {
	if skip {
		return plugins
	}
	for _, p := range plugins {
		if p == "security" {
			return plugins
		}
	}
	return append(plugins, "security")
}
