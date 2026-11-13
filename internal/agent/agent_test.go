package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const sha = "1111111111111111111111111111111111111111"

func TestCLIArgs(t *testing.T) {
	ok := map[string]Request{
		"status":                      {Cmd: "status"},
		"status shop":                 {Cmd: "status", Args: []string{"shop"}},
		"deploy shop " + sha:          {Cmd: "deploy", Args: []string{"shop", sha}},
		"rollback shop":               {Cmd: "rollback", Args: []string{"shop"}},
		"firewall status":             {Cmd: "firewall.status"},
		"firewall enable --yes":       {Cmd: "firewall.enable"},
		"firewall allow 80/tcp --yes": {Cmd: "firewall.allow", Args: []string{"80/tcp", ""}},
		"firewall allow 80/tcp --from 10.0.0.0/8 --yes":                {Cmd: "firewall.allow", Args: []string{"80/tcp", "10.0.0.0/8"}},
		"firewall remove 8080/udp --action limit --yes":                {Cmd: "firewall.remove", Args: []string{"8080/udp", "", "limit"}},
		"firewall remove 8080/udp --action allow --from 1.2.3.4 --yes": {Cmd: "firewall.remove", Args: []string{"8080/udp", "1.2.3.4", "allow"}},
	}
	for want, req := range ok {
		got, err := req.CLIArgs()
		if err != nil || strings.Join(got, " ") != want {
			t.Errorf("%+v: %v %v, ожидалось %q", req, got, err, want)
		}
	}
	bad := []Request{
		{Cmd: "apply"}, {Cmd: "shell", Args: []string{"id"}}, {Cmd: ""}, {Cmd: "init"},
		{Cmd: "status", Args: []string{"../etc"}}, {Cmd: "status", Args: []string{"a", "b"}},
		{Cmd: "deploy", Args: []string{"shop"}}, {Cmd: "deploy", Args: []string{"shop", "abc"}}, {Cmd: "deploy", Args: []string{"Shop", sha}},
		{Cmd: "deploy", Args: []string{"shop; id", sha}},
		{Cmd: "firewall.allow", Args: []string{"80", ""}}, {Cmd: "firewall.allow", Args: []string{"99999/tcp", ""}},
		{Cmd: "firewall.allow", Args: []string{"80/tcp", "--force"}}, {Cmd: "firewall.allow", Args: []string{"80/tcp"}},
		{Cmd: "firewall.remove", Args: []string{"80/tcp", "", "drop"}}, {Cmd: "firewall.enable", Args: []string{"x"}},
	}
	for _, req := range bad {
		if _, err := req.CLIArgs(); err == nil {
			t.Errorf("%+v должен отклоняться", req)
		}
	}
}

func TestExec(t *testing.T) {
	var ran []string
	run := func(args []string) []byte {
		ran = append(ran, strings.Join(args, " "))
		return []byte(`{"ok":true,"api_version":"v1","command":"x"}`)
	}
	var out, journal bytes.Buffer
	Exec(strings.NewReader(`{"cmd":"deploy","args":["shop","`+sha+`"],"caller":"anna"}`), &out, run, &journal)
	if len(ran) != 1 || ran[0] != "deploy shop "+sha+" --json" || !strings.Contains(journal.String(), "caller=anna") {
		t.Fatalf("%v %q", ran, journal.String())
	}
	for _, in := range []string{`{"cmd":"apply","args":[]}`, `not json`, `{"cmd":"status","bogus":1}`, ``} {
		out.Reset()
		ran = nil
		Exec(strings.NewReader(in), &out, run, &journal)
		if len(ran) != 0 || !strings.Contains(out.String(), `"ok":false`) {
			t.Errorf("%q: выполнено %v, ответ %s", in, ran, out.String())
		}
	}
	// имя вызывающего в журнале проверяется: подделка не должна ломать строку журнала
	journal.Reset()
	Exec(strings.NewReader(`{"cmd":"status","caller":"x\nвзлом: root"}`), &out, run, &journal)
	if strings.Count(journal.String(), "\n") != 1 {
		t.Fatalf("журнал: %q", journal.String())
	}
}

// newAgent собирает агента, соединённого с исполнителем через net.Pipe.
func newAgent(t *testing.T, ran *[]string, allowed bool) (*Agent, *bytes.Buffer) {
	t.Helper()
	var audit bytes.Buffer
	a := &Agent{
		ExecDial: func(ctx context.Context) (net.Conn, error) {
			c1, c2 := net.Pipe()
			go func() {
				defer c2.Close()
				Exec(c2, c2, func(args []string) []byte {
					*ran = append(*ran, strings.Join(args, " "))
					return []byte(`{"ok":true,"api_version":"v1","command":"x","data":{}}`)
				}, &bytes.Buffer{})
			}()
			return c1, nil
		},
		Allowed: func(uid int) (string, bool) { return "anna", allowed && uid == 1000 },
		Audit:   &audit, Version: "test",
	}
	return a, &audit
}

