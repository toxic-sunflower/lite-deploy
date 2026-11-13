package cli

import (
	"strings"
	"testing"
)

func TestAgentCommands(t *testing.T) {
	env, _, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "agent", "install", "--admin", "anna"); c != 4 {
		t.Fatalf("install без root: код %d", c)
	}
	if c := run(t, env, "exec"); c != 4 {
		t.Fatalf("exec без root: код %d", c)
	}
	env, _, _ = testEnv(t)
	if c := run(t, env, "agent"); c != 2 {
		t.Fatalf("agent без подкоманды: %d", c)
	}
	if c := run(t, env, "agent", "serve"); c != 2 {
		t.Fatalf("serve без systemd и --socket: %d", c)
	}
	if c := run(t, env, "call", "DELETE", "/v1/status"); c != 2 {
		t.Fatalf("call: метод: %d", c)
	}
	if c := run(t, env, "call", "GET", "/etc/passwd"); c != 2 {
		t.Fatalf("call: путь: %d", c)
	}
}

func TestCallWithoutAgent(t *testing.T) {
	env, out, errb := testEnv(t)
	env.Getenv = func(k string) string {
		if k == "LITE_DEPLOY_SOCK" {
			return t.TempDir() + "/нет.sock"
		}
		return ""
	}
	if c := run(t, env, "call", "GET", "/v1/status", "--json"); c != 3 {
		t.Fatalf("код %d", c)
	}
	e := decode(t, out)
	if e.Error == nil || e.Error.Code != "precondition_failed" || !strings.Contains(e.Error.Hint, "agent install") {
		t.Fatalf("%+v %s", e, errb.String())
	}
}
