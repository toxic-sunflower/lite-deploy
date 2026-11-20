package deploy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const (
	sha1 = "1111111111111111111111111111111111111111"
	sha2 = "2222222222222222222222222222222222222222"
)

const projectJSON = `{"schema":"lite-deploy/project/v1","name":"shop",
 "services":{"api":{"image":"ghcr.io/o/shop-api","container_port":8080,"health":{"path":"/health/ready","version_field":"version","timeout_seconds":10}}},
 "sites":[{"name":"main","host":"shop.example.com","service":"api","trusted_proxy":"cloudflare"}]}`

const stateJSON = `{"ports":{"services":{"api":[18001,18002]},"sites":{"main":18000}},"deployed":false}`

// sim imitates podman, systemd and the HTTP check.
type sim struct {
	t       *testing.T
	fs      *host.MemFS
	runner  *host.FakeRunner
	http    *host.FakeHTTP
	lock    *host.FakeLocker
	clock   time.Time
	pulled  string
	served  map[int]string // port -> the version the container serves
	fail    map[string]bool
	reloads int
}

func newSim(t *testing.T) (*sim, Env) {
	s := &sim{t: t, fs: host.NewMemFS(), lock: &host.FakeLocker{}, clock: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC), served: map[int]string{}, fail: map[string]bool{}}
	s.fs.Files["/etc/lite-deploy/projects/shop.json"] = []byte(projectJSON)
	s.fs.Files["/var/lib/lite-deploy/state/projects/shop.json"] = []byte(stateJSON)
	s.fs.Files["/etc/caddy/upstreams/shop-main.caddy"] = []byte("respond \"not deployed\" 503\n")
	s.fs.Perms["/etc/caddy/upstreams/shop-main.caddy"] = 0o644
	s.runner = &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		if c.Name == "systemd-run" { // the user session wrapper: parse the inner command
			for i, a := range c.Args {
				if a == "--" {
					c = host.Cmd{Name: c.Args[i+1], Args: c.Args[i+2:]}
					break
				}
			}
		}
		line := c.Name + " " + strings.Join(c.Args, " ")
		for k := range s.fail {
			if strings.Contains(line, k) {
				return host.Result{ExitCode: 1, Stderr: []byte("boom")}, nil
			}
		}
		switch {
		case c.Name == "podman" && c.Args[0] == "pull":
			s.pulled = c.Args[len(c.Args)-1][strings.LastIndex(c.Args[len(c.Args)-1], ":")+1:]
		case c.Name == "systemctl" && c.Args[1] == "restart":
			port := 18001
			if strings.Contains(c.Args[2], "-green") {
				port = 18002
			}
			s.served[port] = s.pulled
		case c.Name == "systemctl" && len(c.Args) > 1 && c.Args[1] == "is-active":
		case c.Name == "sudo":
			s.reloads++
		}
		return host.Result{}, nil
	}}
	s.http = &host.FakeHTTP{Handler: func(url, _ string) (host.HTTPResponse, error) {
		var port int
		_, _ = fmt.Sscanf(url, "http://127.0.0.1:%d/", &port)
		v, ok := s.served[port]
		if !ok {
			return host.HTTPResponse{}, fmt.Errorf("connection refused")
		}
		return host.HTTPResponse{Status: 200, Body: []byte(`{"version":"` + v + `"}`)}, nil
	}}
	env := Env{Runner: s.runner, FS: s.fs, HTTP: s.http, Lock: s.lock, Now: func() time.Time { return s.clock },
		Sleep: func(d time.Duration) { s.clock = s.clock.Add(d) }}
	return s, env
}

