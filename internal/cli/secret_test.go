package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

func memfs(env Env) *host.MemFS { return env.FS.(*host.MemFS) }

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake anna@laptop"

// initArgs аргументы init для тестов: без вопросов, файервол не трогаем.
var initArgs = []string{"init", "--yes", "--ufw", "off", "--json"}

// forInit готовит окружение, в котором init проходит: запуск через sudo от anna, у неё уже есть ключ и пароль.
func forInit(env Env) Env {
	m := memfs(env)
	m.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\nanna:x:1000:1000::/home/anna:/bin/bash\n")
	m.Files["/home/anna/.ssh/authorized_keys"] = []byte(testKey + "\n")
	m.Files["/etc/ssh/sshd_config"] = []byte("Include /etc/ssh/sshd_config.d/*.conf\n")
	env.Getenv = func(k string) string {
		if k == "SUDO_USER" {
			return "anna"
		}
		return ""
	}
	env.Runner = &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		if c.Name == "passwd" {
			return host.Result{Stdout: []byte("anna P 2026-01-01 0 99999 7 -1\n")}, nil
		}
		return host.Result{}, nil
	}}
	return env
}

func initKeys(t *testing.T, env Env) string {
	t.Helper()
	var sink strings.Builder
	e := forInit(env)
	e.Stdout = &sink
	if c := RunEnv(initArgs, e); c != 0 {
		t.Fatalf("init: %d %s", c, sink.String())
	}
	return string(memfs(env).Files[secrets.DefaultKeyPath])
}

func TestInit(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "init", "--json"); c != 4 || decode(t, out).Error.Code != "permission_denied" {
		t.Fatalf("not root: %d %s", c, out.String())
	}
	if len(memfs(env).Files) != 2 { // только machine-id и passwd
		t.Error("without root nothing may be created")
	}

	env, out, _ = testEnv(t)
	env = forInit(env)
	if c := run(t, env, initArgs...); c != 0 {
		t.Fatal(c, out.String())
	}
	e := decode(t, out)
	d := e.Data.(map[string]any)
	if d["created"] != true || !strings.HasPrefix(d["public_key"].(string), "age1") {
		t.Fatalf("%v", d)
	}
	m := memfs(env)
	if m.Perms[secrets.DefaultKeyPath] != 0o600 || m.Perms[secrets.DefaultPubPath] != 0o644 || m.Dirs[secrets.DefaultStoreDir] != 0o700 {
		t.Errorf("permissions: %v %v", m.Perms, m.Dirs)
	}
	if strings.Contains(out.String(), "AGE-SECRET-KEY") {
		t.Error("the private key must not be printed")
	}
	key := string(m.Files[secrets.DefaultKeyPath])
	out.Reset()
	if c := run(t, env, initArgs...); c != 0 || decode(t, out).Data.(map[string]any)["created"] != false || string(m.Files[secrets.DefaultKeyPath]) != key {
		t.Fatalf("repeated init: %s", out.String())
	}
}

func TestSecretPubkey(t *testing.T) {
	env, out, _ := testEnv(t)
	if c := run(t, env, "secret", "pubkey", "--json"); c != 3 || decode(t, out).Error.Code != "precondition_failed" {
		t.Fatalf("without init: %d %s", c, out.String())
	}
	initKeys(t, env)
	out.Reset()
	if c := run(t, env, "secret", "pubkey", "--json"); c != 0 || !strings.HasPrefix(decode(t, out).Data.(map[string]any)["public_key"].(string), "age1") {
		t.Fatalf("%s", out.String())
	}
}

