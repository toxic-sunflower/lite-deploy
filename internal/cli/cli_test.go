package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

const projectFile = "../../testdata/fanvue-games/project.json"

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func testEnv(t *testing.T) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	m := host.NewMemFS()
	m.Files["/etc/machine-id"] = []byte("test-machine\n")
	m.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\n")
	var out, errb bytes.Buffer
	return Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, FS: m, Paths: plan.DefaultPaths(), Now: func() time.Time { return now }, IsRoot: func() bool { return true },
		Runner: &host.FakeRunner{Handler: func(host.Cmd) (host.Result, error) {
			return host.Result{ExitCode: 127, Stderr: []byte("not found")}, nil
		}},
		HTTP:        &host.FakeHTTP{Handler: func(string, string) (host.HTTPResponse, error) { return host.HTTPResponse{}, os.ErrNotExist }},
		Interactive: func() bool { return false }}, &out, &errb
}

func run(t *testing.T, env Env, args ...string) int { t.Helper(); return RunEnv(args, env) }

// decode разбирает ответ --json строго и проверяет по схеме конверта.
func decode(t *testing.T, out *bytes.Buffer) Envelope {
	t.Helper()
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("ответ --json должен быть одной строкой: %q", out.String())
	}
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	dec.DisallowUnknownFields()
	var e Envelope
	if err := dec.Decode(&e); err != nil {
		t.Fatal(err)
	}
	if is := schema.Check(&e); len(is) > 0 {
		t.Fatalf("конверт не прошёл проверку: %v", is)
	}
	return e
}

func ciphertextArgs(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	var args []string
	for _, n := range []string{"ghcr_read_token", "auth_session_secret", "fanvue_webhook_secret", "admin_username", "admin_password", "_db_password"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("-----BEGIN AGE ENCRYPTED FILE-----\nQUJDREVGR0hJSktMTU5PUFFSU1RVVldY\n-----END AGE ENCRYPTED FILE-----\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--ciphertext", n+"="+p)
	}
	return args
}

func TestVersion(t *testing.T) {
	env, out, _ := testEnv(t)
	if c := run(t, env, "version"); c != 0 || !strings.Contains(out.String(), Version) {
		t.Fatal(c, out.String())
	}
	env, out, _ = testEnv(t)
	run(t, env, "version", "--json")
	e := decode(t, out)
	if !e.OK || e.Command != "version" || e.APIVersion != "v1" {
		t.Fatalf("%+v", e)
	}
}

func TestValidateOK(t *testing.T) {
	env, out, _ := testEnv(t)
	if c := run(t, env, "project", "validate", projectFile, "--json"); c != 0 {
		t.Fatal(c, out.String())
	}
	if e := decode(t, out); !e.OK || e.Command != "project.validate" {
		t.Fatalf("%+v", e)
	}
}

func TestValidateBad(t *testing.T) {
	cases := map[string]string{
		"поле не проходит": `{"schema":"lite-deploy/project/v1","name":"Bad","services":{"a":{"image":"x/y","container_port":1}},"sites":[{"name":"m","host":"a.example.com","service":"a"}]}`,
		"неизвестное поле": `{"evil":true}`,
		"не JSON":          `name: x`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			env, out, _ := testEnv(t)
			env.Stdin = strings.NewReader(in)
			c := run(t, env, "project", "validate", "-", "--json")
			e := decode(t, out)
			if c != 2 || e.OK || e.Error == nil || e.Error.Code != "invalid_project" || len(e.Error.Details) == 0 {
				t.Fatalf("code=%d %+v", c, e)
			}
		})
	}
}

