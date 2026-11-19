package cli

import (
	"context"
	"errors"
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
	// без --yes и терминала применение не идёт
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
	// пользователь проекта (CI через привратник) выкатывает сам, агент не нужен
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
		env.LoginUID = func() (int, bool) { return 0, true } // вход под root: остальные команды запрещены
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
