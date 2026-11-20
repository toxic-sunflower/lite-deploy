package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

const cfTestToken = "eyJhIjoiMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWYiLCJ0IjoiMTIzNDU2NzgtMTIzNC0xMjM0LTEyMzQtMTIzNDU2Nzg5MGFiIiwicyI6IlpYaGhiWEJzWlE9PSJ9"

func TestCloudflareSetupKeepsTheTokenOffTheCommandLine(t *testing.T) {
	mem := host.NewMemFS()
	var calls []string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Name+" "+strings.Join(c.Args, " "))
		return host.Result{}, nil
	}}
	env := plugin.Env{Runner: r, FS: mem, Stdin: strings.NewReader(cfTestToken + "\n")}
	out, err := Cloudflare{}.Run(context.Background(), env, "setup", []string{"--token-stdin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if strings.Contains(c, cfTestToken) {
			t.Fatalf("the token must never be on a command line: %s", c)
		}
	}
	if got := string(mem.Files[cfToken]); !strings.HasPrefix(got, cfTestToken) {
		t.Errorf("the token file is missing: %q", got)
	}
	if info, err := mem.Stat(cfToken); err != nil || info.Mode.Perm() != 0o600 {
		t.Errorf("the token file must be readable by its owner only: %v %v", info.Mode, err)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"chown cloudflared:cloudflared " + cfToken, "systemctl enable --now " + cfService} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing step %q in:\n%s", want, joined)
		}
	}
	if !strings.Contains(out.Text, "Next:") {
		t.Errorf("the person must be told what to do next: %s", out.Text)
	}
	unit := string(mem.Files[cfUnit])
	if !strings.Contains(unit, "User=cloudflared") || !strings.Contains(unit, "--token-file "+cfToken) || strings.Contains(unit, cfTestToken) {
		t.Errorf("the unit must run as its own user and read the token from a file:\n%s", unit)
	}
}

func TestCloudflareRejectsAWrongToken(t *testing.T) {
	r := &host.FakeRunner{Handler: func(host.Cmd) (host.Result, error) { return host.Result{}, nil }}
	for _, bad := range []string{"", "short", "has spaces in it which a token never has, so this is rejected for sure"} {
		env := plugin.Env{Runner: r, FS: host.NewMemFS(), Stdin: strings.NewReader(bad)}
		if _, err := (Cloudflare{}).Run(context.Background(), env, "setup", []string{"--token-stdin"}); err == nil {
			t.Errorf("token %q must be rejected", bad)
		}
	}
}

func TestCloudflareSeveralTunnels(t *testing.T) {
	mem := host.NewMemFS()
	r := &host.FakeRunner{Handler: func(host.Cmd) (host.Result, error) { return host.Result{}, nil }}
	run := func(cmd string, args ...string) plugin.Output {
		env := plugin.Env{Runner: r, FS: mem, Stdin: strings.NewReader(cfTestToken + "\n")}
		out, err := Cloudflare{}.Run(context.Background(), env, cmd, args)
		if err != nil {
			t.Fatalf("%s %v: %v", cmd, args, err)
		}
		return out
	}
	run("setup", "--token-stdin")
	run("setup", "--token-stdin", "--name", "acme")
	if _, ok := mem.Files[cfDir+"/tunnel-acme.token"]; !ok {
		t.Fatal("the second tunnel needs its own token file")
	}
	unit := string(mem.Files["/etc/systemd/system/lite-cloudflared-acme.service"])
	if !strings.Contains(unit, "--token-file "+cfDir+"/tunnel-acme.token") {
		t.Errorf("the second unit must read its own token:\n%s", unit)
	}
	if got := run("status").Data.(map[string]any)["tunnels"].([]map[string]any); len(got) != 2 || got[0]["name"] != "default" || got[1]["name"] != "acme" {
		t.Errorf("both tunnels must be listed: %v", got)
	}
	run("remove", "--name", "acme")
	if _, ok := mem.Files[cfDir+"/tunnel-acme.token"]; ok {
		t.Error("the token of a removed tunnel must be deleted")
	}
	if _, ok := mem.Files[cfToken]; !ok {
		t.Error("removing one tunnel must not touch the other")
	}
	if _, err := (Cloudflare{}).Run(context.Background(), plugin.Env{Runner: r, FS: mem}, "remove", []string{"--name", "../x"}); err == nil {
		t.Error("a bad tunnel name must be refused")
	}
}
