package builtin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"regexp"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// Podman is the `podman` plugin: the container engine for projects.
type Podman struct{}

func (Podman) Name() string       { return "podman" }
func (Podman) Kind() string       { return "builtin" }
func (Podman) Requires() []string { return nil }
func (Podman) Description() string {
	return "project containers: podman (rootless, systemd services)"
}
func (Podman) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "install podman", Confirm: true},
		{Name: "status", Help: "podman version"},
		{Name: "ps", Root: true, Args: "[PROJECT]", Help: "project containers (each project has its own rootless podman)"},
	}
}

// projectsOf are the projects for a command: the given one or all.
func projectsOf(env plugin.Env, args []string) ([]string, error) {
	if len(args) == 0 {
		return env.Projects, nil
	}
	for _, p := range env.Projects {
		if p == args[0] {
			return []string{p}, nil
		}
	}
	return nil, plugin.Errorf("usage", "no project %q", args[0])
}

func podmanPackages(manager string) []string {
	if manager == "apt" {
		return []string{"podman", "uidmap", "dbus-user-session", "libpam-systemd"} // the last two are needed by user systemd services (rootless Quadlet)
	}
	return []string{"podman"}
}

// InstallPodman installs podman.
func InstallPodman(ctx context.Context, env plugin.Env, say Say) error {
	t := tools{ctx, env}
	say.say("Plugin podman: installing packages...")
	return t.install(podmanPackages(t.manager())...)
}

func (Podman) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	t := tools{ctx, env}
	if cmd == "ps" {
		if len(args) > 1 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite podman ps [PROJECT]")
		}
		list, err := projectsOf(env, args)
		if err != nil {
			return plugin.Output{}, err
		}
		var sb strings.Builder
		data := map[string]string{}
		for _, p := range list {
			res, err := t.run("runuser", "-u", p, "--", "podman", "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Status}}")
			if err != nil || res.ExitCode != 0 {
				data[p] = "error"
				fmt.Fprintf(&sb, "%s: could not list containers\n", p)
				continue
			}
			out := strings.TrimSpace(string(res.Stdout))
			data[p] = out
			for _, l := range strings.Split(out, "\n") {
				if l != "" {
					fmt.Fprintf(&sb, "%s\t%s\n", p, l)
				}
			}
		}
		return plugin.Output{Text: sb.String(), Data: map[string]any{"containers": data}}, nil
	}
	if err := noArgs(args, cmd, "podman"); err != nil {
		return plugin.Output{}, err
	}
	switch cmd {
	case "install":
		if err := InstallPodman(ctx, env, nil); err != nil {
			return plugin.Output{}, err
		}
		return textOut("podman is installed.\nNext: create a project that runs containers (lite-ui or lite project create).\n"), nil
	case "status":
		res, err := t.run("podman", "--version")
		if err != nil || res.ExitCode != 0 {
			e := plugin.Errorf("precondition_failed", "podman is not installed")
			e.Hint = "lite podman install"
			return plugin.Output{}, e
		}
		return textOut(string(res.Stdout)), nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin podman has no command %q", cmd)
}

func (Podman) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	res, err := t.run("podman", "--version")
	if err != nil || res.ExitCode != 0 {
		return []plugin.Finding{finding("podman", "installed", plugin.LevelOK, "podman is not installed (not in use)", "")}
	}
	v := strings.TrimSpace(string(res.Stdout))
	// podman 4.4+ is acceptable, below 5.0 there is a warning (as in verify_host)
	var maj, min int
	if _, err := fmt.Sscanf(v, "podman version %d.%d", &maj, &min); err == nil && (maj < 4 || (maj == 4 && min < 4)) {
		return []plugin.Finding{finding("podman", "version", plugin.LevelFail, v+": version 4.4 or newer is required", "lite package update podman")}
	} else if err == nil && maj < 5 {
		return []plugin.Finding{finding("podman", "version", plugin.LevelWarn, v+": version is below 5.0, some features are unavailable", "")}
	}
	out := []plugin.Finding{finding("podman", "version", plugin.LevelOK, v, "")}
	if t.ok("docker", "--version") {
		out = append(out, finding("podman", "docker", plugin.LevelWarn, "Docker is installed on the server: lite does not use it; access to the Docker daemon is root-equivalent, better remove it", "sudo apt-get remove docker.io docker-ce"))
	}
	for _, p := range env.Projects {
		res, err := t.run("loginctl", "show-user", p, "-p", "Linger", "--value")
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(string(res.Stdout)) != "yes" {
			out = append(out, finding("podman", "linger-"+p, plugin.LevelFail, "user "+p+" has no linger: containers stop when the user logs out and do not start after a reboot", "sudo loginctl enable-linger "+p))
		}
	}
	return out
}

