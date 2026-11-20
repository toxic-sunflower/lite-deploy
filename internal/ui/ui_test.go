package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"filippo.io/age"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/cfaccess"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/uiupdate"
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
	if w := do(h, "GET", "/", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Lite Deploy") {
		t.Fatalf("page: %d", w.Code)
	}
	if w := do(h, "GET", "/api/status", "", ""); w.Code != 401 {
		t.Fatalf("without a token it must be 401, got %d", w.Code)
	}
	if w := do(h, "POST", "/api/deploy", `{"project":"shop","sha":"`+sha+`"}`, "wrong"); w.Code != 401 {
		t.Fatalf("an invalid token: %d", w.Code)
	}
	if len(r.Calls) != 0 {
		t.Fatalf("without a token nothing may run: %v", r.Calls)
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
	if c.Name != "/bin/lite-deploy" || strings.Join(c.Args, " ") != "call POST /v1/deploy" || string(c.Stdin) != `{"project":"shop","sha":"`+sha+`"}` {
		t.Fatalf("%s %v %s", c.Name, c.Args, c.Stdin)
	}
	do(h, "POST", "/api/rollback", `{"project":"shop"}`, tok)
	if got := strings.Join(r.Calls[1].Args, " "); got != "call POST /v1/rollback" || string(r.Calls[1].Stdin) != `{"project":"shop"}` {
		t.Fatal(got, string(r.Calls[1].Stdin))
	}
}

func TestRemoteUsesSSH(t *testing.T) {
	s, r := newServer("admin@203.0.113.5")
	do(s.Handler(), "GET", "/api/status", "", tok)
	c := r.Calls[0]
	if c.Name != "ssh" || strings.Join(c.Args, " ") != "-o BatchMode=yes -o ConnectTimeout=10 admin@203.0.113.5 lite-deploy call GET /v1/status" {
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
			t.Errorf("%q: code %d", body, w.Code)
		}
	}
	if len(r.Calls) != 0 {
		t.Fatalf("bad input must not reach the commands: %v", r.Calls)
	}
}