func open(t *testing.T, env Env) *Manager {
	t.Helper()
	m, e := Open(env, "shop")
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func (s *sim) upstream() string { return string(s.fs.Files["/etc/caddy/upstreams/shop-main.caddy"]) }

func TestDeployFirstThenSecondThenRollback(t *testing.T) {
	s, env := newSim(t)
	m := open(t, env)
	ctx := context.Background()

	r, e := m.Deploy(ctx, sha1)
	if e != nil {
		t.Fatal(e)
	}
	if r.Slot != "blue" || r.PreviousSlot != "none" || !r.Switched {
		t.Fatalf("first deploy: %+v", r)
	}
	if !strings.Contains(s.upstream(), "127.0.0.1:18001") || !strings.Contains(s.upstream(), "CF-Connecting-IP") {
		t.Fatalf("upstream: %q", s.upstream())
	}
	if m.Active() != "blue" || s.reloads != 1 {
		t.Fatalf("active=%s reloads=%d", m.Active(), s.reloads)
	}

	r, e = m.Deploy(ctx, sha2)
	if e != nil {
		t.Fatal(e)
	}
	if r.Slot != "green" || r.PreviousSlot != "blue" || !strings.Contains(s.upstream(), "127.0.0.1:18002") {
		t.Fatalf("second deploy: %+v %q", r, s.upstream())
	}

	rb, e := m.Rollback(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if rb.Slot != "blue" || rb.Version != sha1 || !strings.Contains(s.upstream(), "127.0.0.1:18001") || m.Active() != "blue" {
		t.Fatalf("rollback: %+v %q", rb, s.upstream())
	}

	st, _ := m.Status(ctx)
	if st.Active != "blue" || st.Slots["green"]["api"].Version == nil || *st.Slots["green"]["api"].Version != sha2 || len(st.History) != 3 {
		t.Fatalf("status: %+v", st)
	}
	if st.History[2].Event != "rollback" {
		t.Fatalf("history: %+v", st.History)
	}
}

func TestDeployUnhealthyKeepsTraffic(t *testing.T) {
	s, env := newSim(t)
	m := open(t, env)
	ctx := context.Background()
	if _, e := m.Deploy(ctx, sha1); e != nil {
		t.Fatal(e)
	}
	before := s.upstream()
	// the container starts but serves another version: it does not become ready
	s.http.Handler = func(url, _ string) (host.HTTPResponse, error) {
		return host.HTTPResponse{Status: 200, Body: []byte(`{"version":"old"}`)}, nil
	}
	_, e := m.Deploy(ctx, sha2)
	if e == nil || e.Code != CodeUnhealthy {
		t.Fatalf("expected deploy_unhealthy: %v", e)
	}
	if s.upstream() != before || m.Active() != "blue" {
		t.Fatal("traffic must stay on the previous slot")
	}
	var stopped, logs bool
	for _, c := range s.runner.Calls {
		line := c.Name + " " + strings.Join(c.Args, " ")
		stopped = stopped || strings.Contains(line, "stop shop-api-green")
		logs = logs || strings.Contains(line, "logs --tail 40 shop-api-green")
	}
	if !stopped || !logs {
		t.Fatalf("the slot must be stopped and the log shown: stop=%v logs=%v", stopped, logs)
	}
	if h := m.lastHistory(5); h[len(h)-1].Event != "deploy-failed" {
		t.Fatalf("history: %+v", h)
	}
}

func TestDeployRejectsBadSHAAndLock(t *testing.T) {
	s, env := newSim(t)
	m := open(t, env)
	if _, e := m.Deploy(context.Background(), "abc"); e == nil || e.Code != CodeInvalidSHA {
		t.Fatalf("sha: %v", e)
	}
	s.lock.Held = map[string]bool{"/home/shop/.local/state/lite-deploy/lock": true}
	if _, e := m.Deploy(context.Background(), sha1); e == nil || e.Code != CodeInProgress {
		t.Fatalf("lock: %v", e)
	}
	if _, e := m.Rollback(context.Background()); e == nil || e.Code != CodeInProgress {
		t.Fatalf("rollback lock: %v", e)
	}
}

func TestPullFailureAndCaddyReloadFailureRestore(t *testing.T) {
	s, env := newSim(t)
	m := open(t, env)
	ctx := context.Background()
	s.fail["pull"] = true
	if _, e := m.Deploy(ctx, sha1); e == nil || e.Code != CodeFailed {
		t.Fatalf("pull: %v", e)
	}
	delete(s.fail, "pull")
	before := s.upstream()
	s.fail["sudo"] = true
	if _, e := m.Deploy(ctx, sha1); e == nil || e.Code != CodeCaddyReloadFailed {
		t.Fatalf("reload: %v", e)
	}
	if s.upstream() != before || m.Active() != "none" {
		t.Fatalf("the files must come back: %q active=%s", s.upstream(), m.Active())
	}
}

func TestRollbackErrors(t *testing.T) {
	s, env := newSim(t)
	m := open(t, env)
	ctx := context.Background()
	if _, e := m.Rollback(ctx); e == nil || e.Code != CodeNothingDeployed {
		t.Fatalf("nothing: %v", e)
	}
	if _, e := m.Deploy(ctx, sha1); e != nil {
		t.Fatal(e)
	}
	// the previous slot (green) was never started
	if _, e := m.Rollback(ctx); e == nil || e.Code != CodeRollbackNA {
		t.Fatalf("rollback_unavailable: %v", e)
	}
	_ = s
}

func TestOpenErrors(t *testing.T) {
	_, env := newSim(t)
	if _, e := Open(env, "nope"); e == nil || e.Code != CodeProjectNotFound {
		t.Fatalf("not found: %v", e)
	}
	if _, e := Open(env, "../x"); e == nil || e.Code != CodeProjectNotFound {
		t.Fatalf("bad name: %v", e)
	}
}

func TestAsUserUsesSystemdRunAndChown(t *testing.T) {
	s, env := newSim(t)
	env.AsUser = true
	m := open(t, env)
	if _, e := m.Deploy(context.Background(), sha1); e != nil {
		t.Fatal(e)
	}
	var wrapped bool
	for _, c := range s.runner.Calls {
		wrapped = wrapped || c.Name == "systemd-run"
		if c.Name == "podman" || (c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == "--user") {
			t.Fatalf("from root the commands must go through systemd-run: %s %v", c.Name, c.Args)
		}
	}
	if !wrapped {
		t.Fatal("no systemd-run calls")
	}
	if own := s.fs.Owners["/home/shop/.local/state/lite-deploy/active"]; own != [2]string{"shop", "shop"} {
		t.Fatalf("owner of active: %v", own)
	}
	var reload bool
	for _, c := range s.runner.Calls {
		reload = reload || (c.Name == "systemctl" && strings.Join(c.Args, " ") == "reload caddy")
	}
	if !reload {
		t.Fatal("reload from root without sudo")
	}
}

func TestStatusJSONShape(t *testing.T) {
	_, env := newSim(t)
	m := open(t, env)
	st, _ := m.Status(context.Background())
	raw, _ := json.Marshal(st)
	if !strings.Contains(string(raw), `"active":"none"`) || !strings.Contains(string(raw), `"history":[]`) {
		t.Fatalf("%s", raw)
	}
}

func TestProjectUserSwitchesTrafficThroughTheHelper(t *testing.T) {
	s, env := newSim(t)
	var slots []string
	env.Switch = func(_ context.Context, slot string) error { slots = append(slots, slot); return nil }
	m := open(t, env)
	before := s.upstream()
	if _, e := m.Deploy(context.Background(), sha1); e != nil {
		t.Fatal(e)
	}
	if len(slots) != 1 || slots[0] != "blue" {
		t.Fatalf("the helper must be asked to switch to the idle slot: %v", slots)
	}
	if s.upstream() != before {
		t.Error("a project user must not write the Caddy upstream file itself")
	}
	for _, c := range s.runner.Calls {
		if c.Name == "sudo" {
			t.Fatalf("no sudo any more: %v", c.Args)
		}
	}
}

func TestWithoutTheHelperTheOldWayStillWorks(t *testing.T) {
	s, env := newSim(t)
	env.Switch = func(context.Context, string) error { return ErrNoHelper }
	m := open(t, env)
	if _, e := m.Deploy(context.Background(), sha1); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(s.upstream(), "18001") && !strings.Contains(s.upstream(), "18002") {
		t.Errorf("without the helper the upstream is written as before: %s", s.upstream())
	}
}

func TestHelperFailureKeepsTraffic(t *testing.T) {
	s, env := newSim(t)
	env.Switch = func(context.Context, string) error { return fmt.Errorf("caddy would not reload") }
	m := open(t, env)
	_, e := m.Deploy(context.Background(), sha1)
	if e == nil || e.Code != CodeCaddyReloadFailed {
		t.Fatalf("a helper failure must fail the deploy: %v", e)
	}
	_ = s
}

func TestHelperSwitcherTalksToASocketAndNoticesAMissingOne(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/switch.sock"
	if err := HelperSwitcher(sock)(context.Background(), "blue"); err != ErrNoHelper {
		t.Fatalf("a missing socket means no helper: %v", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no unix sockets here")
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadBytes('\n')
			var req SwitchRequest
			_ = json.Unmarshal(line, &req)
			rep := SwitchReply{OK: req.Slot == "blue", Error: "only blue here"}
			b, _ := json.Marshal(rep)
			_, _ = c.Write(append(b, '\n'))
			_ = c.Close()
		}
	}()
	if err := HelperSwitcher(sock)(context.Background(), "blue"); err != nil {
		t.Fatalf("blue must work: %v", err)
	}
	if err := HelperSwitcher(sock)(context.Background(), "green"); err == nil || !strings.Contains(err.Error(), "only blue here") {
		t.Fatalf("an error of the helper must reach the caller: %v", err)
	}
}
