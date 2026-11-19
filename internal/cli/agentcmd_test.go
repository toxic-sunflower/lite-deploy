package cli

import (
	"strings"
	"testing"
)

func TestAgentCommands(t *testing.T) {
	env, _, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "agent", "install"); c != 4 {
		t.Fatalf("install without root: code %d", c)
	}
	if c := run(t, env, "exec"); c != 4 {
		t.Fatalf("exec without root: code %d", c)
	}
	env, _, _ = testEnv(t)
	if c := run(t, env, "agent"); c != 2 {
		t.Fatalf("agent without a subcommand: %d", c)
	}
	if c := run(t, env, "agent", "serve"); c != 2 {
		t.Fatalf("serve without systemd and --socket: %d", c)
	}
	if c := run(t, env, "call", "DELETE", "/v1/status"); c != 2 {
		t.Fatalf("call: method: %d", c)
	}
	if c := run(t, env, "call", "GET", "/etc/passwd"); c != 2 {
		t.Fatalf("call: path: %d", c)
	}
}

func TestCallWithoutAgent(t *testing.T) {
	env, out, errb := testEnv(t)
	env.Getenv = func(k string) string {
		if k == "LITE_DEPLOY_SOCK" {
			return t.TempDir() + "/missing.sock"
		}
		return ""
	}
	if c := run(t, env, "call", "GET", "/v1/status", "--json"); c != 3 {
		t.Fatalf("code %d", c)
	}
	e := decode(t, out)
	if e.Error == nil || e.Error.Code != "precondition_failed" || !strings.Contains(e.Error.Hint, "liteup") {
		t.Fatalf("%+v %s", e, errb.String())
	}
}

func TestDirectRootIsRefused(t *testing.T) {
	env, out, _ := testEnv(t)
	env.LoginUID = func() (int, bool) { return 0, true } // root login
	for _, cmd := range [][]string{{"status", "--json"}, {"deploy", "shop", "x", "--json"}, {"firewall", "status", "--json"}, {"project", "list", "--json"}} {
		out.Reset()
		if c := run(t, env, cmd...); c != 4 {
			t.Fatalf("%v: code %d", cmd, c)
		}
		if e := decode(t, out); e.Error == nil || e.Error.Code != "permission_denied" || !strings.Contains(e.Error.Hint, "lite init --user") {
			t.Fatalf("%v: %+v", cmd, e)
		}
	}
	out.Reset()
	if c := run(t, env, "version", "--json"); c != 0 {
		t.Fatalf("version must work: %d", c)
	}
	// through sudo from a regular user (loginuid 1000) and from a service (not set) there is no ban
	for _, l := range []func() (int, bool){func() (int, bool) { return 1000, true }, func() (int, bool) { return 0, false }} {
		env.LoginUID = l
		out.Reset()
		run(t, env, "status", "--json")
		if e := decode(t, out); e.Error != nil && e.Error.Code == "permission_denied" {
			t.Fatalf("the ban must not trigger: %+v", e)
		}
	}
}
