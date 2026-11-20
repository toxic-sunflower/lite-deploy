package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/toxic-sunflower/lite-deploy/internal/owner"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const sha = "1111111111111111111111111111111111111111"

func TestCLIArgs(t *testing.T) {
	ok := map[string]Request{
		"status":                                 {Cmd: "status"},
		"status shop":                            {Cmd: "status", Args: []string{"shop"}},
		"deploy shop " + sha:                     {Cmd: "deploy", Args: []string{"shop", sha}},
		"rollback shop":                          {Cmd: "rollback", Args: []string{"shop"}},
		"firewall status":                        {Cmd: "firewall.status"},
		"info":                                   {Cmd: "info"},
		"verify":                                 {Cmd: "verify"},
		"plugin run package install nginx --yes": {Cmd: "plugin.run", Args: []string{"package", "install", "nginx"}},
		"firewall enable --yes":                  {Cmd: "firewall.enable"},
		"project bundle --dry-run":               {Cmd: "project.preview", Body: `{"project":{}}`},
		"project bundle --yes":                   {Cmd: "project.create", Body: `{"project":{}}`},
		"secret pubkey":                          {Cmd: "project.key"},
		"fix --yes":                              {Cmd: "fix"},
		"firewall allow 80/tcp --yes":            {Cmd: "firewall.allow", Args: []string{"80/tcp", ""}},
		"firewall allow 80/tcp --from 10.0.0.0/8 --yes":                {Cmd: "firewall.allow", Args: []string{"80/tcp", "10.0.0.0/8"}},
		"firewall remove 8080/udp --action limit --yes":                {Cmd: "firewall.remove", Args: []string{"8080/udp", "", "limit"}},
		"firewall remove 8080/udp --action allow --from 1.2.3.4 --yes": {Cmd: "firewall.remove", Args: []string{"8080/udp", "1.2.3.4", "allow"}},
	}
	for want, req := range ok {
		got, err := req.CLIArgs()
		if err != nil || strings.Join(got, " ") != want {
			t.Errorf("%+v: %v %v, expected %q", req, got, err, want)
		}
	}
	bad := []Request{
		{Cmd: "apply"}, {Cmd: "shell", Args: []string{"id"}}, {Cmd: ""}, {Cmd: "init"},
		{Cmd: "status", Args: []string{"../etc"}}, {Cmd: "status", Args: []string{"a", "b"}},
		{Cmd: "deploy", Args: []string{"shop"}}, {Cmd: "deploy", Args: []string{"shop", "abc"}}, {Cmd: "deploy", Args: []string{"Shop", sha}},
		{Cmd: "deploy", Args: []string{"shop; id", sha}},
		{Cmd: "firewall.allow", Args: []string{"80", ""}}, {Cmd: "firewall.allow", Args: []string{"99999/tcp", ""}},
		{Cmd: "firewall.allow", Args: []string{"80/tcp", "--force"}}, {Cmd: "firewall.allow", Args: []string{"80/tcp"}},
		{Cmd: "plugin.run"}, {Cmd: "plugin.run", Args: []string{"package"}}, {Cmd: "plugin.run", Args: []string{"Package", "install"}},
		{Cmd: "plugin.run", Args: []string{"package", "install", "a\nb"}}, {Cmd: "info", Args: []string{"x"}},
		{Cmd: "firewall.remove", Args: []string{"80/tcp", "", "drop"}}, {Cmd: "firewall.enable", Args: []string{"x"}},
		{Cmd: "project.create"}, {Cmd: "project.preview", Body: "not json"}, {Cmd: "project.create", Body: strings.Repeat("a", MaxBody+1)},
		{Cmd: "project.create", Args: []string{"x"}, Body: "{}"}, {Cmd: "status", Body: "{}"}, {Cmd: "fix", Args: []string{"--dry-run"}}, {Cmd: "deploy", Args: []string{"shop", sha}, Body: "{}"},
	}
	for _, req := range bad {
		if _, err := req.CLIArgs(); err == nil {
			t.Errorf("%+v must be rejected", req)
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
			t.Errorf("%q: ran %v, answer %s", in, ran, out.String())
		}
	}
	// the caller name in the journal is checked: a forgery must not break the journal line
	journal.Reset()
	Exec(strings.NewReader(`{"cmd":"status","caller":"x\nhack: root"}`), &out, run, &journal)
	if strings.Count(journal.String(), "\n") != 1 {
		t.Fatalf("journal: %q", journal.String())
	}
}