func TestSecretEncryptRoundTrip(t *testing.T) {
	env, out, _ := testEnv(t)
	key := initKeys(t, env)
	env.IsRoot = func() bool { return false } // encrypt доступен любому
	env.Stdin = strings.NewReader("hunter2\n")
	if c := run(t, env, "secret", "encrypt", "shop", "api_key", "--json"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	if strings.Contains(out.String(), "hunter2") {
		t.Fatal("the value ended up in the output")
	}
	ct := decode(t, out).Data.(map[string]any)["ciphertext"].(string)
	v, err := secrets.Decrypt(key, ct, "shop", "api_key")
	if err != nil || string(v) != "hunter2" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := secrets.Decrypt(key, ct, "shop", "other_name"); err == nil {
		t.Error("binding to the name")
	}
}

func TestSecretEncryptPubkeyFlagAndErrors(t *testing.T) {
	sec, pub, _ := secrets.GenerateIdentity()
	env, out, _ := testEnv(t) // ключа на сервере нет, как на машине владельца
	env.Stdin = strings.NewReader("v\r\n")
	if c := run(t, env, "secret", "encrypt", "shop", "api_key", "--pubkey", pub); c != 0 {
		t.Fatal(c)
	}
	if v, err := secrets.Decrypt(sec, strings.TrimSpace(out.String()), "shop", "api_key"); err != nil || string(v) != "v" {
		t.Fatalf("CRLF must be trimmed: %q %v", v, err)
	}
	cases := []struct {
		name  string
		stdin string
		args  []string
		code  int
		err   string
	}{
		{"an empty value", "", []string{"shop", "api_key", "--pubkey", pub}, 2, "invalid_secret"},
		{"a bad name", "v", []string{"shop", "Bad", "--pubkey", pub}, 2, "invalid_secret"},
		{"a bad project", "v", []string{"../x", "api_key", "--pubkey", pub}, 2, "invalid_secret"},
		{"a bad key", "v", []string{"shop", "api_key", "--pubkey", "age1zzz"}, 2, "invalid_secret"},
		{"too long", strings.Repeat("x", 9000), []string{"shop", "api_key", "--pubkey", pub}, 2, "invalid_secret"},
		{"no key", "v", []string{"shop", "api_key"}, 3, "precondition_failed"},
		{"not enough arguments", "v", []string{"shop"}, 2, "usage"},
		{"the value in the arguments", "v", []string{"shop", "api_key", "hunter2"}, 2, "usage"},
	}
	for _, c := range cases {
		env, out, _ := testEnv(t)
		env.Stdin = strings.NewReader(c.stdin)
		got := RunEnv(append(append([]string{"secret", "encrypt"}, c.args...), "--json"), env)
		e := decode(t, out)
		if got != c.code || e.Error == nil || e.Error.Code != c.err {
			t.Errorf("%s: code %d, %+v", c.name, got, e.Error)
		}
	}
}

func TestSecretSetAndList(t *testing.T) {
	env, out, _ := testEnv(t)
	env.Stdin = strings.NewReader("topsecret\n")
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "secret", "set", "shop", "api_key", "--json"); c != 4 || decode(t, out).Error.Code != "permission_denied" {
		t.Fatalf("not root: %d %s", c, out.String())
	}
	env, out, _ = testEnv(t)
	env.Stdin = strings.NewReader("topsecret\n")
	if c := run(t, env, "secret", "set", "shop", "api_key", "--json"); c != 0 {
		t.Fatal(c, out.String())
	}
	if strings.Contains(out.String(), "topsecret") || decode(t, out).Data.(map[string]any)["changed"] != true {
		t.Fatalf("%s", out.String())
	}
	m := memfs(env)
	p := secrets.DefaultStoreDir + "/shop/api_key"
	if string(m.Files[p]) != "topsecret" || m.Perms[p] != 0o600 {
		t.Errorf("storage: %q %v", m.Files[p], m.Perms[p])
	}
	out.Reset()
	env.Stdin = strings.NewReader("topsecret\n")
	run(t, env, "secret", "set", "shop", "api_key", "--json")
	if decode(t, out).Data.(map[string]any)["changed"] != false {
		t.Error("the same value: changed=false")
	}

	out.Reset()
	if c := run(t, env, "secret", "list", "shop", "--json"); c != 0 {
		t.Fatal(c, out.String())
	}
	if strings.Contains(out.String(), "topsecret") {
		t.Fatal("list must not show values")
	}
	items := decode(t, out).Data.(map[string]any)["secrets"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "api_key" || items[0].(map[string]any)["updated_at"] == nil {
		t.Fatalf("%v", items)
	}

	// не root: каталога секретов не видно, имена берутся из состояния проекта
	env, out, _ = testEnv(t)
	env.IsRoot = func() bool { return false }
	memfs(env).Files["/var/lib/lite-deploy/state/projects/shop.json"] = []byte(`{"secrets":["api_key","tok"]}`)
	if c := run(t, env, "secret", "list", "shop", "--json"); c != 0 {
		t.Fatal(c, out.String())
	}
	items = decode(t, out).Data.(map[string]any)["secrets"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["updated_at"] != nil {
		t.Fatalf("%v", items)
	}
	env, out, _ = testEnv(t)
	if c := run(t, env, "secret", "list", "nope", "--json"); c != 7 || decode(t, out).Error.Code != "project_not_found" {
		t.Fatalf("%d %s", c, out.String())
	}
	env, out, _ = testEnv(t)
	if c := run(t, env, "secret", "list", "../x", "--json"); c != 2 {
		t.Fatalf("%d %s", c, out.String())
	}
}

func TestSecretUsage(t *testing.T) {
	for _, args := range [][]string{{"secret"}, {"secret", "show", "p", "n"}, {"secret", "get", "p", "n"}, {"secret", "set", "p"}, {"secret", "list"}} {
		env, out, _ := testEnv(t)
		if c := RunEnv(append(args, "--json"), env); c != 2 || decode(t, out).Error.Code != "usage" {
			t.Errorf("%v: %d %s", args, c, out.String())
		}
	}
}

// Сквозной сценарий: init, encrypt всех секретов, plan, проверка, расшифровка шага set_secret.
func TestEndToEndSecrets(t *testing.T) {
	env, out, _ := testEnv(t)
	key := initKeys(t, env)
	dir := t.TempDir()
	var args []string
	values := map[string]string{}
	for _, n := range []string{"ghcr_read_token", "auth_session_secret", "webhook_secret", "admin_username", "admin_password", "_db_password"} {
		values[n] = "value-of-" + n
		e := env
		e.Stdin = strings.NewReader(values[n])
		var o strings.Builder
		e.Stdout = &o
		if c := RunEnv([]string{"secret", "encrypt", "shop", n, "--json"}, e); c != 0 {
			t.Fatalf("%s: %d %s", n, c, o.String())
		}
		var env2 Envelope
		_ = json.Unmarshal([]byte(o.String()), &env2)
		p := filepath.Join(dir, n)
		_ = os.WriteFile(p, []byte(env2.Data.(map[string]any)["ciphertext"].(string)+"\n"), 0o600)
		args = append(args, "--ciphertext", n+"="+p)
	}
	planFile := filepath.Join(dir, "plan.json")
	out.Reset()
	if c := RunEnv(append([]string{"project", "plan", projectFile, "--out", planFile, "--json"}, args...), env); c != 0 {
		t.Fatalf("plan: %d %s", c, out.String())
	}
	raw, _ := os.ReadFile(planFile)
	for _, v := range values {
		if strings.Contains(string(raw), v) || strings.Contains(out.String(), v) {
			t.Fatalf("the value %s ended up in the plan or the output", v)
		}
	}
	out.Reset()
	if c := run(t, env, "apply", planFile, "--dry-run", "--json"); c != 0 {
		t.Fatalf("apply --dry-run: %d %s", c, out.String())
	}
	// шаги set_secret расшифровываются ключом исполнителя и привязаны к проекту и имени
	var pl struct {
		Steps []struct {
			Action string
			Params json.RawMessage
		}
	}
	_ = json.Unmarshal(raw, &pl)
	n := 0
	for _, s := range pl.Steps {
		if s.Action != "set_secret" {
			continue
		}
		v, _ := actions.Decode(s.Action, s.Params)
		ss := v.(*actions.SetSecret)
		got, err := secrets.Decrypt(key, ss.Ciphertext, ss.Project, ss.Name)
		if err != nil || string(got) != values[ss.Name] {
			t.Errorf("%s: %q %v", ss.Name, got, err)
		}
		n++
	}
	if n != 6 {
		t.Fatalf("set_secret steps %d", n)
	}
}
