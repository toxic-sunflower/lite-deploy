package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// Redis is the `redis` plugin: Redis on the host, reachable from this server only.
type Redis struct{}

func (Redis) Name() string       { return "redis" }
func (Redis) Kind() string       { return "builtin" }
func (Redis) Requires() []string { return nil }
func (Redis) Description() string {
	return "Redis on the host: listens on 127.0.0.1 only"
}
func (Redis) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "install Redis", Confirm: true},
		{Name: "status", Help: "version, service state, listen address"},
	}
}

func redisPackages(manager string) []string {
	if manager == "apt" {
		return []string{"redis-server"}
	}
	return []string{"redis"}
}

// redisService is the systemd service name: redis-server on Debian, redis elsewhere.
func (t tools) redisService() string {
	if t.manager() == "apt" {
		return "redis-server"
	}
	return "redis"
}

// InstallRedis installs Redis and enables the service.
func InstallRedis(ctx context.Context, env plugin.Env, say Say) error {
	t := tools{ctx, env}
	say.say("Plugin redis: installing packages...")
	if err := t.install(redisPackages(t.manager())...); err != nil {
		return err
	}
	return t.must("systemctl", "enable", "--now", t.redisService())
}

// redisBind returns the bind value from a running Redis.
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

// bindLocalOnly is true if all bind addresses (without a leading "-") are 127.0.0.1 or ::1.
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
		return textOut("Redis is installed.\n"), nil
	case "status":
		res, err := t.run("redis-server", "--version")
		if err != nil || res.ExitCode != 0 {
			e := plugin.Errorf("precondition_failed", "Redis is not installed")
			e.Hint = "lite redis install"
			return plugin.Output{}, e
		}
		active := t.ok("systemctl", "is-active", "--quiet", t.redisService())
		bind, _ := t.redisBind()
		v := strings.TrimSpace(string(res.Stdout))
		state := map[bool]string{true: "running", false: "not working"}[active]
		return plugin.Output{Text: fmt.Sprintf("%s\nservice %s, bind: %s\n", v, state, bind),
			Data: map[string]any{"version": v, "active": active, "bind": bind}}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin redis has no command %q", cmd)
}

func (Redis) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if !t.ok("redis-server", "--version") {
		return []plugin.Finding{finding("redis", "installed", plugin.LevelOK, "Redis is not installed (not in use)", "")}
	}
	if !t.ok("systemctl", "is-active", "--quiet", t.redisService()) {
		return []plugin.Finding{finding("redis", "service", plugin.LevelFail, "the Redis service is not running", "sudo systemctl enable --now "+t.redisService())}
	}
	out := []plugin.Finding{finding("redis", "service", plugin.LevelOK, "the Redis service is running", "")}
	bind, ok := t.redisBind()
	switch {
	case !ok:
		out = append(out, finding("redis", "bind", plugin.LevelWarn, "could not find out the Redis listen addresses", ""))
	case !bindLocalOnly(bind):
		out = append(out, finding("redis", "bind", plugin.LevelFail, "Redis listens on more than localhost (bind: "+bind+")", "set bind 127.0.0.1 ::1 in redis.conf"))
	default:
		out = append(out, finding("redis", "bind", plugin.LevelOK, "Redis listens on localhost only", ""))
	}
	return out
}