func TestGuardRejectsForeignHostAndOrigin(t *testing.T) {
	s, _ := newServer("")
	h := s.Handler()
	req := httptest.NewRequest("GET", "http://evil.example.com/", nil)
	if w := httptest.NewRecorder(); func() bool { h.ServeHTTP(w, req); return w.Code != 403 }() {
		t.Fatal("a foreign Host must be rejected")
	}
	req = httptest.NewRequest("POST", "http://127.0.0.1:8787/api/rollback", strings.NewReader(`{"project":"shop"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token", tok)
	req.Header.Set("Origin", "http://evil.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("a foreign Origin: %d", w.Code)
	}
}

func TestValidate(t *testing.T) {
	if (&Server{Token: tok, Host: "-oProxyCommand=x"}).Validate() == nil {
		t.Fatal("an address starting with a hyphen must be rejected")
	}
	if (&Server{Token: "short"}).Validate() == nil {
		t.Fatal("a short token")
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
		t.Fatalf("without a token: %d", w.Code)
	}
	do(h, "GET", "/api/firewall", "", tok)
	do(h, "POST", "/api/firewall/allow", `{"port":8080,"proto":"tcp","from":"10.0.0.0/8"}`, tok)
	do(h, "POST", "/api/firewall/remove", `{"port":8080,"proto":"tcp","from":"","action":"allow"}`, tok)
	do(h, "POST", "/api/firewall/enable", `{}`, tok)
	want := []string{
		"call GET /v1/firewall",
		"call POST /v1/firewall/allow",
		"call POST /v1/firewall/remove",
		"call POST /v1/firewall/enable",
	}
	if len(r.Calls) != len(want) {
		t.Fatalf("calls %d: %v", len(r.Calls), r.Calls)
	}
	for i, w := range want {
		if got := strings.Join(r.Calls[i].Args, " "); got != w {
			t.Errorf("%d: %q, expected %q", i, got, w)
		}
	}
	if got := string(r.Calls[1].Stdin); !strings.Contains(got, `"port":8080`) || !strings.Contains(got, `"from":"10.0.0.0/8"`) {
		t.Errorf("allow body: %s", got)
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
			t.Errorf("%s: code %d", body, w.Code)
		}
	}
	if w := do(h, "POST", "/api/firewall/remove", `{"port":8080,"proto":"tcp","action":"drop"}`, tok); w.Code != 400 {
		t.Errorf("action: code %d", w.Code)
	}
	if len(r.Calls) != 0 {
		t.Fatalf("on wrong input nothing runs: %v", r.Calls)
	}
}

func TestPluginAPI(t *testing.T) {
	s, r := newServer("")
	h := s.Handler()
	if w := do(h, "GET", "/api/plugins", "", ""); w.Code != 401 {
		t.Fatalf("without a token: %d", w.Code)
	}
	do(h, "GET", "/api/plugins", "", tok)
	do(h, "GET", "/api/verify", "", tok)
	do(h, "POST", "/api/plugin", `{"plugin":"package","command":"install","args":["nginx"]}`, tok)
	do(h, "POST", "/api/plugin", `{"plugin":"package","command":"list"}`, tok)
	want := []string{"call GET /v1/plugins", "call GET /v1/verify", "call POST /v1/plugin", "call POST /v1/plugin"}
	if len(r.Calls) != len(want) {
		t.Fatalf("calls %d: %v", len(r.Calls), r.Calls)
	}
	for i, w := range want {
		if got := strings.Join(r.Calls[i].Args, " "); got != w {
			t.Errorf("%d: %q, expected %q", i, got, w)
		}
	}
	if got := string(r.Calls[2].Stdin); !strings.Contains(got, `"args":["nginx"]`) {
		t.Errorf("body: %s", got)
	}
	if got := string(r.Calls[3].Stdin); !strings.Contains(got, `"args":[]`) {
		t.Errorf("empty arguments must be a list: %s", got)
	}
}

func TestPluginValidation(t *testing.T) {
	s, r := newServer("")
	h := s.Handler()
	for _, body := range []string{
		`{"plugin":"Package","command":"list"}`, `{"plugin":"package","command":"../x"}`, `{"plugin":"package","command":"install","args":["a\nb"]}`,
		`{"plugin":"package","command":"list","extra":1}`, `not json`,
	} {
		if w := do(h, "POST", "/api/plugin", body, tok); w.Code != 400 {
			t.Errorf("%s: code %d", body, w.Code)
		}
	}
	if len(r.Calls) != 0 {
		t.Fatalf("invalid requests must not reach the server: %v", r.Calls)
	}
}

func TestProjectPreviewEncryptsSecretsLocally(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	var calls []host.Cmd
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c)
		if strings.Contains(strings.Join(c.Args, " "), "/v1/project/key") {
			return host.Result{Stdout: []byte(`{"ok":true,"api_version":"v1","command":"secret.pubkey","data":{"public_key":"` + id.Recipient().String() + `"}}`)}, nil
		}
		return host.Result{Stdout: []byte(`{"ok":true,"api_version":"v1","command":"project.bundle","data":{}}`)}, nil
	}}
	s := &Server{Runner: r, Bin: "/bin/lite-deploy", Token: tok}
	h := s.Handler()
	body := `{"project":{"name":"shop"},"secrets":{"api_key":"S3CRET-VALUE-123"},"expect_project_hash":"sha256:x"}`
	if w := do(h, "POST", "/api/project/preview", body, tok); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(calls) != 2 || strings.Join(calls[1].Args, " ") != "call POST /v1/project/preview" {
		t.Fatalf("calls: %+v", calls)
	}
	sent := string(calls[1].Stdin)
	if strings.Contains(sent, "S3CRET-VALUE-123") || !strings.Contains(sent, "BEGIN AGE ENCRYPTED FILE") || !strings.Contains(sent, `"expect_project_hash":"sha256:x"`) {
		t.Fatalf("the server must get the ciphertext, not the value: %s", sent)
	}
}

func TestProjectEndpointsValidate(t *testing.T) {
	s, r := newServer("")
	h := s.Handler()
	for _, c := range []struct {
		path, body, token string
		code              int
	}{
		{"/api/project/create", `{"project":{"name":"shop"}}`, "", 401},
		{"/api/project/create", `{"project":{"name":"Bad Name"}}`, tok, 400},
		{"/api/project/create", `{"project":{}}`, tok, 400},
		{"/api/project/create", `{"project":{"name":"shop"},"x":1}`, tok, 400},
		{"/api/project/preview", `not json`, tok, 400},
	} {
		if w := do(h, "POST", c.path, c.body, c.token); w.Code != c.code {
			t.Errorf("%s %s: %d, expected %d", c.path, c.body, w.Code, c.code)
		}
	}
	if len(r.Calls) != 0 {
		t.Fatalf("on a wrong request the agent is not called: %v", r.Calls)
	}
}

func doServer(h http.Handler, method, path, body, server string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Token", tok)
	if server != "" {
		req.Header.Set("X-Server", server)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestServerListAndSelection(t *testing.T) {
	s, r := newServer("")
	s.Host = ""
	s.Servers = &ServerList{Path: t.TempDir() + "/lite-ui/servers"}
	h := s.Handler()

	if w := doServer(h, "GET", "/api/status", "", ""); !strings.Contains(w.Body.String(), "no_server") || len(r.Calls) != 0 {
		t.Fatalf("without a selected server nothing may run: %s %v", w.Body.String(), r.Calls)
	}
	for _, host := range []string{"anna@203.0.113.7", "bob@198.51.100.2"} {
		if w := doServer(h, "POST", "/api/servers/add", `{"host":"`+host+`"}`, ""); w.Code != 200 {
			t.Fatalf("add %s: %d %s", host, w.Code, w.Body.String())
		}
	}
	if w := doServer(h, "POST", "/api/servers/add", `{"host":"not-a-server"}`, ""); w.Code != 400 {
		t.Errorf("a server without USER@ must be refused: %d", w.Code)
	}
	if w := doServer(h, "GET", "/api/servers", "", ""); !strings.Contains(w.Body.String(), `"bob@198.51.100.2","anna@203.0.113.7"`) {
		t.Fatalf("the newest server goes first: %s", w.Body.String())
	}
	// the request goes to the server the page selected, not to another one
	if w := doServer(h, "GET", "/api/status", "", "anna@203.0.113.7"); w.Code != 200 || len(r.Calls) != 1 || r.Calls[0].Args[4] != "anna@203.0.113.7" {
		t.Fatalf("wrong target: %d %v", w.Code, r.Calls)
	}
	// a server that is not in the list is refused: the page cannot make lite-ui connect anywhere
	if w := doServer(h, "GET", "/api/status", "", "evil@203.0.113.99"); !strings.Contains(w.Body.String(), "unknown_server") || len(r.Calls) != 1 {
		t.Fatalf("an unlisted server must be refused: %s %v", w.Body.String(), r.Calls)
	}
	if w := doServer(h, "POST", "/api/servers/remove", `{"host":"anna@203.0.113.7"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if strings.Contains(doServer(h, "GET", "/api/servers", "", "").Body.String(), "anna@") {
		t.Error("the removed server must be gone")
	}
}

func TestIngressAndPublishGoToTheAgent(t *testing.T) {
	s, r := newServer("anna@203.0.113.7")
	h := s.Handler()
	if w := do(h, "GET", "/api/ingress", "", tok); w.Code != 200 || len(r.Calls) != 1 || r.Calls[0].Args[len(r.Calls[0].Args)-1] != "/v1/ingress" {
		t.Fatalf("ingress: %d %v", w.Code, r.Calls)
	}
	if w := do(h, "POST", "/api/publish", `{"project":"shop","site":"main","mode":"direct"}`, tok); w.Code != 200 || len(r.Calls) != 2 {
		t.Fatalf("publish: %d %v", w.Code, r.Calls)
	}
	for _, bad := range []string{`{"project":"shop","site":"main","mode":"open"}`, `{"project":"Bad","site":"main","mode":"direct"}`, `{"project":"shop","site":"x y","mode":"direct"}`} {
		if w := do(h, "POST", "/api/publish", bad, tok); w.Code != 400 {
			t.Errorf("%s must be refused: %d", bad, w.Code)
		}
	}
	if len(r.Calls) != 2 {
		t.Errorf("a refused request must not reach the agent: %v", r.Calls)
	}
}

func TestFailedLoginsDoNotPileUp(t *testing.T) {
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		return host.Result{ExitCode: 255, Stderr: []byte("serafim@203.0.113.7: Permission denied (publickey).")}, nil
	}}
	s := &Server{Runner: r, Host: "serafim@203.0.113.7", Token: tok}
	h := s.Handler()
	// a page opens with several requests at once: only one login attempt may reach the server
	done := make(chan struct{}, 6)
	for i := 0; i < 6; i++ {
		go func() { do(h, "GET", "/api/status", "", tok); done <- struct{}{} }()
	}
	for i := 0; i < 6; i++ {
		<-done
	}
	if len(r.Calls) != 1 {
		t.Fatalf("a burst of requests must make one login attempt, made %d", len(r.Calls))
	}
	w := do(h, "GET", "/api/verify", "", tok)
	if len(r.Calls) != 1 || !strings.Contains(w.Body.String(), "ssh_failed") || !strings.Contains(w.Body.String(), "ssh-add") {
		t.Fatalf("after a failed login lite-ui must stop trying and say what to do: %d calls, %s", len(r.Calls), w.Body.String())
	}
}

func TestWorkingConnectionIsUsedInParallel(t *testing.T) {
	s, r := newServer("anna@203.0.113.7")
	h := s.Handler()
	for i := 0; i < 3; i++ {
		if w := do(h, "GET", "/api/status", "", tok); w.Code != 200 || strings.Contains(w.Body.String(), "ssh_failed") {
			t.Fatal(w.Body.String())
		}
	}
	if len(r.Calls) != 3 {
		t.Fatalf("working requests must all go through: %d", len(r.Calls))
	}
}

// fakeStarter plays the server side of `lite-deploy call-stream` for a test.
type fakeStarter struct {
	mu     sync.Mutex
	starts int
	mode   string // "ok", "old" (the command does not exist), "denied" (login fails)
}

func (f *fakeStarter) Start(_ context.Context, _ string, _ []string) (*host.Conn, error) {
	f.mu.Lock()
	f.starts++
	f.mu.Unlock()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var conn *host.Conn
	stderr := ""
	switch f.mode {
	case "denied":
		stderr = "serafim@203.0.113.7: Permission denied (publickey)."
	case "old":
		stderr = "lite-deploy: unknown command: call-stream"
	}
	conn = host.NewConn(inW, outR, func() string { return stderr }, func() { _ = outW.Close() })
	go func() {
		if f.mode != "ok" {
			_ = outW.Close()
			conn.Finish()
			return
		}
		_, _ = io.WriteString(outW, `{"stream":"lite-deploy","version":"test"}`+"\n")
		sc := bufio.NewScanner(inR)
		for sc.Scan() {
			var req struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}
			_ = json.Unmarshal(sc.Bytes(), &req)
			resp, _ := json.Marshal(map[string]any{"id": req.ID, "response": map[string]any{"ok": true, "command": "x", "data": map[string]string{"path": req.Path}}})
			_, _ = io.WriteString(outW, string(resp)+"\n")
		}
		_ = outW.Close()
		conn.Finish()
	}()
	return conn, nil
}