// PG is the `pg` plugin: PostgreSQL on the host, shared by projects.
type PG struct{}

func (PG) Name() string       { return "pg" }
func (PG) Kind() string       { return "builtin" }
func (PG) Requires() []string { return nil }
func (PG) Description() string {
	return "PostgreSQL on the host: one shared server, each project gets its own database and role"
}
func (PG) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "install PostgreSQL", Confirm: true},
		{Name: "status", Help: "version and service state"},
		{Name: "databases", Root: true, Help: "list of databases"},
		{Name: "roles", Root: true, Help: "list of roles"},
		{Name: "create-db", Confirm: true, Args: "DATABASE [ROLE] [--password-stdin] [--project P --secret NAME [--template TEXT]]", Help: "create a database and a role (password in /etc/lite-deploy/secrets/pg-ROLE, root only; or your own password from stdin); with --project and --secret also let the role log in over the socket and store the ready connection string as that secret of the project (--template: your own notation with {host} {port} {user} {password} {db})"},
		{Name: "drop-db", Confirm: true, Args: "DATABASE", Help: "delete a database (the role stays)"},
		{Name: "backup", Root: true, Args: "[DATABASE]", Help: "back up a database (or all databases) into /var/backups/lite-deploy/pg, the latest 7 are kept"},
		{Name: "backups", Root: true, Help: "list of backups"},
		{Name: "restore", Confirm: true, Args: "DATABASE FILE [--as OTHER_DATABASE] [--identity-stdin]", Help: "restore a database from a backup (current data is replaced; --as restores into another existing database); for .age give the private key on stdin"},
		{Name: "schedule", Confirm: true, Args: "on|off", Help: "daily backup with a systemd timer"},
		{Name: "password", Confirm: true, Args: "ROLE --password-stdin", Help: "set a role password (from standard input)"},
		{Name: "listen", Confirm: true, Args: "ADDRESS|localhost", Help: "also accept connections on this server's address (restarts PostgreSQL)"},
		{Name: "allow", Confirm: true, Args: "DATABASE ROLE FROM", Help: "let a role reach a database from another server (ssl, password, port 5432 only for this address)"},
		{Name: "deny", Confirm: true, Args: "DATABASE ROLE FROM", Help: "remove such a permission"},
		{Name: "access", Root: true, Help: "who has remote access"},
	}
}

// pgSecretDir is the directory of role passwords; only root reads it.
const pgSecretDir = "/etc/lite-deploy/secrets" //nolint:gosec // this is a directory path, not a password

var pgNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// createDB creates a role (with a password from a file that is created if needed) and a database. A repeated call is safe.
func (t tools) createDB(db, role, given string) error {
	if !pgNameRe.MatchString(db) || !pgNameRe.MatchString(role) {
		return plugin.Errorf("usage", "database and role names: lowercase Latin letters, digits and _, up to 40 characters, starting with a letter")
	}
	secret := pgSecretDir + "/pg-" + role
	pass := given
	if pass == "" {
		if b, err := t.env.FS.ReadFile(secret); err == nil {
			pass = strings.TrimSpace(string(b))
		}
	}
	if pass == "" || given != "" {
		if given == "" {
			p, err := randomHex(24)
			if err != nil {
				return plugin.Errorf("internal", "could not get random bytes: %v", err)
			}
			pass = p
		}
		if err := t.env.FS.MkdirAll(pgSecretDir, 0o700); err != nil {
			return plugin.Errorf("step_failed", "directory %s: %v", pgSecretDir, err)
		}
		if err := t.env.FS.WriteFile(secret, []byte(pass+"\n"), 0o600); err != nil {
			return plugin.Errorf("step_failed", "file %s: %v", secret, err)
		}
	}
	// the names were checked above, the password is hexadecimal: substitution into SQL is safe
	exists, err := t.psql(fmt.Sprintf("select 1 from pg_roles where rolname='%s'", role))
	if err != nil {
		return err
	}
	verb := "create"
	if strings.TrimSpace(exists) != "" {
		verb = "alter"
	}
	if err := t.psqlSecret(fmt.Sprintf("%s role %s login password '%s'", verb, role, pass)); err != nil {
		return err
	}
	have, err := t.psql(fmt.Sprintf("select 1 from pg_database where datname='%s'", db))
	if err != nil {
		return err
	}
	if strings.TrimSpace(have) == "" {
		if _, err := t.psql(fmt.Sprintf("create database %s owner %s", db, role)); err != nil {
			return err
		}
	}
	return nil
}

func pgPackages(manager string) []string {
	switch manager {
	case "apt":
		return []string{"postgresql"}
	case "pacman":
		return []string{"postgresql"}
	}
	return []string{"postgresql-server"}
}

// InstallPG installs PostgreSQL.
func InstallPG(ctx context.Context, env plugin.Env, say Say) error {
	t := tools{ctx, env}
	say.say("Plugin pg: installing packages...")
	return t.install(pgPackages(t.manager())...)
}

