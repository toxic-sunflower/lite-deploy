package cli

import (
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

func fwEnv(t *testing.T) (Env, *[]string) {
	t.Helper()
	env, _, _ := testEnv(t)
	var calls []string
	env.Runner = &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		calls = append(calls, line)
		if line == "ufw status" {
			return host.Result{Stdout: []byte("Status: active\n\nTo   Action   From\n--   ------   ----\n22/tcp     LIMIT     Anywhere\n9000/tcp    ALLOW     Anywhere\n")}, nil
		}
		if c.Name == "sshd" {
			return host.Result{Stdout: []byte("port 22\n")}, nil
		}
		return host.Result{}, nil
	}}
	return env, &calls
}

func TestFirewallStatusJSON(t *testing.T) {
	env, calls := fwEnv(t)
	out := env.Stdout.(interface{ String() string })
	if c := run(t, env, "firewall", "status", "--json"); c != 0 {
		t.Fatalf("код %d: %s", c, out.String())
	}
	if !strings.Contains(out.String(), `"command":"firewall.status"`) || !strings.Contains(out.String(), `"port":9000`) {
		t.Fatalf("%s", out.String())
	}
	_ = calls
}

func TestFirewallNeedsRootAndConfirmation(t *testing.T) {
	env, calls := fwEnv(t)
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "firewall", "status"); c != 4 {
		t.Fatalf("без root: код %d", c)
	}
	env, calls = fwEnv(t)
	if c := run(t, env, "firewall", "allow", "8080/tcp"); c != 4 {
		t.Fatalf("без --yes: код %d", c)
	}
	for _, l := range *calls {
		if strings.Contains(l, "allow") {
			t.Fatalf("без подтверждения ufw менять нельзя: %s", l)
		}
	}
	env, calls = fwEnv(t)
	if c := run(t, env, "firewall", "allow", "8080/tcp", "--from", "10.0.0.0/8", "--yes"); c != 0 {
		t.Fatalf("код %d", c)
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "ufw allow proto tcp from 10.0.0.0/8 to any port 8080") {
		t.Fatalf("%v", *calls)
	}
}

func TestFirewallErrors(t *testing.T) {
	env, _ := fwEnv(t)
	if c := run(t, env, "firewall", "remove", "22/tcp", "--action", "limit", "--yes"); c != 4 {
		t.Fatalf("защищённый порт: код %d", c)
	}
	env, _ = fwEnv(t)
	if c := run(t, env, "firewall", "remove", "7777/tcp", "--yes"); c != 7 {
		t.Fatalf("нет правила: код %d", c)
	}
	env, _ = fwEnv(t)
	if c := run(t, env, "firewall", "allow", "abc", "--yes"); c != 2 {
		t.Fatalf("неверный порт: код %d", c)
	}
	env, _ = fwEnv(t)
	if c := run(t, env, "firewall", "bogus"); c != 2 {
		t.Fatalf("неизвестная подкоманда: код %d", c)
	}
}