func TestOneConnectionCarriesAllRequests(t *testing.T) {
	s, r := newServer("anna@203.0.113.7")
	f := &fakeStarter{mode: "ok"}
	s.Streamer = f
	h := s.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := []string{"/api/status", "/api/verify", "/api/plugins", "/api/firewall"}[i%4]
			if w := do(h, "GET", path, "", tok); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
				t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
	if f.starts != 1 {
		t.Fatalf("all requests must share one connection, started %d", f.starts)
	}
	if len(r.Calls) != 0 {
		t.Fatalf("no per-request ssh may run: %v", r.Calls)
	}
}

func TestOlderServerFallsBackToOneSSHPerRequest(t *testing.T) {
	s, r := newServer("anna@203.0.113.7")
	f := &fakeStarter{mode: "old"}
	s.Streamer = f
	h := s.Handler()
	for i := 0; i < 3; i++ {
		if w := do(h, "GET", "/api/status", "", tok); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if f.starts != 1 || len(r.Calls) != 3 {
		t.Fatalf("the stream is tried once, then every request uses its own ssh: starts %d, calls %d", f.starts, len(r.Calls))
	}
}

func TestFailedLoginOnTheStreamStopsAllAttempts(t *testing.T) {
	s, r := newServer("anna@203.0.113.7")
	f := &fakeStarter{mode: "denied"}
	s.Streamer = f
	h := s.Handler()
	for i := 0; i < 5; i++ {
		if w := do(h, "GET", "/api/status", "", tok); !strings.Contains(w.Body.String(), "ssh_failed") {
			t.Fatalf("%s", w.Body.String())
		}
	}
	if f.starts != 1 || len(r.Calls) != 0 {
		t.Fatalf("one failed login must stop all attempts: starts %d, ssh calls %d", f.starts, len(r.Calls))
	}
}

func TestPostsAreSignedWhenTheServerAsksForTheOwnerKey(t *testing.T) {
	dir := t.TempDir()
	var calls [][]string
	var pubID string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Args)
		path := ""
		for i, a := range c.Args {
			if a == "call" && i+2 < len(c.Args) {
				path = c.Args[i+2]
			}
		}
		switch path {
		case "/v1/owner":
			return host.Result{Stdout: []byte(`{"ok":true,"data":{"required":true,"keys":[{"id":"` + pubID + `"}]}}`)}, nil
		case "/v1/project/key":
			return host.Result{Stdout: []byte(`{"ok":true,"data":{"public_key":"age1server"}}`)}, nil
		}
		return host.Result{Stdout: []byte(`{"ok":true,"data":{}}`)}, nil
	}}
	s := &Server{Runner: r, Host: "anna@203.0.113.7", Token: tok, OwnerKeyPath: dir + "/owner.key"}
	priv, err := s.createOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	pubID = ownerKeyID(priv)
	h := s.Handler()
	if w := do(h, "POST", "/api/deploy", `{"project":"shop","sha":"`+sha+`"}`, tok); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	last := calls[len(calls)-1]
	hasSig := false
	for _, a := range last {
		hasSig = hasSig || a == "--sig"
	}
	if !hasSig {
		t.Fatalf("a changing request must be signed when the server asks for the owner key: %v", last)
	}
	// reading is never signed
	calls = nil
	s.forgetOwner("anna@203.0.113.7")
	do(h, "GET", "/api/status", "", tok)
	for _, c := range calls {
		for _, a := range c {
			if a == "--sig" {
				t.Fatalf("a read must not be signed: %v", c)
			}
		}
	}
}

