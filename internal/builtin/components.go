package builtin

import (
	"context"
	"fmt"
	"strings"

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
	}
}

func podmanPackages(manager string) []string {
	if manager == "apt" {
		return []string{"podman", "uidmap"}
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
	return []plugin.Finding{finding("podman", "version", plugin.LevelOK, v, "")}
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
	}
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
	return []plugin.Finding{finding("pg", "service", plugin.LevelOK, "служба PostgreSQL работает", "")}
}

// PluginNames имена плагинов, которые ставит lite init --plugins (postgres принимается как прежнее имя pg).
func PluginNames() []string { return []string{"caddy", "pg", "podman", "security"} }

// InstallPlugin ставит плагин по имени для lite init. Для security включается вся защита сервера.
func InstallPlugin(ctx context.Context, env plugin.Env, name string, say Say) error {
	switch name {
	case "caddy":
		return InstallCaddy(ctx, env, say)
	case "podman":
		return InstallPodman(ctx, env, say)
	case "pg", "postgres":
		return InstallPG(ctx, env, say)
	case "security":
		say.say("Плагин security: включаю защиту сервера...")
		_, err := Security{}.Run(ctx, env, "enable", nil)
		return err
	}
	return plugin.Errorf("usage", "неизвестный плагин %q (есть: %s)", name, strings.Join(PluginNames(), ", "))
}
