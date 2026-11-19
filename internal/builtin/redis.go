package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// Redis плагин `redis`: Redis на хосте, доступный только с этого сервера.
type Redis struct{}

func (Redis) Name() string       { return "redis" }
func (Redis) Kind() string       { return "builtin" }
func (Redis) Requires() []string { return nil }
func (Redis) Description() string {
	return "Redis на хосте: слушает только 127.0.0.1"
}
func (Redis) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "поставить Redis", Confirm: true},
		{Name: "status", Help: "версия, состояние службы, адрес прослушивания"},
	}
}

func redisPackages(manager string) []string {
	if manager == "apt" {
		return []string{"redis-server"}
	}
	return []string{"redis"}
}

// redisService имя службы systemd: в Debian redis-server, в остальных redis.
func (t tools) redisService() string {
	if t.manager() == "apt" {
		return "redis-server"
	}
	return "redis"
}

// InstallRedis ставит Redis и включает службу.
func InstallRedis(ctx context.Context, env plugin.Env, say Say) error {
	t := tools{ctx, env}
	say.say("Плагин redis: ставлю пакеты...")
	if err := t.install(redisPackages(t.manager())...); err != nil {
		return err
	}
	return t.must("systemctl", "enable", "--now", t.redisService())
}

// redisBind возвращает значение bind из работающего Redis.
func (t tools) redisBind() (string, bool) {
	res, err := t.run("redis-cli", "config", "get", "bind")
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	if len(lines) < 2 {
		return "", false
	}
	return strings.TrimSpace(lines[1]), true
}

// bindLocalOnly истинно, если все адреса bind (без ведущего «-») лежат на 127.0.0.1 или ::1.
func bindLocalOnly(bind string) bool {
	if strings.TrimSpace(bind) == "" {
		return false
	}
	for _, a := range strings.Fields(bind) {
		a = strings.TrimPrefix(a, "-")
		if a != "127.0.0.1" && a != "::1" {
			return false
		}
	}
	return true
}

func (Redis) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	t := tools{ctx, env}
	if err := noArgs(args, cmd, "redis"); err != nil {
		return plugin.Output{}, err
	}
	switch cmd {
	case "install":
		if err := InstallRedis(ctx, env, nil); err != nil {
			return plugin.Output{}, err
		}
		return textOut("Redis установлен.\n"), nil
	case "status":
		res, err := t.run("redis-server", "--version")
		if err != nil || res.ExitCode != 0 {
			e := plugin.Errorf("precondition_failed", "Redis не установлен")
			e.Hint = "sudo lite redis install"
			return plugin.Output{}, e
		}
		active := t.ok("systemctl", "is-active", "--quiet", t.redisService())
		bind, _ := t.redisBind()
		v := strings.TrimSpace(string(res.Stdout))
		state := map[bool]string{true: "работает", false: "не работает"}[active]
		return plugin.Output{Text: fmt.Sprintf("%s\nслужба %s, bind: %s\n", v, state, bind),
			Data: map[string]any{"version": v, "active": active, "bind": bind}}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "у плагина redis нет команды %q", cmd)
}

func (Redis) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if !t.ok("redis-server", "--version") {
		return []plugin.Finding{finding("redis", "installed", plugin.LevelOK, "Redis не установлен (не используется)", "")}
	}
	if !t.ok("systemctl", "is-active", "--quiet", t.redisService()) {
		return []plugin.Finding{finding("redis", "service", plugin.LevelFail, "служба Redis не работает", "sudo systemctl enable --now "+t.redisService())}
	}
	out := []plugin.Finding{finding("redis", "service", plugin.LevelOK, "служба Redis работает", "")}
	bind, ok := t.redisBind()
	switch {
	case !ok:
		out = append(out, finding("redis", "bind", plugin.LevelWarn, "не удалось узнать адреса прослушивания Redis", ""))
	case !bindLocalOnly(bind):
		out = append(out, finding("redis", "bind", plugin.LevelFail, "Redis слушает не только localhost (bind: "+bind+")", "в redis.conf задайте bind 127.0.0.1 ::1"))
	default:
		out = append(out, finding("redis", "bind", plugin.LevelOK, "Redis слушает только localhost", ""))
	}
	return out
}