func TestBackupKeyIsMadeForThePageAndNeverKept(t *testing.T) {
	s, r := newServer("anna@203.0.113.7")
	h := s.Handler()
	w := do(h, "POST", "/api/backup/keygen", `{}`, tok)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"public":"age1`) || !strings.Contains(w.Body.String(), `"private":"AGE-SECRET-KEY-1`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(r.Calls) != 0 {
		t.Fatalf("the key must not be sent to the server by this call: %v", r.Calls)
	}
	if w2 := do(h, "POST", "/api/backup/keygen", `{}`, tok); w2.Body.String() == w.Body.String() {
		t.Error("every call must make a new key")
	}
}

func TestMCPToolsReadAndDeployButNeverSign(t *testing.T) {
	dir := t.TempDir()
	var calls [][]string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Args)
		return host.Result{Stdout: []byte(`{"ok":true,"data":{"projects":[]}}`)}, nil
	}}
	s := &Server{Runner: r, Servers: &ServerList{Path: dir + "/servers"}, Token: tok, OwnerKeyPath: dir + "/owner.key"}
	_ = s.Servers.Add("anna@203.0.113.7")
	if _, err := s.createOwnerKey(); err != nil {
		t.Fatal(err)
	}
	var byName = map[string]func(context.Context, map[string]any) (string, error){}
	for _, tl := range s.MCPTools() {
		byName[tl.Name] = tl.Run
	}
	if out, err := byName["project_status"](context.Background(), map[string]any{}); err != nil || !strings.Contains(out, "projects") {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := byName["deploy"](context.Background(), map[string]any{"project": "shop", "sha": "short"}); err == nil {
		t.Error("a deploy needs the full 40-character hash")
	}
	if _, err := byName["deploy"](context.Background(), map[string]any{"project": "shop", "sha": sha}); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		for _, a := range c {
			if a == "--sig" {
				t.Fatalf("a tool of an assistant must never sign with the owner's key: %v", c)
			}
		}
	}
	if _, err := byName["verify"](context.Background(), map[string]any{"server": "evil@198.51.100.9"}); err == nil {
		t.Error("a server that is not in the list must be refused")
	}
}

// The Claude Desktop extension lists the tools: it must list the ones lite-ui really has.
func TestExtensionManifestMatchesTheTools(t *testing.T) {
	raw, err := os.ReadFile("../../packaging/mcpb/manifest.json.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.NewReplacer("@VERSION@", "1.2.3", "@BINARY@", "lite-ui.exe", "@PLATFORM@", "win32").Replace(string(raw))
	var m struct {
		ManifestVersion string `json:"manifest_version"`
		Name            string `json:"name"`
		Version         string `json:"version"`
		Server          struct {
			Type       string `json:"type"`
			EntryPoint string `json:"entry_point"`
			MCP        struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcp_config"`
		} `json:"server"`
		Tools      []struct{ Name string } `json:"tools"`
		UserConfig map[string]struct {
			Required bool `json:"required"`
		} `json:"user_config"`
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("the manifest is not valid JSON: %v", err)
	}
	if m.ManifestVersion == "" || m.Name != "lite-deploy" || m.Version != "1.2.3" || m.Server.Type != "node" || m.Server.EntryPoint != "server/index.js" || m.Server.MCP.Command != "node" {
		t.Fatalf("the manifest is incomplete: %+v", m)
	}
	if len(m.Server.MCP.Args) != 2 || !strings.HasSuffix(m.Server.MCP.Args[0], "server/index.js") || m.Server.MCP.Args[1] != "${user_config.server}" || m.UserConfig["server"].Required {
		t.Errorf("the launcher must get the address the person typed: %+v", m.Server.MCP)
	}
	have := map[string]bool{}
	for _, tl := range (&Server{}).MCPTools() {
		have[tl.Name] = true
	}
	for _, tl := range m.Tools {
		if !have[tl.Name] {
			t.Errorf("the manifest lists a tool that lite-ui does not have: %s", tl.Name)
		}
		delete(have, tl.Name)
	}
	for name := range have {
		t.Errorf("lite-ui has a tool the manifest does not list: %s", name)
	}
}