func TestPlanThenApplyDryRun(t *testing.T) {
	env, out, _ := testEnv(t)
	planFile := filepath.Join(t.TempDir(), "plan.json")
	args := append([]string{"project", "plan", projectFile, "--out", planFile, "--json"}, ciphertextArgs(t)...)
	if c := RunEnv(args, env); c != 0 {
		t.Fatalf("plan: %d %s", c, out.String())
	}
	e := decode(t, out)
	if !e.OK || e.Command != "project.plan" {
		t.Fatalf("%+v", e)
	}
	raw, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("secret://")) {
		t.Error("в плане остались ссылки secret://")
	}

	env, out, _ = testEnv(t)
	if c := run(t, env, "apply", planFile, "--dry-run", "--json"); c != 0 {
		t.Fatalf("apply --dry-run: %d %s", c, out.String())
	}
	e = decode(t, out)
	data := e.Data.(map[string]any)
	if data["dry_run"] != true || len(data["steps"].([]any)) == 0 || data["plan_hash"] == "" {
		t.Fatalf("%v", data)
	}
	// на «сервере» нет ни podman, ни caddy: dry-run сообщает would_fail и не меняет систему
	first := data["steps"].([]any)[0].(map[string]any)
	if first["state"] != "would_fail" || len(e.Warnings) == 0 || e.Warnings[0].Code != "would_fail" {
		t.Fatalf("verify_host должен дать would_fail: %v %v", first, e.Warnings)
	}

	// без --yes и без терминала применение не начинается
	env, out, _ = testEnv(t)
	if c := run(t, env, "apply", planFile, "--json"); c != 4 || decode(t, out).Error.Code != "confirmation_required" {
		t.Fatalf("apply без --yes: %d %s", c, out.String())
	}
	// не root
	env, out, _ = testEnv(t)
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "apply", planFile, "--dry-run", "--json"); c != 4 || decode(t, out).Error.Code != "permission_denied" {
		t.Fatalf("не root: %d %s", c, out.String())
	}
	// первая же ошибка шага: step_failed, остальные пропущены
	env, out, _ = testEnv(t)
	if c := run(t, env, "apply", planFile, "--yes", "--json"); c != 5 {
		t.Fatalf("apply --yes: %d %s", c, out.String())
	} else if e := decode(t, out); e.Error.Code != "step_failed" || len(e.Error.Details) != 1 {
		t.Fatalf("%+v", e.Error)
	}

	// план для другого сервера
	env, out, _ = testEnv(t)
	env.FS.(*host.MemFS).Files["/etc/machine-id"] = []byte("other-machine")
	if c := run(t, env, "apply", planFile, "--dry-run", "--json"); c != 6 || decode(t, out).Error.Code != "plan_wrong_server" {
		t.Fatalf("другой сервер: %d %s", c, out.String())
	}

	// просроченный план
	env, out, _ = testEnv(t)
	env.Now = func() time.Time { return now.Add(48 * time.Hour) }
	if c := run(t, env, "apply", planFile, "--dry-run", "--json"); c != 6 || decode(t, out).Error.Code != "plan_expired" {
		t.Fatalf("просрочен: %d %s", c, out.String())
	}

	// испорченный план
	tampered := strings.Replace(string(raw), `"project": "fanvue-games"`, `"project": "other"`, 1)
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte(tampered), 0o600)
	env, out, _ = testEnv(t)
	if c := run(t, env, "apply", bad, "--dry-run", "--json"); c != 6 || decode(t, out).Error.Code != "plan_hash_mismatch" {
		t.Fatalf("испорчен: %d %s", c, out.String())
	}
}