// newAgent builds an agent connected to the executor through net.Pipe.
func newAgent(t *testing.T, ran *[]string, allowed bool) (*Agent, *bytes.Buffer) {
	t.Helper()
	var audit bytes.Buffer
	a := &Agent{
		ExecDial: func(ctx context.Context) (net.Conn, error) {
			c1, c2 := net.Pipe()
			go func() {
				defer func() { _ = c2.Close() }()
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
		{"GET", "/v1/plugins", "", "info --json"},
		{"GET", "/v1/verify", "", "verify --json"},
		{"POST", "/v1/plugin", `{"plugin":"package","command":"check","args":["nginx"]}`, "plugin run package check nginx --yes --json"},
		{"GET", "/v1/firewall", "", "firewall status --json"},
		{"POST", "/v1/firewall/allow", `{"port":8080,"proto":"tcp","from":"10.0.0.0/8"}`, "firewall allow 8080/tcp --from 10.0.0.0/8 --yes --json"},
		{"POST", "/v1/firewall/remove", `{"port":8080,"proto":"tcp","action":"allow"}`, "firewall remove 8080/tcp --action allow --yes --json"},
		{"POST", "/v1/firewall/enable", `{}`, "firewall enable --yes --json"},
		{"POST", "/v1/project/preview", `{"project":{"name":"shop"}}`, "project bundle --dry-run --json"},
		{"POST", "/v1/project/create", `{"project":{"name":"shop"}}`, "project bundle --yes --json"},
		{"GET", "/v1/project/key", "", "secret pubkey --json"},
		{"POST", "/v1/fix", `{}`, "fix --yes --json"},
		{"GET", "/v1/update", "", "update --check --json"},
		{"POST", "/v1/update/apply", `{}`, "update --yes --json"},
		{"GET", "/v1/ingress", "", "ingress --json"},
		{"GET", "/v1/secrets?project=shop", "", "secret list shop --json"},
		{"GET", "/v1/project/export?project=shop", "", "project export shop --json"},
		{"POST", "/v1/secret", `{"project":"shop","name":"api_key","ciphertext":"-----BEGIN AGE ENCRYPTED FILE-----"}`, "secret put shop api_key --json"},
		{"POST", "/v1/secret/remove", `{"project":"shop","name":"api_key"}`, "secret remove shop api_key --json"},
		{"POST", "/v1/publish", `{"project":"shop","site":"main","mode":"direct"}`, "publish shop main direct --yes --json"},
	}
	for _, c := range cases {
		w := req(h, 1000, c.method, c.path, c.body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	if len(ran) != len(cases) {
		t.Fatalf("ran %v", ran)
	}
	for i, c := range cases {
		if ran[i] != c.want {
			t.Errorf("%d: %q, expected %q", i, ran[i], c.want)
		}
	}
	if n := strings.Count(audit.String(), "\n"); n != len(cases) || !strings.Contains(audit.String(), `"user":"anna"`) {
		t.Fatalf("agent journal: %s", audit.String())
	}
}

func TestAgentAuthAndValidation(t *testing.T) {
	var ran []string
	a, _ := newAgent(t, &ran, true)
	h := a.Handler()
	if w := req(h, 1001, "GET", "/v1/status", ""); w.Code != 403 {
		t.Fatalf("a foreign uid: %d", w.Code)
	}
	if w := req(h, -1, "GET", "/v1/status", ""); w.Code != 403 {
		t.Fatalf("unknown uid: %d", w.Code)
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
	for _, body := range []string{`[]`, `"x"`, `{`, strings.Repeat("x", MaxBody+10)} {
		if w := req(h, 1000, "POST", "/v1/project/create", body); w.Code != 400 {
			t.Errorf("the body %.10q was accepted: %d", body, w.Code)
		}
	}
	if w := req(h, 1000, "GET", "/v1/deploy", ""); w.Code != 405 {
		t.Fatalf("method: %d", w.Code)
	}
	if len(ran) != 0 {
		t.Fatalf("on a refusal the executor is not called: %v", ran)
	}
	a2, _ := newAgent(t, &ran, false)
	if w := req(a2.Handler(), 1000, "GET", "/v1/status", ""); w.Code != 403 {
		t.Fatalf("not in the group: %d", w.Code)
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
			t.Errorf("uid %d: %v, expected %v", uid, ok, want)
		}
	}
}

func TestInstall(t *testing.T) {
	for _, tc := range []struct{ has, want string }{{"sudo", "sudo"}, {"wheel", "wheel"}} {
		m := host.NewMemFS()
		var calls []string
		r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
			line := c.Name + " " + strings.Join(c.Args, " ")
			calls = append(calls, line)
			if c.Name == "getent" && !strings.HasSuffix(line, " "+tc.has) {
				return host.Result{ExitCode: 2}, nil
			}
			if c.Name == "id" {
				return host.Result{ExitCode: 1}, nil // the service user does not exist yet
			}
			return host.Result{}, nil
		}}
		group, err := Install(context.Background(), r, m, "/usr/local/bin/lite-deploy")
		if err != nil || group != tc.want {
			t.Fatalf("%s: %q %v", tc.has, group, err)
		}
		joined := strings.Join(calls, "\n")
		for _, want := range []string{"useradd --system", "systemctl daemon-reload", "systemctl enable --now lite-deploy-agent.socket lite-deploy-exec.socket", "systemctl try-restart lite-deploy-agent.service"} {
			if !strings.Contains(joined, want) {
				t.Errorf("no call %q in\n%s", want, joined)
			}
		}
		if strings.Contains(joined, "groupadd") || strings.Contains(joined, "usermod") {
			t.Errorf("there must be no new groups or user edits:\n%s", joined)
		}
		for name, body := range Units("/usr/local/bin/lite-deploy", tc.want) {
			if got, ok := m.Files[UnitDir+"/"+name]; !ok || string(got) != body {
				t.Errorf("unit %s was not written", name)
			}
		}
		if !strings.Contains(string(m.Files[UnitDir+"/lite-deploy-exec.socket"]), "SocketMode=0600") ||
			!strings.Contains(string(m.Files[UnitDir+"/lite-deploy-agent.socket"]), "SocketGroup="+tc.want) ||
			!strings.Contains(string(m.Files[UnitDir+"/lite-deploy-agent.service"]), "agent serve --group "+tc.want) {
			t.Fatal("socket permissions and group")
		}
	}
}

func TestInstallNoAdminGroup(t *testing.T) {
	r := &host.FakeRunner{Handler: func(host.Cmd) (host.Result, error) { return host.Result{ExitCode: 2}, nil }}
	if _, err := Install(context.Background(), r, host.NewMemFS(), "/x"); err == nil {
		t.Fatal("without the sudo and wheel groups the installation must refuse")
	}
}

func TestStatusProjectQuery(t *testing.T) {
	var ran []string
	a, _ := newAgent(t, &ran, true)
	h := a.Handler()
	if w := req(h, 1000, "GET", "/v1/status?project=shop", ""); w.Code != 200 || len(ran) != 1 || ran[0] != "status shop --json" {
		t.Fatalf("%d %v", w.Code, ran)
	}
	if w := req(h, 1000, "GET", "/v1/status?project=../x", ""); w.Code != 400 {
		t.Fatalf("a bad project name: %d", w.Code)
	}
}

func TestUninstallRemovesEverythingAndIsRepeatable(t *testing.T) {
	m := host.NewMemFS()
	for name := range Units("/usr/local/bin/lite-deploy", "sudo") {
		m.Files[UnitDir+"/"+name] = []byte("x")
	}
	m.Files["/etc/lite-deploy/exec.age"] = []byte("key")
	m.Files["/var/lib/lite-deploy/secrets/shop/api"] = []byte("secret")
	var calls []string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Name+" "+strings.Join(c.Args, " "))
		return host.Result{}, nil
	}}
	for i := 0; i < 2; i++ { // the second run: nothing is left, it must still succeed
		if err := Uninstall(context.Background(), r, m); err != nil {
			t.Fatal(err)
		}
	}
	for name := range Units("", "sudo") {
		if _, ok := m.Files[UnitDir+"/"+name]; ok {
			t.Errorf("unit %s was not removed", name)
		}
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"systemctl disable --now lite-deploy-agent.socket", "systemctl daemon-reload", "userdel lite-deploy"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no call %q in\n%s", want, joined)
		}
	}
	if _, ok := m.Files["/etc/lite-deploy/exec.age"]; !ok {
		t.Error("the executor key must not be removed")
	}
	if _, ok := m.Files["/var/lib/lite-deploy/secrets/shop/api"]; !ok {
		t.Error("secrets must not be removed")
	}
}