func TestSecretValueIsEncryptedBeforeItLeavesThisComputer(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	var sent []string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		path := c.Args[len(c.Args)-1]
		if path == "/v1/project/key" {
			return host.Result{Stdout: []byte(`{"ok":true,"data":{"public_key":"` + id.Recipient().String() + `"}}`)}, nil
		}
		sent = append(sent, string(c.Stdin))
		return host.Result{Stdout: []byte(`{"ok":true,"data":{}}`)}, nil
	}}
	s := &Server{Runner: r, Host: "anna@203.0.113.7", Token: tok}
	h := s.Handler()
	w := do(h, "POST", "/api/secret/set", `{"project":"shop","name":"api_key","value":"s3cr3t-value-123"}`, tok)
	if w.Code != 200 || len(sent) != 1 {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), sent)
	}
	if strings.Contains(sent[0], "s3cr3t-value-123") || !strings.Contains(sent[0], "BEGIN AGE ENCRYPTED FILE") {
		t.Fatalf("the value must travel encrypted: %s", sent[0])
	}
	if w := do(h, "POST", "/api/secret/set", `{"project":"shop","name":"_db_password","value":"x"}`, tok); w.Code != 400 {
		t.Errorf("an internal name must be refused: %d", w.Code)
	}
	if w := do(h, "GET", "/api/secrets?project=Shop%20x", "", tok); w.Code != 400 {
		t.Errorf("a bad project name must be refused: %d", w.Code)
	}
}