// psqlSecret runs SQL with a password: the text goes through stdin and does not appear in the process list.
func (t tools) psqlSecret(sql string) error {
	res, err := t.env.Runner.Run(t.ctx, host.Cmd{Name: "runuser", Args: []string{"-u", "postgres", "--", "psql", "-At", "-v", "ON_ERROR_STOP=1"}, Stdin: []byte(sql + "\n"), Timeout: 5 * time.Minute})
	if err != nil {
		return plugin.Errorf("step_failed", "psql: %v", err)
	}
	if res.ExitCode != 0 {
		return plugin.Errorf("step_failed", "psql: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

func (t tools) psql(sql string) (string, error) {
	res, err := t.run("runuser", "-u", "postgres", "--", "psql", "-Atc", sql)
	if err != nil {
		return "", plugin.Errorf("step_failed", "psql: %v", err)
	}
	if res.ExitCode != 0 {
		return "", plugin.Errorf("step_failed", "psql: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return string(res.Stdout), nil
}

func (PG) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	t := tools{ctx, env}
	switch cmd {
	case "listen":
		return t.pgListen(args)
	case "allow":
		return t.pgAllow(args)
	case "deny":
		return t.pgDeny(args)
	case "access":
		if len(args) != 0 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite pg access")
		}
		return t.pgAccess()
	case "backup":
		return t.pgBackup(args)
	case "backups":
		return t.pgBackupsList()
	case "restore":
		return t.pgRestore(args)
	case "schedule":
		return t.pgSchedule(args)
	case "password":
		rest, stdin := extractPasswordFlag(args)
		if len(rest) != 1 || !stdin || !pgNameRe.MatchString(rest[0]) {
			return plugin.Output{}, plugin.Errorf("usage", "usage: echo PASSWORD | lite pg password ROLE --password-stdin")
		}
		pass, err := readPassword(env.Stdin)
		if err != nil {
			return plugin.Output{}, err
		}
		if err := t.setPassword(rest[0], pass); err != nil {
			return plugin.Output{}, err
		}
		return textOut("Password of role " + rest[0] + " changed.\n"), nil
	case "create-db":
		args, stdin := extractPasswordFlag(args)
		var project, secret, template string
		var rest []string
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--project", "--secret", "--template":
				if i+1 >= len(args) {
					return plugin.Output{}, plugin.Errorf("usage", "%s needs a value", args[i])
				}
				switch args[i] {
				case "--project":
					project = args[i+1]
				case "--secret":
					secret = args[i+1]
				default:
					template = args[i+1]
				}
				i++
			default:
				rest = append(rest, args[i])
			}
		}
		args = rest
		if len(args) < 1 || len(args) > 2 || (secret != "") != (project != "") {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite pg create-db DATABASE [ROLE] [--password-stdin] [--project PROJECT --secret NAME [--template TEXT]]")
		}
		role := args[0]
		if len(args) == 2 {
			role = args[1]
		}
		given := ""
		if stdin {
			var err error
			if given, err = readPassword(env.Stdin); err != nil {
				return plugin.Output{}, err
			}
		}
		if err := t.createDB(args[0], role, given); err != nil {
			return plugin.Output{}, err
		}
		if secret == "" {
			return plugin.Output{Text: fmt.Sprintf("Database %s and role %s are ready; password: %s/pg-%s\n", args[0], role, pgSecretDir, role),
				Data: map[string]any{"database": args[0], "role": role, "secret": pgSecretDir + "/pg-" + role}}, nil
		}
		// for a project: the role may log in over the socket, and the ready connection string becomes a secret of the project
		pw, err := env.FS.ReadFile(pgSecretDir + "/pg-" + role)
		if err != nil {
			return plugin.Output{}, plugin.Errorf("io_error", "%v", err)
		}
		conn, err := pgConnection(template, role, args[0], strings.TrimSpace(string(pw)))
		if err != nil {
			return plugin.Output{}, err
		}
		if err := t.ensureHba(role); err != nil {
			return plugin.Output{}, err
		}
		if err := t.storeConnection(project, secret, conn); err != nil {
			return plugin.Output{}, err
		}
		return plugin.Output{Text: fmt.Sprintf("Database %s and role %s are ready. The connection is the secret %s of project %s (secret://%s); a container shares %s.\n", args[0], role, secret, project, secret, pgSocketDir),
			Data: map[string]any{"database": args[0], "role": role, "project": project, "secret": secret, "socket": pgSocketDir}}, nil
	case "drop-db":
		if len(args) != 1 || !pgNameRe.MatchString(args[0]) {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite pg drop-db DATABASE")
		}
		if _, err := t.psql("drop database " + args[0]); err != nil {
			return plugin.Output{}, err
		}
		return textOut("Database " + args[0] + " deleted.\n"), nil
	}
	if err := noArgs(args, cmd, "pg"); err != nil {
		return plugin.Output{}, err
	}
	switch cmd {
	case "install":
		if err := InstallPG(ctx, env, nil); err != nil {
			return plugin.Output{}, err
		}
		return textOut("PostgreSQL is installed.\nNext: create a project with a database, and set up backups: lite backup setup age1…\n"), nil
	case "status":
		if !t.ok("psql", "--version") {
			e := plugin.Errorf("precondition_failed", "PostgreSQL is not installed")
			e.Hint = "lite pg install"
			return plugin.Output{}, e
		}
		res, _ := t.run("psql", "--version")
		active := t.ok("systemctl", "is-active", "--quiet", "postgresql")
		v := strings.TrimSpace(string(res.Stdout))
		sched := t.ok("systemctl", "is-enabled", "--quiet", "lite-pg-backup.timer")
		return plugin.Output{Text: fmt.Sprintf("%s, service %s\n", v, map[bool]string{true: "running", false: "not working"}[active]),
			Data: map[string]any{"version": v, "active": active, "scheduled": sched}}, nil
	case "databases", "roles":
		sql := "select datname from pg_database where not datistemplate order by 1"
		if cmd == "roles" {
			sql = "select rolname from pg_roles where rolname !~ '^pg_' order by 1"
		}
		out, err := t.psql(sql)
		if err != nil {
			return plugin.Output{}, err
		}
		list := []string{}
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" {
				list = append(list, l)
			}
		}
		return plugin.Output{Text: out, Data: map[string]any{cmd: list}}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin pg has no command %q", cmd)
}

