package builtin

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestCloudflareConnectDoesTheWholeJobOnTheServer(t *testing.T) {
	zone, tid := "fedcba9876543210fedcba9876543210", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(b))
		switch {
		case r.URL.Path == "/accounts":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"0123456789abcdef0123456789abcdef","name":"Acme"}]}`))
		case r.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"` + zone + `","name":"example.com"}]}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/cfd_tunnel"):
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/cfd_tunnel"):
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"` + tid + `","name":"lite-acme"}}`))
		case strings.HasSuffix(r.URL.Path, "/token"):
			_, _ = w.Write([]byte(`{"success":true,"result":"` + cfTestToken + `"}`))
		case r.Method == "GET":
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	defer srv.Close()
	old := cfAPIBase
	cfAPIBase = srv.URL
	defer func() { cfAPIBase = old }()
	mem := host.NewMemFS()
	var calls []string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Name+" "+strings.Join(c.Args, " "))
		return host.Result{}, nil
	}}
	const apiToken = "the-cloudflare-api-token-value"
	env := plugin.Env{Runner: r, FS: mem, Stdin: strings.NewReader(apiToken + "\n")}
	out, err := Cloudflare{}.Run(context.Background(), env, "connect", []string{"--domain", "app.example.com", "--port", "18080", "--name", "acme", "--token-stdin", "--project", "shop", "--site", "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "routed to port 18080") || !strings.Contains(out.Text, "connected on this server") {
		t.Errorf("%s", out.Text)
	}
	if got := string(mem.Files[cfDir+"/tunnel-acme.token"]); !strings.HasPrefix(got, cfTestToken) {
		t.Errorf("the token of the tunnel must be kept for cloudflared: %q", got)
	}
	// the API token is used once and never kept: not in a file, not on a command line, not in a unit
	for name, body := range mem.Files {
		if strings.Contains(string(body), apiToken) {
			t.Errorf("the API token was written to %s", name)
		}
	}
	for _, c := range calls {
		if strings.Contains(c, apiToken) || strings.Contains(c, cfTestToken) {
			t.Errorf("a token on a command line: %s", c)
		}
	}
	if !strings.Contains(strings.Join(seen, "\n"), `"hostname":"app.example.com","service":"http://127.0.0.1:18080"`) {
		t.Errorf("the route was not made:\n%s", strings.Join(seen, "\n"))
	}
	if _, err := (Cloudflare{}).Run(context.Background(), plugin.Env{Runner: r, FS: mem, Stdin: strings.NewReader(apiToken)}, "connect", []string{"--domain", "app.example.com", "--port", "80", "--token-stdin"}); err == nil {
		t.Error("the port must be the port of a site")
	}
}