type fakeUIUpdater struct{ applied bool }

func (f *fakeUIUpdater) Check(context.Context) (uiupdate.Info, error) {
	return uiupdate.Info{Current: "v0.1.0", Latest: "v0.2.0", Available: true}, nil
}
func (f *fakeUIUpdater) Apply(context.Context) (string, error) {
	f.applied = true
	return "v0.2.0", nil
}

func TestUIUpdateEndpoints(t *testing.T) {
	s, _ := newServer("")
	f := &fakeUIUpdater{}
	done := make(chan struct{})
	s.Updater, s.OnUpdated = f, func() { close(done) }
	h := s.Handler()
	if w := do(h, "GET", "/api/ui-update", "", ""); w.Code != 401 {
		t.Fatalf("a token is required: %d", w.Code)
	}
	w := do(h, "GET", "/api/ui-update", "", s.Token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatalf("check: %d %s", w.Code, w.Body)
	}
	w = do(h, "POST", "/api/ui-update/apply", "{}", s.Token)
	if w.Code != 200 || !f.applied {
		t.Fatalf("apply: %d %s", w.Code, w.Body)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the program was not asked to restart")
	}
}

func TestCloudflareAccessGoesToCloudflareNotToTheServer(t *testing.T) {
	var got []string
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(b))
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/access/apps"):
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case r.URL.Path == "/accounts":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"0123456789abcdef0123456789abcdef","name":"Acme"}]}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb","name":"n","domain":"shop.example.com/admin*"}}`))
		}
	}))
	defer cf.Close()
	s, r := newServer("")
	s.CFBase = cf.URL
	s.CF = &cfaccess.Store{Path: filepath.Join(t.TempDir(), "cf.json")}
	h := s.Handler()
	body := `{"action":"protect","token":"secret-token-value","remember":true,"host":"shop.example.com","path":"/admin*","who":["me@example.com"]}`
	w := do(h, "POST", "/api/cf-access", body, s.Token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("protect: %d %s", w.Code, w.Body)
	}
	if len(got) < 2 || !strings.Contains(got[len(got)-1], "Bearer secret-token-value") || !strings.Contains(got[len(got)-1], "shop.example.com/admin*") {
		t.Fatalf("Cloudflare did not get the request: %v", got)
	}
	if len(r.Calls) != 0 {
		t.Errorf("the token must never go to the server: %v", r.Calls)
	}
	if v, ok := s.CF.Get("default"); !ok || v.Token != "secret-token-value" {
		t.Errorf("the token was asked to be remembered: %+v", v)
	}
	// the saved token is used when the form sends none, and the labels never reveal it
	w = do(h, "POST", "/api/cf-access", `{"action":"labels"}`, s.Token)
	if strings.Contains(w.Body.String(), "secret-token-value") || !strings.Contains(w.Body.String(), "default") {
		t.Errorf("labels: %s", w.Body)
	}
	if w := do(h, "POST", "/api/cf-access", `{"action":"list"}`, s.Token); w.Code != 200 {
		t.Errorf("a saved token must be enough: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/cf-access", `{"action":"protect","token":"x","host":"bad host","who":["me@example.com"]}`, s.Token); w.Code != 400 {
		t.Errorf("a bad domain must be refused: %d", w.Code)
	}
	if w := do(h, "POST", "/api/cf-access", `{"action":"list"}`, ""); w.Code != 401 {
		t.Errorf("a token is required: %d", w.Code)
	}
}

