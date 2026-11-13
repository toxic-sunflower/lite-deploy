package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const tok = "0123456789abcdef0123456789abcdef"
const sha = "1111111111111111111111111111111111111111"

func newServer(h string) (*Server, *host.FakeRunner) {
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		return host.Result{Stdout: []byte(`{"ok":true,"api_version":"v1","command":"x","data":{}}` + "\n")}, nil
	}}
	return &Server{Runner: r, Host: h, Bin: "/bin/lite-deploy", Token: tok}, r
}

func do(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Token", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPageAndToken(t *testing.T) {
	s, r := newServer("")
	h := s.Handler()
	if w := do(h, "GET", "/", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "lite-deploy") {
		t.Fatalf("страница: %d", w.Code)
	}
	if w := do(h, "GET", "/api/status", "", ""); w.Code != 401 {
		t.Fatalf("без токена должно быть 401, получили %d", w.Code)
	}
	if w := do(h, "POST", "/api/deploy", `{"project":"shop","sha":"`+sha+`"}`, "wrong"); w.Code != 401 {
		t.Fatalf("неверный токен: %d", w.Code)
	}
	if len(r.Calls) != 0 {
		t.Fatalf("без токена ничего не должно запускаться: %v", r.Calls)
	}
}

func TestDeployRunsCLI(t *testing.T) {
	s, r := newServer("")
	h := s.Handler()
	w := do(h, "POST", "/api/deploy", `{"project":"shop","sha":"`+sha+`"}`, tok)
	if w.Code != 200 || len(r.Calls) != 1 {
		t.Fatalf("%d %v", w.Code, r.Calls)
	}
	c := r.Calls[0]
	if c.Name != "/bin/lite-deploy" || strings.Join(c.Args, " ") != "deploy shop "+sha+" --json" {
		t.Fatalf("%s %v", c.Name, c.Args)
	}
	do(h, "POST", "/api/rollback", `{"project":"shop"}`, tok)
	if got := strings.Join(r.Calls[1].Args, " "); got != "rollback shop --json" {
		t.Fatal(got)
	}
}

func TestRemoteUsesSSH(t *testing.T) {
	s, r := newServer("admin@203.0.113.5")
	do(s.Handler(), "GET", "/api/status", "", tok)
	c := r.Calls[0]
	if c.Name != "ssh" || strings.Join(c.Args, " ") != "-o BatchMode=yes -o ConnectTimeout=10 admin@203.0.113.5 sudo -n lite-deploy status --json" {
		t.Fatalf("%s %v", c.Name, c.Args)
	}
}

func TestInputValidation(t *testing.T) {
	s, r := newServer("")
	h := s.Handler()
	for _, body := range []string{
		`{"project":"../x","sha":"` + sha + `"}`,
		`{"project":"shop; rm -rf /","sha":"` + sha + `"}`,
		`{"project":"shop","sha":"abc"}`,
		`{"project":"shop","sha":"` + sha + `","extra":1}`,
		`not json`,
	} {
		if w := do(h, "POST", "/api/deploy", body, tok); w.Code != 400 {
			t.Errorf("%q: код %d", body, w.Code)
		}
	}
	if len(r.Calls) != 0 {
		t.Fatalf("плохой ввод не должен доходить до команд: %v", r.Calls)
	}
}

func TestGuardRejectsForeignHostAndOrigin(t *testing.T) {
	s, _ := newServer("")
	h := s.Handler()
	req := httptest.NewRequest("GET", "http://evil.example.com/", nil)
	if w := httptest.NewRecorder(); func() bool { h.ServeHTTP(w, req); return w.Code != 403 }() {
		t.Fatal("чужой Host должен отклоняться")
	}
	req = httptest.NewRequest("POST", "http://127.0.0.1:8787/api/rollback", strings.NewReader(`{"project":"shop"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token", tok)
	req.Header.Set("Origin", "http://evil.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("чужой Origin: %d", w.Code)
	}
}

func TestValidate(t *testing.T) {
	if (&Server{Token: tok, Host: "-oProxyCommand=x"}).Validate() == nil {
		t.Fatal("адрес, начинающийся с дефиса, должен отклоняться")
	}
	if (&Server{Token: "short"}).Validate() == nil {
		t.Fatal("короткий токен")
	}
	if err := (&Server{Token: tok, Host: "admin@srv.example.com"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCLIFailureBecomesJSONError(t *testing.T) {
	s, r := newServer("")
	r.Handler = func(host.Cmd) (host.Result, error) {
		return host.Result{ExitCode: 255, Stderr: []byte("ssh: connect refused")}, nil
	}
	w := do(s.Handler(), "GET", "/api/status", "", tok)
	if !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), "connect refused") {
		t.Fatal(w.Body.String())
	}
}

func TestFirewallAPI(t *testing.T) {
	s, r := newServer("")
	h := s.Handler()
	if w := do(h, "GET", "/api/firewall", "", ""); w.Code != 401 {
		t.Fatalf("без токена: %d", w.Code)
	}
	do(h, "GET", "/api/firewall", "", tok)
	do(h, "POST", "/api/firewall/allow", `{"port":8080,"proto":"tcp","from":"10.0.0.0/8"}`, tok)
	do(h, "POST", "/api/firewall/remove", `{"port":8080,"proto":"tcp","from":"","action":"allow"}`, tok)
	do(h, "POST", "/api/firewall/enable", `{}`, tok)
	want := []string{
		"firewall status --json",
		"firewall allow 8080/tcp --from 10.0.0.0/8 --yes --json",
		"firewall remove 8080/tcp --action allow --yes --json",
		"firewall enable --yes --json",
	}
	if len(r.Calls) != len(want) {
		t.Fatalf("вызовов %d: %v", len(r.Calls), r.Calls)
	}
	for i, w := range want {
		if got := strings.Join(r.Calls[i].Args, " "); got != w {
			t.Errorf("%d: %q, ожидалось %q", i, got, w)
		}
	}
}

func TestFirewallValidation(t *testing.T) {
	s, r := newServer("")
	h := s.Handler()
	for _, body := range []string{
		`{"port":0,"proto":"tcp"}`, `{"port":70000,"proto":"tcp"}`, `{"port":80,"proto":"sctp"}`,
		`{"port":80,"proto":"tcp","from":"--force"}`, `{"port":80,"proto":"tcp","from":"1.2.3.4; reboot"}`,
		`{"port":80,"proto":"tcp","extra":1}`, `not json`,
	} {
		if w := do(h, "POST", "/api/firewall/allow", body, tok); w.Code != 400 {
			t.Errorf("%s: код %d", body, w.Code)
		}
	}
	if w := do(h, "POST", "/api/firewall/remove", `{"port":8080,"proto":"tcp","action":"drop"}`, tok); w.Code != 400 {
		t.Errorf("действие: код %d", w.Code)
	}
	if len(r.Calls) != 0 {
		t.Fatalf("при неверном вводе ничего не запускается: %v", r.Calls)
	}
}