func TestPlanErrors(t *testing.T) {
	env, out, _ := testEnv(t)
	if c := run(t, env, "project", "plan", projectFile, "--json"); c != 3 || decode(t, out).Error.Code != "secret_missing" {
		t.Fatalf("без секретов: %d %s", c, out.String())
	}
	env, out, _ = testEnv(t)
	env.FS = host.NewMemFS() // нет machine-id
	args := append([]string{"project", "plan", projectFile, "--json"}, ciphertextArgs(t)...)
	if c := RunEnv(args, env); c != 3 || decode(t, out).Error.Code != "precondition_failed" {
		t.Fatalf("без machine-id: %d %s", c, out.String())
	}
	env, out, _ = testEnv(t)
	if c := run(t, env, "project", "plan", projectFile, "--ciphertext", "bad", "--json"); c != 2 || decode(t, out).Error.Code != "usage" {
		t.Fatalf("неверный --ciphertext: %d %s", c, out.String())
	}
	env, out, _ = testEnv(t)
	if c := run(t, env, "project", "plan", "/nonexistent.json", "--json"); c != 1 || decode(t, out).Error.Code != "io_error" {
		t.Fatalf("нет файла: %d %s", c, out.String())
	}
}

func TestProjectList(t *testing.T) {
	env, out, _ := testEnv(t)
	env.FS.(*host.MemFS).Files["/var/lib/lite-deploy/state/projects/shop.json"] = []byte(`{"hash":"sha256:aa","deployed":true}`)
	if c := run(t, env, "project", "list", "--json"); c != 0 {
		t.Fatal(c, out.String())
	}
	e := decode(t, out)
	items := e.Data.(map[string]any)["projects"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "shop" {
		t.Fatalf("%v", items)
	}
}

func TestSchemaCommand(t *testing.T) {
	for _, n := range SchemaNames {
		env, out, _ := testEnv(t)
		if c := run(t, env, "schema", n); c != 0 {
			t.Fatalf("%s: %d", n, c)
		}
		var m map[string]any
		if err := json.Unmarshal(out.Bytes(), &m); err != nil || m["$schema"] == nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	env, out, _ := testEnv(t)
	if c := run(t, env, "schema", "nope", "--json"); c != 2 || decode(t, out).Error.Code != "usage" {
		t.Fatal(c, out.String())
	}
}

func TestUsageAndUnimplemented(t *testing.T) {
	cases := []struct {
		args []string
		code int
		err  string
	}{
		{nil, 2, "usage"},
		{[]string{"bogus"}, 2, "usage"},
		{[]string{"project"}, 2, "usage"},
		{[]string{"project", "bogus"}, 2, "usage"},
		{[]string{"project", "validate"}, 2, "usage"},
		{[]string{"apply"}, 2, "usage"},
		{[]string{"project", "create"}, 2, "usage"},
		{[]string{"deploy", "p"}, 2, "usage"},
		{[]string{"deploy", "shop", "xyz"}, 7, "project_not_found"},
		{[]string{"rollback", "shop"}, 7, "project_not_found"},
		{[]string{"rollback"}, 2, "usage"},
		{[]string{"status", "shop"}, 7, "project_not_found"},
		{[]string{"gate"}, 2, "usage"},
	}
	for _, c := range cases {
		env, out, _ := testEnv(t)
		got := RunEnv(append(append([]string{}, c.args...), "--json"), env)
		e := decode(t, out)
		if got != c.code || e.Error == nil || e.Error.Code != c.err {
			t.Errorf("%v: код %d, ответ %+v", c.args, got, e)
		}
	}
}

// Таблица кодов выхода из SPEC-v0.md, 6.3.
func TestExitCodeTable(t *testing.T) {
	want := map[int][]string{
		1:  {"internal", "caddy_reload_failed", "io_error"},
		2:  {"invalid_project", "invalid_plan", "unknown_action", "usage", "command_not_allowed", "invalid_sha"},
		3:  {"precondition_failed", "secret_missing", "project_exists_unmanaged", "adopt_password_unknown"},
		4:  {"permission_denied", "confirmation_required"},
		5:  {"step_failed"},
		6:  {"plan_hash_mismatch", "plan_expired", "plan_wrong_server", "deploy_in_progress", "quota_exceeded"},
		7:  {"project_not_found", "secret_not_found"},
		10: {"deploy_unhealthy", "deploy_failed", "rollback_unavailable", "nothing_deployed"},
	}
	for code, names := range want {
		for _, n := range names {
			if got := ExitCode(n); got != code {
				t.Errorf("%s: код выхода %d, ожидали %d", n, got, code)
			}
		}
	}
	if ExitCode("совсем неизвестный") != 1 {
		t.Error("неизвестный код ошибки это 1")
	}
	for _, c := range ErrorCodes() {
		if ExitCode(c) == 0 {
			t.Errorf("%s: 0 зарезервирован для успеха", c)
		}
	}
}

func TestTextModeErrorsGoToStderr(t *testing.T) {
	env, out, errb := testEnv(t)
	env.Stdin = strings.NewReader(`{}`)
	if c := run(t, env, "project", "validate", "-"); c != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "invalid_project") {
		t.Fatalf("%d stdout=%q stderr=%q", c, out.String(), errb.String())
	}
}

func TestEnvelopeDoesNotLeakCiphertextToStdoutInTextMode(t *testing.T) {
	env, out, _ := testEnv(t)
	args := append([]string{"project", "plan", projectFile}, ciphertextArgs(t)...)
	if c := RunEnv(args, env); c != 0 {
		t.Fatal(c)
	}
	if strings.Contains(out.String(), "AGE ENCRYPTED") {
		t.Error("читаемый вывод плана не должен печатать шифртексты")
	}
}

func TestConfigAffectsPlan(t *testing.T) {
	env, out, _ := testEnv(t)
	env.FS.(*host.MemFS).Files["/etc/lite-deploy/config.json"] = []byte(`{"limits":{"projects":1}}`)
	env.FS.(*host.MemFS).Files["/var/lib/lite-deploy/state/projects/other.json"] = []byte(`{}`)
	args := append([]string{"project", "plan", projectFile, "--json"}, ciphertextArgs(t)...)
	if c := RunEnv(args, env); c != 6 || decode(t, out).Error.Code != "quota_exceeded" {
		t.Fatalf("лимит проектов из config.json: %d %s", c, out.String())
	}
}

func TestConfigInvalid(t *testing.T) {
	env, out, _ := testEnv(t)
	env.FS.(*host.MemFS).Files["/etc/lite-deploy/config.json"] = []byte(`{"limits":{"plan_steps":99999}}`)
	if c := run(t, env, "project", "validate", projectFile, "--json"); c != 3 || decode(t, out).Error.Code != "precondition_failed" {
		t.Fatalf("%d %s", c, out.String())
	}
}

func TestProjectBytesLimit(t *testing.T) {
	env, out, _ := testEnv(t)
	env.FS.(*host.MemFS).Files["/etc/lite-deploy/config.json"] = []byte(`{"limits":{"project_bytes":1024}}`)
	if c := run(t, env, "project", "validate", projectFile, "--json"); c != 2 || decode(t, out).Error.Details[0].Code != "too_large" {
		t.Fatalf("%d %s", c, out.String())
	}
}

func TestGate(t *testing.T) {
	env, out, _ := testEnv(t)
	env.Getenv = func(k string) string {
		if k == "SSH_ORIGINAL_COMMAND" {
			return "rm -rf /"
		}
		return ""
	}
	if c := run(t, env, "gate", "--project", "shop", "--json"); c != 2 {
		t.Fatalf("код %d", c)
	}
	if e := decode(t, out); e.Error == nil || e.Error.Code != "command_not_allowed" {
		t.Fatalf("%+v", e)
	}

	// формат ответа задаёт команда ключа: `status --json` даёт JSON без аргумента --json у gate
	env, out, _ = testEnv(t)
	env.Getenv = func(string) string { return "status --json" }
	if c := run(t, env, "gate", "--project", "shop"); c != 7 {
		t.Fatalf("код %d", c)
	}
	if e := decode(t, out); e.Error == nil || e.Error.Code != "project_not_found" {
		t.Fatalf("%+v", e)
	}
}