func req(h http.Handler, uid int, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r = r.WithContext(WithUID(r.Context(), uid))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAgentRoutes(t *testing.T) {
	var ran []string
	a, audit := newAgent(t, &ran, true)
	h := a.Handler()
	cases := []struct{ method, path, body, want string }{
		{"GET", "/v1/status", "", "status --json"},
		{"POST", "/v1/deploy", `{"project":"shop","sha":"` + sha + `"}`, "deploy shop " + sha + " --json"},
		{"POST", "/v1/rollback", `{"project":"shop"}`, "rollback shop --json"},
		{"GET", "/v1/firewall", "", "firewall status --json"},
		{"POST", "/v1/firewall/allow", `{"port":8080,"proto":"tcp","from":"10.0.0.0/8"}`, "firewall allow 8080/tcp --from 10.0.0.0/8 --yes --json"},
		{"POST", "/v1/firewall/remove", `{"port":8080,"proto":"tcp","action":"allow"}`, "firewall remove 8080/tcp --action allow --yes --json"},
		{"POST", "/v1/firewall/enable", `{}`, "firewall enable --yes --json"},
	}
	for _, c := range cases {
		w := req(h, 1000, c.method, c.path, c.body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	if len(ran) != len(cases) {
		t.Fatalf("выполнено %v", ran)
	}
	for i, c := range cases {
		if ran[i] != c.want {
			t.Errorf("%d: %q, ожидалось %q", i, ran[i], c.want)
		}
	}
	if n := strings.Count(audit.String(), "\n"); n != len(cases) || !strings.Contains(audit.String(), `"user":"anna"`) {
		t.Fatalf("журнал агента: %s", audit.String())
	}
}

func TestAgentAuthAndValidation(t *testing.T) {
	var ran []string
	a, _ := newAgent(t, &ran, true)
	h := a.Handler()
	if w := req(h, 1001, "GET", "/v1/status", ""); w.Code != 403 {
		t.Fatalf("чужой uid: %d", w.Code)
	}
	if w := req(h, -1, "GET", "/v1/status", ""); w.Code != 403 {
		t.Fatalf("неизвестный uid: %d", w.Code)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/v1/deploy", `{"project":"shop","sha":"zzz"}`},
		{"POST", "/v1/deploy", `{"project":"shop","sha":"` + sha + `","x":1}`},
		{"POST", "/v1/firewall/allow", `{"port":80,"proto":"tcp","from":"evil"}`},
		{"POST", "/v1/rollback", `nope`},
	} {
		if w := req(h, 1000, c.method, c.path, c.body); w.Code != 400 && !strings.Contains(w.Body.String(), `"ok":false`) {
			t.Errorf("%s %s: %d", c.method, c.path, w.Code)
		}
	}
	if w := req(h, 1000, "GET", "/v1/deploy", ""); w.Code != 405 {
		t.Fatalf("метод: %d", w.Code)
	}
	if len(ran) != 0 {
		t.Fatalf("при отказе исполнитель не вызывается: %v", ran)
	}
	a2, _ := newAgent(t, &ran, false)
	if w := req(a2.Handler(), 1000, "GET", "/v1/status", ""); w.Code != 403 {
		t.Fatalf("не в группе: %d", w.Code)
	}
}

func TestExecUnavailable(t *testing.T) {
	a := &Agent{ExecDial: func(context.Context) (net.Conn, error) { return nil, net.ErrClosed },
		Allowed: func(int) (string, bool) { return "anna", true }}
	w := req(a.Handler(), 1, "GET", "/v1/status", "")
	var e struct{ Error struct{ Code string } }
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	if e.Error.Code != "precondition_failed" {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestGroupAllowed(t *testing.T) {
	m := host.NewMemFS()
	m.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\nanna:x:1000:1000::/home/anna:/bin/bash\nbob:x:1001:1001::/home/bob:/bin/bash\ncarl:x:1002:500::/home/carl:/bin/bash\n")
	m.Files["/etc/group"] = []byte("lite-deploy-admins:x:500:bob\nanna:x:1000:\n")
	allowed := GroupAllowed(m, "lite-deploy-admins")
	for uid, want := range map[int]bool{0: false, 1000: false, 1001: true, 1002: true, 4242: false} {
		if _, ok := allowed(uid); ok != want {
			t.Errorf("uid %d: %v, ожидалось %v", uid, ok, want)
		}
	}
}

func TestInstall(t *testing.T) {
	m := host.NewMemFS()
	var calls []string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		calls = append(calls, line)
		if c.Name == "getent" || (c.Name == "id" && strings.Contains(line, ServiceUser)) {
			return host.Result{ExitCode: 2}, nil // нет группы и пользователя службы
		}
		return host.Result{}, nil
	}}
	if err := Install(context.Background(), r, m, "/usr/local/bin/lite-deploy", []string{"anna"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"groupadd --system " + AdminGroup, "useradd --system", "usermod -aG " + AdminGroup + " anna", "systemctl daemon-reload", "systemctl enable --now lite-deploy-agent.socket lite-deploy-exec.socket"} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет вызова %q в\n%s", want, joined)
		}
	}
	for name, body := range Units("/usr/local/bin/lite-deploy") {
		got, ok := m.Files[UnitDir+"/"+name]
		if !ok || string(got) != body {
			t.Errorf("unit %s не записан", name)
		}
	}
	if !strings.Contains(string(m.Files[UnitDir+"/lite-deploy-exec.socket"]), "SocketMode=0600") ||
		!strings.Contains(string(m.Files[UnitDir+"/lite-deploy-agent.socket"]), "SocketGroup="+AdminGroup) {
		t.Fatal("права сокетов")
	}
	if err := Install(context.Background(), r, m, "/x", []string{"bad name; rm"}); err == nil {
		t.Fatal("неверное имя администратора должно отклоняться")
	}
}
