package deploy

import (
	"context"
	"encoding/json"
	"fmt"
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

// sim имитирует podman, systemd и HTTP-проверку.
type sim struct {
	t       *testing.T
	fs      *host.MemFS
	runner  *host.FakeRunner
	http    *host.FakeHTTP
	lock    *host.FakeLocker
	clock   time.Time
	pulled  string
	served  map[int]string // порт -> версия, которую отдаёт контейнер
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
		if c.Name == "systemd-run" { // обёртка сессии пользователя: разбираем внутреннюю команду
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
		t.Fatalf("первая выкатка: %+v", r)
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
		t.Fatalf("вторая выкатка: %+v %q", r, s.upstream())
	}

	rb, e := m.Rollback(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if rb.Slot != "blue" || rb.Version != sha1 || !strings.Contains(s.upstream(), "127.0.0.1:18001") || m.Active() != "blue" {
		t.Fatalf("откат: %+v %q", rb, s.upstream())
	}

	st, _ := m.Status(ctx)
	if st.Active != "blue" || st.Slots["green"]["api"].Version == nil || *st.Slots["green"]["api"].Version != sha2 || len(st.History) != 3 {
		t.Fatalf("статус: %+v", st)
	}
	if st.History[2].Event != "rollback" {
		t.Fatalf("история: %+v", st.History)
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
	// контейнер запускается, но отдаёт другую версию: готовым не становится
	s.http.Handler = func(url, _ string) (host.HTTPResponse, error) {
		return host.HTTPResponse{Status: 200, Body: []byte(`{"version":"old"}`)}, nil
	}
	_, e := m.Deploy(ctx, sha2)
	if e == nil || e.Code != CodeUnhealthy {
		t.Fatalf("ожидали deploy_unhealthy: %v", e)
	}
	if s.upstream() != before || m.Active() != "blue" {
		t.Fatal("трафик должен остаться на прежнем слоте")
	}
	var stopped, logs bool
	for _, c := range s.runner.Calls {
		line := c.Name + " " + strings.Join(c.Args, " ")
		stopped = stopped || strings.Contains(line, "stop shop-api-green")
		logs = logs || strings.Contains(line, "logs --tail 40 shop-api-green")
	}
	if !stopped || !logs {
		t.Fatalf("слот должен быть остановлен, журнал показан: stop=%v logs=%v", stopped, logs)
	}
	if h := m.lastHistory(5); h[len(h)-1].Event != "deploy-failed" {
		t.Fatalf("история: %+v", h)
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
		t.Fatalf("файлы должны вернуться: %q active=%s", s.upstream(), m.Active())
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
	// прежний слот (green) никогда не запускался
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
			t.Fatalf("от root команды должны идти через systemd-run: %s %v", c.Name, c.Args)
		}
	}
	if !wrapped {
		t.Fatal("нет вызовов systemd-run")
	}
	if own := s.fs.Owners["/home/shop/.local/state/lite-deploy/active"]; own != [2]string{"shop", "shop"} {
		t.Fatalf("владелец active: %v", own)
	}
	var reload bool
	for _, c := range s.runner.Calls {
		reload = reload || (c.Name == "systemctl" && strings.Join(c.Args, " ") == "reload caddy")
	}
	if !reload {
		t.Fatal("от root reload без sudo")
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