func TestQuotaStopsTooManyChanges(t *testing.T) {
	var ran []string
	a, _ := newAgent(t, &ran, true)
	a.ApplyPerHour = 3
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return now }
	h := a.Handler()
	for i := 0; i < 3; i++ {
		if w := req(h, 1000, "POST", "/v1/rollback", `{"project":"shop"}`); !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatalf("change %d must pass: %s", i+1, w.Body.String())
		}
	}
	if w := req(h, 1000, "POST", "/v1/rollback", `{"project":"shop"}`); !strings.Contains(w.Body.String(), "quota_exceeded") {
		t.Fatalf("the fourth change in an hour must be refused: %s", w.Body.String())
	}
	if w := req(h, 1000, "GET", "/v1/status", ""); !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("reading never counts: %s", w.Body.String())
	}
	now = now.Add(61 * time.Minute)
	if w := req(h, 1000, "POST", "/v1/rollback", `{"project":"shop"}`); !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("after an hour changes are allowed again: %s", w.Body.String())
	}
}

func TestOwnerKeyProtectsDangerousActions(t *testing.T) {
	var ran []string
	a, _ := newAgent(t, &ran, true)
	mem := host.NewMemFS()
	a.FS, a.KeysPath = mem, "/etc/lite-deploy/owner-keys"
	a.serverID = "age1testserver"
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return now }
	h := a.Handler()

	body := `{"port":8080,"proto":"tcp","from":""}`
	// no key registered: nothing is required
	if w := req(h, 1000, "POST", "/v1/firewall/allow", body); !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("without a registered key a change is allowed: %s", w.Body.String())
	}
	pub, priv, _ := owner.GenerateKey()
	_ = mem.MkdirAll("/etc/lite-deploy", 0o755)
	_ = mem.WriteFile(a.KeysPath, []byte(owner.Line(pub, "laptop")+"\n"), 0o644)

	if w := req(h, 1000, "POST", "/v1/firewall/allow", body); w.Code != 403 || !strings.Contains(w.Body.String(), "owner_confirmation_required") {
		t.Fatalf("a dangerous action without a signature must be refused: %d %s", w.Code, w.Body.String())
	}
	// reading and deploying stay open (CI deploys with a restricted key)
	for _, c := range []struct{ m, p, b string }{{"GET", "/v1/status", ""}, {"GET", "/v1/verify", ""}, {"POST", "/v1/rollback", `{"project":"shop"}`}} {
		if w := req(h, 1000, c.m, c.p, c.b); !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatalf("%s %s must stay open: %s", c.m, c.p, w.Body.String())
		}
	}
	sig, _ := owner.Sign(priv, "age1testserver", "POST", "/v1/firewall/allow", []byte(body), now)
	r := httptest.NewRequest("POST", "/v1/firewall/allow", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Lite-Sig", sig)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(WithUID(r.Context(), 1000)))
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("a signed action must pass: %d %s", w.Code, w.Body.String())
	}
	// the same signature cannot be used again
	r2 := httptest.NewRequest("POST", "/v1/firewall/allow", strings.NewReader(body))
	r2.Header.Set("Content-Type", "application/json")
	r2.Header.Set("X-Lite-Sig", sig)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2.WithContext(WithUID(r2.Context(), 1000)))
	if w2.Code != 403 {
		t.Fatalf("a replayed signature must be refused: %d %s", w2.Code, w2.Body.String())
	}
}

