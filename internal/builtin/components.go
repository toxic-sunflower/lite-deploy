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

// Podman плагин `podman`: движок контейнеров для проектов.
type Podman struct{}

func (Podman) Name() string       { return "podman" }
func (Podman) Kind() string       { return "builtin" }
func (Podman) Requires() []string { return nil }
func (Podman) Description() string {
	return "контейнеры проектов: podman (rootless, службы systemd)"
}
func (Podman) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "поставить podman", Confirm: true},
		{Name: "status", Help: "версия podman"},
		{Name: "ps", Root: true, Args: "[ПРОЕКТ]", Help: "контейнеры проектов (у каждого проекта свой rootless podman)"},
	}
}

// projectsOf проекты для команды: указанный или все.
func projectsOf(env plugin.Env, args []string) ([]string, error) {
	if len(args) == 0 {
		return env.Projects, nil
	}
	for _, p := range env.Projects {
		if p == args[0] {
			return []string{p}, nil
		}
	}
	return nil, plugin.Errorf("usage", "нет проекта %q", args[0])
}

func podmanPackages(manager string) []string {
	if manager == "apt" {
		return []string{"podman", "uidmap", "dbus-user-session", "libpam-systemd"} // две последние нужны пользовательским службам systemd (rootless Quadlet)
	}
	return []string{"podman"}
}

// InstallPodman ставит podman.
func InstallPodman(ctx context.Context, env plugin.Env, say Say) error {
	t := tools{ctx, env}
	say.say("Плагин podman: ставлю пакеты...")
	return t.install(podmanPackages(t.manager())...)
}

func (Podman) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	t := tools{ctx, env}
	if cmd == "ps" {
		if len(args) > 1 {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite podman ps [ПРОЕКТ]")
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
				data[p] = "ошибка"
				fmt.Fprintf(&sb, "%s: не удалось получить список контейнеров\n", p)
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
		return textOut("podman установлен.\n"), nil
	case "status":
		res, err := t.run("podman", "--version")
		if err != nil || res.ExitCode != 0 {
			e := plugin.Errorf("precondition_failed", "podman не установлен")
			e.Hint = "sudo lite podman install"
			return plugin.Output{}, e
		}
		return textOut(string(res.Stdout)), nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "у плагина podman нет команды %q", cmd)
}

func (Podman) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	res, err := t.run("podman", "--version")
	if err != nil || res.ExitCode != 0 {
		return []plugin.Finding{finding("podman", "installed", plugin.LevelOK, "podman не установлен (не используется)", "")}
	}
	v := strings.TrimSpace(string(res.Stdout))
	// podman 4.4+ допустим, ниже 5.0 есть предупреждение (как в verify_host)
	var maj, min int
	if _, err := fmt.Sscanf(v, "podman version %d.%d", &maj, &min); err == nil && (maj < 4 || (maj == 4 && min < 4)) {
		return []plugin.Finding{finding("podman", "version", plugin.LevelFail, v+": нужна версия 4.4 или новее", "sudo lite package update podman")}
	} else if err == nil && maj < 5 {
		return []plugin.Finding{finding("podman", "version", plugin.LevelWarn, v+": версия ниже 5.0, часть возможностей недоступна", "")}
	}
	out := []plugin.Finding{finding("podman", "version", plugin.LevelOK, v, "")}
	if t.ok("docker", "--version") {
		out = append(out, finding("podman", "docker", plugin.LevelWarn, "на сервере установлен Docker: lite его не использует; доступ к демону Docker равен root, лучше удалить", "sudo apt-get remove docker.io docker-ce"))
	}
	for _, p := range env.Projects {
		res, err := t.run("loginctl", "show-user", p, "-p", "Linger", "--value")
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(string(res.Stdout)) != "yes" {
			out = append(out, finding("podman", "linger-"+p, plugin.LevelFail, "у пользователя "+p+" не включён linger: контейнеры остановятся при выходе и не поднимутся после перезагрузки", "sudo loginctl enable-linger "+p))
		}
	}
	return out
}

// PG плагин `pg`: PostgreSQL на хосте, общий для проектов.
type PG struct{}

