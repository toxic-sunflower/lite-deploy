package cli

import (
	"context"
	"errors"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestPluginStdinIsForwardedNotArgs(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	env.Stdin = strings.NewReader("S3cretS3cretS3cret\n")
	var body string
	env.AgentCall = func(_ context.Context, _, _, _ string, b []byte) ([]byte, error) {
		body = string(b)
		return []byte(`{"ok":true,"api_version":"v1","command":"plugin.run","data":{"output":"done"}}`), nil
	}
	if c := run(t, env, "pg", "password", "shop", "--password-stdin", "--yes"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	if !strings.Contains(body, `"stdin":"S3cretS3cretS3cret\n"`) || strings.Contains(strings.SplitN(body, `"stdin"`, 2)[0], "S3cret") {
		t.Fatalf("the password must go through stdin, not the arguments: %s", body)
	}
	if !strings.Contains(out.String(), "done") {
		t.Fatalf("output: %q", out.String())
	}
}

func TestProjectCreateWithoutRootGoesThroughAgent(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	ct := "/tmp/ct"
	var paths []string
	var createBody string
	env.AgentCall = func(_ context.Context, _, _, path string, b []byte) ([]byte, error) {
		paths = append(paths, path)
		if strings.HasSuffix(path, "preview") {
			return []byte(`{"ok":true,"api_version":"v1","command":"project.bundle","data":{"project_hash":"sha256:abc","steps":[{"id":"s01","action":"verify_host","state":"would_change"}]}}`), nil
		}
		createBody = string(b)
		return []byte(`{"ok":true,"api_version":"v1","command":"project.bundle","data":{"steps":[]}}`), nil
	}
	if err := env.FS.WriteFile(ct, []byte(ctArmor+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := run(t, env, "project", "create", projectFile, "--yes", "--ciphertext", "api_key="+ct, "--json"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	if strings.Join(paths, ",") != "/v1/project/preview,/v1/project/create" || !strings.Contains(createBody, `"expect_project_hash":"sha256:abc"`) || !strings.Contains(createBody, "BEGIN AGE ENCRYPTED FILE") {
		t.Fatalf("%v %s", paths, createBody)
	}
	// without --yes and a terminal applying does not go ahead
	paths = nil
	out.Reset()
	if c := run(t, env, "project", "create", projectFile, "--ciphertext", "api_key="+ct, "--json"); c != 4 || len(paths) != 1 {
		t.Fatalf("without --yes: %d %v", c, paths)
	}
}

func TestVerifyWithoutRootUsesAgentResults(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	env.AgentCall = func(_ context.Context, _, method, path string, _ []byte) ([]byte, error) {
		if method != "GET" || path != "/v1/verify" {
			t.Fatalf("%s %s", method, path)
		}
		return []byte(`{"ok":false,"api_version":"v1","command":"verify","data":{"findings":[{"plugin":"security","check":"fail2ban","level":"ok","message":"protects"},{"plugin":"pg","check":"x","level":"fail","message":"bad","fix":"sudo lite pg install"}],"ok":1,"warnings":0,"failures":1},"error":{"code":"verify_failed","message":"the check found errors: 1"}}`), nil
	}
	if c := run(t, env, "verify"); c != 8 {
		t.Fatalf("code %d", c)
	}
	if !strings.Contains(out.String(), "security/fail2ban: protects") || !strings.Contains(out.String(), "fix: sudo lite pg install") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestVerifyWithoutRootFallsBackWhenAgentIsMissing(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	env.AgentCall = func(context.Context, string, string, string, []byte) ([]byte, error) {
		return nil, errors.New("no agent")
	}
	run(t, env, "verify")
	if !strings.Contains(out.String(), "Total:") {
		t.Fatalf("the check must run on the spot: %s", out.String())
	}
}

func TestDeployByAdminGoesThroughAgentButProjectUserRunsLocally(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	env.CurrentUser = func() string { return "anna" }
	var path, body string
	env.AgentCall = func(_ context.Context, _, _, p string, b []byte) ([]byte, error) {
		path, body = p, string(b)
		return []byte(`{"ok":true,"api_version":"v1","command":"deploy","data":{"project":"shop","version":"` + strings.Repeat("a", 40) + `","slot":"blue","previous_slot":"none"}}`), nil
	}
	if c := run(t, env, "deploy", "shop", strings.Repeat("a", 40)); c != 0 || path != "/v1/deploy" || !strings.Contains(body, `"project":"shop"`) || !strings.Contains(out.String(), "Deployed aaaaaaaaaaaa") {
		t.Fatalf("%d %s %s %s", c, path, body, out.String())
	}
	// a project user (CI through the gatekeeper) deploys by itself, the agent is not needed
	path = ""
	env.CurrentUser = func() string { return "shop" }
	run(t, env, "deploy", "shop", strings.Repeat("a", 40), "--json")
	if path != "" {
		t.Fatalf("a project user must not go to the agent: %s", path)
	}
}

func TestHelpWorksForEveryoneIncludingRoot(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {"-h"}, {}, {"deploy", "--help"}} {
		env, out, _ := testEnv(t)
		env.IsRoot = func() bool { return true }
		env.LoginUID = func() (int, bool) { return 0, true } // root login: the other commands are forbidden
		if c := run(t, env, args...); c != 0 || !strings.Contains(out.String(), "Usage: lite") {
			t.Errorf("%v: code %d, output %q", args, c, out.String())
		}
	}
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return true }
	env.LoginUID = func() (int, bool) { return 0, true }
	if c := run(t, env, "verify"); c != 4 || !strings.Contains(out.String()+"", "") {
		t.Errorf("other commands are still forbidden for root: %d", c)
	}
}

func TestPublishSwitchesSiteThroughAgent(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	proj, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	_ = env.FS.WriteFile("/etc/lite-deploy/projects/shop.json", proj, 0o644)
	var createBody string
	env.AgentCall = func(_ context.Context, _, _, path string, b []byte) ([]byte, error) {
		if strings.HasSuffix(path, "preview") {
			return []byte(`{"ok":true,"api_version":"v1","command":"project.bundle","data":{"project_hash":"sha256:abc","steps":[]}}`), nil
		}
		createBody = string(b)
		return []byte(`{"ok":true,"api_version":"v1","command":"project.bundle","data":{"steps":[]}}`), nil
	}
	if c := run(t, env, "publish", "shop", "main"); c != 0 || !strings.Contains(out.String(), "tunnel") {
		t.Fatalf("showing the mode: %d %s", c, out.String())
	}
	out.Reset()
	if c := run(t, env, "publish", "shop", "main", "direct", "--yes"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	if !strings.Contains(createBody, `"publish":"direct"`) {
		t.Fatalf("the agent did not get publish=direct: %s", createBody)
	}
	if c := run(t, env, "publish", "shop", "nosuch", "direct", "--yes"); c == 0 {
		t.Fatal("an unknown site must fail")
	}
}

func TestVersionCompare(t *testing.T) {
	for _, c := range []struct {
		latest, cur string
		want        bool
	}{{"v0.7.8", "v0.7.7", true}, {"v0.7.7", "v0.7.7", false}, {"v0.10.0", "v0.9.9", true}, {"v0.7.7", "v0.7.8", false}, {"v0.7.7", "0.0.0-dev", false}} {
		if got := newer(c.latest, c.cur); got != c.want {
			t.Errorf("newer(%s, %s) = %v, want %v", c.latest, c.cur, got, c.want)
		}
	}
}

func TestUpdateCheckAndVerifyWarning(t *testing.T) {
	old := Version
	Version = "v0.7.7"
	defer func() { Version = old }()
	env, out, _ := testEnv(t)
	env.HTTP = &host.FakeHTTP{Handler: func(url, _ string) (host.HTTPResponse, error) {
		h := http.Header{}
		h.Set("Location", "https://github.com/toxic-sunflower/lite-deploy/releases/tag/v0.7.9")
		return host.HTTPResponse{Status: 302, Header: h}, nil
	}}
	if c := run(t, env, "update", "--check", "--json"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	if !strings.Contains(out.String(), `"available":true`) || !strings.Contains(out.String(), "v0.7.9") {
		t.Fatalf("the update must be reported: %s", out.String())
	}
	found := false
	for _, f := range versionFinding(context.Background(), env) {
		found = found || (f.Check == "version" && f.Level == plugin.LevelWarn && f.Fix == "lite update")
	}
	if !found {
		t.Error("verify must warn about a new version")
	}
}

const keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJi5Jedy5KNPnCRATLPXN1w/JstcLrxCXM8y3z6qQZ8W old@laptop"
const keyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDtqJ7zOtqQtYqOo0CpvDXNlMhV3HeJDpjrASKGLWdop new@laptop"

func TestSSHKeyReplaceNeverLocksOut(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	env.Getenv = func(k string) string {
		if k == "HOME" {
			return "/home/anna"
		}
		return ""
	}
	_ = env.FS.MkdirAll("/home/anna/.ssh", 0o700)
	_ = env.FS.WriteFile("/home/anna/.ssh/authorized_keys", []byte(keyA+"\n"), 0o600)
	// the only key cannot be removed
	if c := run(t, env, "ssh-key", "remove", fingerprint(keyA)); c == 0 {
		t.Fatal("removing the only key must be refused")
	}
	if c := run(t, env, "ssh-key", "add", keyB); c != 0 {
		t.Fatalf("add: %d %s", c, out.String())
	}
	b, _ := env.FS.ReadFile("/home/anna/.ssh/authorized_keys")
	if !strings.Contains(string(b), "old@laptop") || !strings.Contains(string(b), "new@laptop") {
		t.Fatalf("both keys must be there after add: %s", b)
	}
	if c := run(t, env, "ssh-key", "remove", fingerprint(keyA)); c != 0 {
		t.Fatalf("remove: %d", c)
	}
	b, _ = env.FS.ReadFile("/home/anna/.ssh/authorized_keys")
	if strings.Contains(string(b), "old@laptop") || !strings.Contains(string(b), "new@laptop") {
		t.Fatalf("only the new key must stay: %s", b)
	}
	if c := run(t, env, "ssh-key", "add", "not a key"); c == 0 {
		t.Error("an invalid key must be refused")
	}
}

func TestMigrateDoesNothingWhereProtectionIsOff(t *testing.T) {
	env, out, _ := testEnv(t)
	if c := run(t, env, "migrate"); c != 0 || !strings.Contains(out.String(), "Nothing to bring up to date") {
		t.Fatalf("%d %s", c, out.String())
	}
	env2, _, _ := testEnv(t)
	env2.IsRoot = func() bool { return false }
	if c := run(t, env2, "migrate"); c == 0 {
		t.Fatal("migrate must refuse to run without root")
	}
}

func TestCallStreamAnswersManyRequestsOnOneStream(t *testing.T) {
	dir := t.TempDir()
	sock := dir + "/api.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no unix sockets here")
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"api_version":"v1","command":"x","data":{"path":"` + r.URL.Path + `"}}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	env.Getenv = func(k string) string {
		if k == "LITE_DEPLOY_SOCK" {
			return sock
		}
		return ""
	}
	env.Stdin = strings.NewReader(`{"id":1,"method":"GET","path":"/v1/status"}` + "\n" +
		`{"id":2,"method":"GET","path":"/v1/verify"}` + "\n" +
		`{"id":3,"method":"GET","path":"/etc/passwd"}` + "\n")
	if c := run(t, env, "call-stream"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	got := out.String()
	for _, want := range []string{`"stream":"lite-deploy"`, `"id":1,"response":{"ok":true`, `/v1/status`, `/v1/verify`, `"error":"invalid request","id":3`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
}

func TestMigrationsRunOnceInOrder(t *testing.T) {
	old := migrations
	defer func() { migrations = old }()
	var ran []string
	migrations = []migration{
		{1, "first", func(Env) error { ran = append(ran, "first"); return nil }},
		{2, "second", func(Env) error { ran = append(ran, "second"); return nil }},
	}
	env, out, _ := testEnv(t)
	if c := run(t, env, "migrate"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	if c := run(t, env, "migrate"); c != 0 {
		t.Fatal(c)
	}
	if strings.Join(ran, ",") != "first,second" {
		t.Fatalf("each step must run once, in order: %v", ran)
	}
	if b, _ := env.FS.ReadFile(migrationState); strings.TrimSpace(string(b)) != "2" {
		t.Errorf("the last number must be kept: %q", b)
	}
}