// The OpenAPI description must list every route of the agent, and nothing else.
func TestOpenAPIDescribesEveryRoute(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("../../schemas/agent.openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`route\(http\.Method(Get|Post), "(/v1/[a-z/-]*)"|HandleFunc\("(/v1/info)"`)
	found := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		switch {
		case m[3] != "":
			found[m[3]] = "get"
		default:
			found[m[2]] = strings.ToLower(m[1])
		}
	}
	found["/v1/project/preview"], found["/v1/project/create"] = "post", "post" // registered in a loop
	delete(found, "/v1/project/")
	for p, method := range found {
		if _, ok := doc.Paths[p][method]; !ok {
			t.Errorf("%s %s is a route of the agent but is missing in schemas/agent.openapi.json", method, p)
		}
	}
	for p := range doc.Paths {
		if _, ok := found[p]; !ok {
			t.Errorf("%s is in schemas/agent.openapi.json but is not a route of the agent", p)
		}
	}
}

func TestFallbackAuthArguments(t *testing.T) {
	got, err := Request{Cmd: "fallback.auth", Args: []string{"shop", "main", "set", "admin", "/admin*"}, Body: "a long enough password"}.CLIArgs()
	want := "fallback-auth shop main set --user admin --password-stdin --path /admin* --yes"
	if err != nil || strings.Join(got, " ") != want {
		t.Fatalf("got %v %v, want %s", got, err, want)
	}
	for _, bad := range []Request{
		{Cmd: "fallback.auth", Args: []string{"shop", "main", "set", "admin"}, Body: "short"},
		{Cmd: "fallback.auth", Args: []string{"shop", "main", "set", "Admin"}, Body: "a long enough password"},
		{Cmd: "fallback.auth", Args: []string{"shop", "main", "set", "admin", "/a b"}, Body: "a long enough password"},
		{Cmd: "fallback.auth", Args: []string{"shop", "main", "off"}, Body: "x"},
		{Cmd: "fallback.auth", Args: []string{"shop", "main", "boom"}},
	} {
		if _, err := bad.CLIArgs(); err == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
}