func (PG) Name() string       { return "pg" }
func (PG) Kind() string       { return "builtin" }
func (PG) Requires() []string { return nil }
func (PG) Description() string {
	return "PostgreSQL на хосте: общий сервер, у проекта своя база и роль"
}
func (PG) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "поставить PostgreSQL", Confirm: true},
		{Name: "status", Help: "версия и состояние службы"},
		{Name: "databases", Root: true, Help: "список баз"},
		{Name: "roles", Root: true, Help: "список ролей"},
		{Name: "create-db", Confirm: true, Args: "БАЗА [РОЛЬ] [--password-stdin]", Help: "создать базу и роль (пароль в /etc/lite-deploy/secrets/pg-РОЛЬ, только root; свой пароль из stdin)"},
		{Name: "drop-db", Confirm: true, Args: "БАЗА", Help: "удалить базу (роль остаётся)"},
		{Name: "backup", Root: true, Args: "[БАЗА]", Help: "снять копию базы (или всех баз) в /var/backups/lite-deploy/pg, хранится 7 последних"},
		{Name: "backups", Root: true, Help: "список копий"},
		{Name: "restore", Confirm: true, Args: "БАЗА ФАЙЛ [--identity-stdin]", Help: "вернуть данные базы из копии (текущие данные заменяются); для .age закрытый ключ со stdin"},
		{Name: "schedule", Confirm: true, Args: "on|off", Help: "ежедневная копия по таймеру systemd"},
		{Name: "password", Confirm: true, Args: "РОЛЬ --password-stdin", Help: "задать пароль роли (из стандартного ввода)"},
		{Name: "listen", Confirm: true, Args: "АДРЕС|localhost", Help: "принимать соединения ещё и на адресе этого сервера (перезапускает PostgreSQL)"},
		{Name: "allow", Confirm: true, Args: "БАЗА РОЛЬ ОТКУДА", Help: "пустить роль к базе с другого сервера (ssl, пароль, порт 5432 только для этого адреса)"},
		{Name: "deny", Confirm: true, Args: "БАЗА РОЛЬ ОТКУДА", Help: "убрать такое разрешение"},
		{Name: "access", Root: true, Help: "кому открыт удалённый доступ"},
	}
}

// pgSecretDir каталог паролей ролей; читает только root.
const pgSecretDir = "/etc/lite-deploy/secrets" //nolint:gosec // это путь к каталогу, а не пароль

var pgNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// createDB создаёт роль (с паролем из файла, который создаётся при необходимости) и базу. Повторный вызов безопасен.
func (t tools) createDB(db, role, given string) error {
	if !pgNameRe.MatchString(db) || !pgNameRe.MatchString(role) {
		return plugin.Errorf("usage", "имя базы и роли: строчные латинские буквы, цифры и _, до 40 знаков, начинается с буквы")
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
				return plugin.Errorf("internal", "не удалось получить случайные байты: %v", err)
			}
			pass = p
		}
		if err := t.env.FS.MkdirAll(pgSecretDir, 0o700); err != nil {
			return plugin.Errorf("step_failed", "каталог %s: %v", pgSecretDir, err)
		}
		if err := t.env.FS.WriteFile(secret, []byte(pass+"\n"), 0o600); err != nil {
			return plugin.Errorf("step_failed", "файл %s: %v", secret, err)
		}
	}
	// имена проверены выше, пароль шестнадцатеричный: подстановка в SQL безопасна
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

// InstallPG ставит PostgreSQL.
func InstallPG(ctx context.Context, env plugin.Env, say Say) error {
	t := tools{ctx, env}
	say.say("Плагин pg: ставлю пакеты...")
	return t.install(pgPackages(t.manager())...)
}

