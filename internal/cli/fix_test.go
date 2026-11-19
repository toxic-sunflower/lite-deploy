package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

func TestFixCommandsAllowlist(t *testing.T) {
	auto, manual := fixCommands("sudo lite security ssh; sudo lite firewall mode max; sudo lite backup setup age1…; sudo loginctl enable-linger shop; sudo lite pg schedule on")
	var got []string
	for _, a := range auto {
		got = append(got, strings.Join(a, " "))
	}
	if strings.Join(got, "|") != "security ssh|pg schedule on" {
		t.Fatalf("auto = %v", got)
	}
	if len(manual) != 3 {
		t.Fatalf("manual = %v", manual)
	}
	for _, evil := range []string{"sudo lite security ssh --force", "sudo lite pg install; rm -rf /", "lite security ssh", "sudo lite security allow-users ИМЯ…"} {
		a, _ := fixCommands(evil)
		for _, x := range a {
			if strings.Contains(strings.Join(x, " "), "--force") || strings.Contains(strings.Join(x, " "), "rm") {
				t.Errorf("%q: попало в автоисправление: %v", evil, a)
			}
		}
	}
}

func TestFixDryRunDoesNotChangeAnything(t *testing.T) {
	env, out, _ := testEnv(t)
	var mutating []string
	inner := env.Runner
	env.Runner = fnRunner(func(c host.Cmd) (host.Result, error) {
		if c.Name == "apt-get" && len(c.Args) > 0 && c.Args[0] == "install" {
			mutating = append(mutating, c.Name)
		}
		return inner.Run(context.Background(), c)
	})
	if c := run(t, env, "fix", "--dry-run", "--json"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	e := decode(t, out)
	d := e.Data.(map[string]any)
	if d["dry_run"] != true || len(d["applied"].([]any)) != 0 || len(mutating) != 0 {
		t.Fatalf("%+v %v", d, mutating)
	}
}

func TestFixRequiresRootAndConfirmation(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	var sent string
	env.AgentCall = func(_ context.Context, _, method, path string, _ []byte) ([]byte, error) {
		sent = method + " " + path
		return []byte(`{"ok":true,"api_version":"v1","command":"fix","data":{"applied":[],"failed":[],"manual":[]}}`), nil
	}
	run(t, env, "fix", "--json")
	if e := decode(t, out); e.OK || e.Error.Code != "confirmation_required" || sent != "" {
		t.Fatalf("без --yes и терминала: %+v %q", e, sent)
	}
	out.Reset()
	if c := run(t, env, "fix", "--yes", "--json"); c != 0 || sent != "POST /v1/fix" {
		t.Fatalf("с --yes команда уходит агенту: %d %q", c, sent)
	}
}

type fnRunner func(host.Cmd) (host.Result, error)

func (f fnRunner) Run(_ context.Context, c host.Cmd) (host.Result, error) { return f(c) }