func (PG) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if !t.ok("psql", "--version") {
		return []plugin.Finding{finding("pg", "installed", plugin.LevelOK, "PostgreSQL is not installed (not in use)", "")}
	}
	if !t.ok("systemctl", "is-active", "--quiet", "postgresql") {
		return []plugin.Finding{finding("pg", "service", plugin.LevelFail, "the PostgreSQL service is not running", "sudo systemctl enable --now postgresql")}
	}
	out := []plugin.Finding{finding("pg", "service", plugin.LevelOK, "the PostgreSQL service is running", "")}
	out = append(out, t.backupFindings()...)
	out = append(out, t.hbaFindings()...)
	if la, err := t.psql("show listen_addresses"); err == nil {
		la = strings.TrimSpace(la)
		if bindLocalOnly(strings.ReplaceAll(la, ",", " ")) || la == "localhost" {
			out = append(out, finding("pg", "listen", plugin.LevelOK, "PostgreSQL listens on localhost only", ""))
		} else {
			out = append(out, finding("pg", "listen", plugin.LevelWarn, "PostgreSQL listens on "+la+": make sure the port is closed by the firewall for foreign addresses", "lite firewall status"))
		}
	}
	return out
}

// PluginNames are the names of the plugins that lite init --plugins installs (postgres is accepted as the old name of pg).
func PluginNames() []string {
	return []string{"caddy", "pg", "podman", "redis", "cloudflare", "security", "backblaze", "s3", "sftp"}
}

// InstallPlugin installs a plugin by name for lite init. For security the whole server protection is enabled.
func InstallPlugin(ctx context.Context, env plugin.Env, name string, say Say) error {
	switch name {
	case "caddy":
		return InstallCaddy(ctx, env, say)
	case "podman":
		return InstallPodman(ctx, env, say)
	case "pg", "postgres":
		return InstallPG(ctx, env, say)
	case "redis":
		return InstallRedis(ctx, env, say)
	case "cloudflare":
		return InstallCloudflare(ctx, env, say)
	case "backblaze", "s3", "sftp":
		return InstallRclone(ctx, env, say)
	case "security":
		say.say("Plugin security: enabling server protection...")
		_, err := Security{}.Run(ctx, env, "enable", nil)
		return err
	}
	return plugin.Errorf("usage", "unknown plugin %q (available: %s)", name, strings.Join(PluginNames(), ", "))
}

// setPassword changes the password of an existing role and updates the secret file.
func (t tools) setPassword(role, pass string) error {
	have, err := t.psql(fmt.Sprintf("select 1 from pg_roles where rolname='%s'", role))
	if err != nil {
		return err
	}
	if strings.TrimSpace(have) == "" {
		return plugin.Errorf("precondition_failed", "role %s does not exist", role)
	}
	if err := t.env.FS.MkdirAll(pgSecretDir, 0o700); err != nil {
		return plugin.Errorf("step_failed", "directory %s: %v", pgSecretDir, err)
	}
	if err := t.env.FS.WriteFile(pgSecretDir+"/pg-"+role, []byte(pass+"\n"), 0o600); err != nil {
		return plugin.Errorf("step_failed", "secret file: %v", err)
	}
	return t.psqlSecret(fmt.Sprintf("alter role %s password '%s'", role, pass))
}

// Installed reports whether podman is on this server.
func (Podman) Installed(ctx context.Context, env plugin.Env) bool {
	return tools{ctx, env}.ok("podman", "--version")
}

// Installed reports whether PostgreSQL is on this server.
func (PG) Installed(ctx context.Context, env plugin.Env) bool {
	return tools{ctx, env}.ok("psql", "--version")
}