// The assistant can do what the page can, but it asks first and never signs.
func TestMCPToolsMatchThePage(t *testing.T) {
	dir := t.TempDir()
	var calls [][]string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Args)
		return host.Result{Stdout: []byte(`{"ok":true,"data":{"done":true}}`)}, nil
	}}
	s := &Server{Runner: r, Servers: &ServerList{Path: dir + "/servers"}, Token: tok, OwnerKeyPath: dir + "/owner.key"}
	_ = s.Servers.Add("anna@203.0.113.7")
	if _, err := s.createOwnerKey(); err != nil {
		t.Fatal(err)
	}
	by := map[string]mcpTool{}
	readOnly := 0
	for _, tl := range s.MCPTools() {
		by[tl.Name] = mcpTool{tl.Run, tl.ReadOnly}
		if tl.ReadOnly {
			readOnly++
		}
	}
	for _, want := range []string{"firewall_status", "secrets_list", "project_export", "server_config", "update_server", "fix", "run_plugin", "publish",
		"fallback_password", "project_preview", "project_create", "secret_set", "secret_remove", "connect_cloudflare_tunnel", "cloudflare_tunnel", "cloudflare_access", "add_server", "remove_server"} {
		if _, ok := by[want]; !ok {
			t.Errorf("the page can do it, the assistant cannot: %s", want)
		}
	}
	for _, ro := range []string{"verify", "project_status", "firewall_status", "secrets_list"} {
		if !by[ro].readOnly {
			t.Errorf("%s only reads and must say so", ro)
		}
	}
	for _, ch := range []string{"deploy", "rollback", "run_plugin", "publish", "secret_set", "update_server"} {
		if by[ch].readOnly {
			t.Errorf("%s changes things and must not say it is read only", ch)
		}
	}
	ctx := context.Background()
	if out, err := by["run_plugin"].run(ctx, map[string]any{"plugin": "firewall", "command": "mode", "args": []any{"http"}}); err != nil || !strings.Contains(out, "done") {
		t.Fatalf("run_plugin: %v %s", err, out)
	}
	if _, err := by["run_plugin"].run(ctx, map[string]any{"plugin": "Bad Name", "command": "x"}); err == nil {
		t.Error("the checks of the page apply to the assistant too")
	}
	if _, err := by["publish"].run(ctx, map[string]any{"project": "shop", "site": "main", "mode": "sideways"}); err == nil {
		t.Error("a wrong mode must be refused")
	}
	if _, err := by["secret_set"].run(ctx, map[string]any{"project": "shop", "name": "api_key", "value": "a-secret-value"}); err != nil {
		t.Fatalf("secret_set: %v", err)
	}
	if out, err := by["add_server"].run(ctx, map[string]any{"server": "bob@203.0.113.9"}); err != nil || !s.Servers.Has("bob@203.0.113.9") {
		t.Fatalf("add_server: %v %s", err, out)
	}
	if _, err := by["secrets_list"].run(ctx, map[string]any{"project": "../x"}); err == nil {
		t.Error("an invalid project name must be refused")
	}
	for _, c := range calls {
		for _, a := range c {
			if a == "--sig" {
				t.Fatalf("an assistant must never sign: %v", c)
			}
		}
	}
}