// psqlSecret выполняет SQL с паролем: текст идёт через stdin и не попадает в список процессов.
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
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite pg access")
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
			return plugin.Output{}, plugin.Errorf("usage", "использование: echo ПАРОЛЬ | lite pg password РОЛЬ --password-stdin")
		}
		pass, err := readPassword(env.Stdin)
		if err != nil {
			return plugin.Output{}, err
		}
		if err := t.setPassword(rest[0], pass); err != nil {
			return plugin.Output{}, err
		}
		return textOut("Пароль роли " + rest[0] + " изменён.\n"), nil
	case "create-db":
		args, stdin := extractPasswordFlag(args)
		if len(args) < 1 || len(args) > 2 {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite pg create-db БАЗА [РОЛЬ] [--password-stdin]")
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
		return plugin.Output{Text: fmt.Sprintf("База %s и роль %s готовы; пароль: %s/pg-%s\n", args[0], role, pgSecretDir, role),
			Data: map[string]any{"database": args[0], "role": role, "secret": pgSecretDir + "/pg-" + role}}, nil
	case "drop-db":
		if len(args) != 1 || !pgNameRe.MatchString(args[0]) {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite pg drop-db БАЗА")
		}
		if _, err := t.psql("drop database " + args[0]); err != nil {
			return plugin.Output{}, err
		}
		return textOut("База " + args[0] + " удалена.\n"), nil
	}
	if err := noArgs(args, cmd, "pg"); err != nil {
		return plugin.Output{}, err
	}
	switch cmd {
	case "install":
		if err := InstallPG(ctx, env, nil); err != nil {
			return plugin.Output{}, err
		}
		return textOut("PostgreSQL установлен.\n"), nil
	case "status":
		if !t.ok("psql", "--version") {
			e := plugin.Errorf("precondition_failed", "PostgreSQL не установлен")
			e.Hint = "sudo lite pg install"
			return plugin.Output{}, e
		}
		res, _ := t.run("psql", "--version")
		active := t.ok("systemctl", "is-active", "--quiet", "postgresql")
		v := strings.TrimSpace(string(res.Stdout))
		return plugin.Output{Text: fmt.Sprintf("%s, служба %s\n", v, map[bool]string{true: "работает", false: "не работает"}[active]),
			Data: map[string]any{"version": v, "active": active}}, nil
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
	return plugin.Output{}, plugin.Errorf("usage", "у плагина pg нет команды %q", cmd)
}

func (PG) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if !t.ok("psql", "--version") {
		return []plugin.Finding{finding("pg", "installed", plugin.LevelOK, "PostgreSQL не установлен (не используется)", "")}
	}
	if !t.ok("systemctl", "is-active", "--quiet", "postgresql") {
		return []plugin.Finding{finding("pg", "service", plugin.LevelFail, "служба PostgreSQL не работает", "sudo systemctl enable --now postgresql")}
	}
	out := []plugin.Finding{finding("pg", "service", plugin.LevelOK, "служба PostgreSQL работает", "")}
	out = append(out, t.backupFindings()...)
	if la, err := t.psql("show listen_addresses"); err == nil {
		la = strings.TrimSpace(la)
		if bindLocalOnly(strings.ReplaceAll(la, ",", " ")) || la == "localhost" {
			out = append(out, finding("pg", "listen", plugin.LevelOK, "PostgreSQL слушает только localhost", ""))
		} else {
			out = append(out, finding("pg", "listen", plugin.LevelWarn, "PostgreSQL слушает "+la+": убедитесь, что порт закрыт файрволом для чужих адресов", "lite firewall status"))
		}
	}
	return out
}

// PluginNames имена плагинов, которые ставит lite init --plugins (postgres принимается как прежнее имя pg).
func PluginNames() []string {
	return []string{"caddy", "pg", "podman", "redis", "security", "backblaze", "s3", "sftp"}
}

// InstallPlugin ставит плагин по имени для lite init. Для security включается вся защита сервера.
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
	case "backblaze", "s3", "sftp":
		return InstallRclone(ctx, env, say)
	case "security":
		say.say("Плагин security: включаю защиту сервера...")
		_, err := Security{}.Run(ctx, env, "enable", nil)
		return err
	}
	return plugin.Errorf("usage", "неизвестный плагин %q (есть: %s)", name, strings.Join(PluginNames(), ", "))
}

// setPassword меняет пароль существующей роли и обновляет файл секрета.
func (t tools) setPassword(role, pass string) error {
	have, err := t.psql(fmt.Sprintf("select 1 from pg_roles where rolname='%s'", role))
	if err != nil {
		return err
	}
	if strings.TrimSpace(have) == "" {
		return plugin.Errorf("precondition_failed", "роли %s нет", role)
	}
	if err := t.env.FS.MkdirAll(pgSecretDir, 0o700); err != nil {
		return plugin.Errorf("step_failed", "каталог %s: %v", pgSecretDir, err)
	}
	if err := t.env.FS.WriteFile(pgSecretDir+"/pg-"+role, []byte(pass+"\n"), 0o600); err != nil {
		return plugin.Errorf("step_failed", "файл секрета: %v", err)
	}
	return t.psqlSecret(fmt.Sprintf("alter role %s password '%s'", role, pass))
}