type mcpTool struct {
	run      func(context.Context, map[string]any) (string, error)
	readOnly bool
}

func TestCloudflareTunnelFromThePage(t *testing.T) {
	zone, tid := "fedcba9876543210fedcba9876543210", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	var cfCalls []string
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cfCalls = append(cfCalls, r.Method+" "+r.URL.Path+" "+string(b))
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
			_, _ = w.Write([]byte(`{"success":true,"result":"eyJ-tunnel-token-value"}`))
		case r.Method == "GET" && (strings.HasSuffix(r.URL.Path, "/configurations") || strings.HasSuffix(r.URL.Path, "/dns_records")):
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	defer cf.Close()
	var toServer []string
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		toServer = append(toServer, strings.Join(c.Args, " ")+" | "+string(c.Stdin))
		return host.Result{Stdout: []byte(`{"ok":true,"data":{"tunnels":[]}}`)}, nil
	}}
	s := &Server{Runner: r, Servers: &ServerList{Path: filepath.Join(t.TempDir(), "servers")}, Token: tok}
	_ = s.Servers.Add("anna@203.0.113.7")
	s.CFBase = cf.URL
	s.CF = &cfaccess.Store{Path: filepath.Join(t.TempDir(), "cf.json")}
	h := s.Handler()
	body := `{"label":"acme","token":"cf-api-token-value","host":"app.example.com","port":18080,"project":"shop","site":"main"}`
	w := doServer(h, "POST", "/api/cf-tunnel", body, "anna@203.0.113.7")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), "routed to port 18080") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	all := strings.Join(cfCalls, "\n")
	for _, want := range []string{"POST /accounts/0123456789abcdef0123456789abcdef/cfd_tunnel", `"hostname":"app.example.com","service":"http://127.0.0.1:18080"`, "cfargotunnel.com"} {
		if !strings.Contains(all, want) {
			t.Errorf("Cloudflare did not get %q:\n%s", want, all)
		}
	}
	server := strings.Join(toServer, "\n")
	if strings.Contains(server, "cf-api-token-value") {
		t.Error("the Cloudflare API token must never go to the server")
	}
	if !strings.Contains(server, "eyJ-tunnel-token-value") {
		t.Errorf("the token of the tunnel must reach the server:\n%s", server)
	}
	if w := do(h, "POST", "/api/cf-tunnel", `{"token":"x","host":"app.example.com","port":80}`, s.Token); w.Code != 400 {
		t.Errorf("the port must be one of the sites: %d", w.Code)
	}
}
