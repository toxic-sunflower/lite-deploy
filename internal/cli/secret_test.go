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

func initKeys(t *testing.T, env Env) string {
	t.Helper()
	var sink strings.Builder
	e := env
	e.Stdout = &sink
	if c := RunEnv([]string{"init", "--json"}, e); c != 0 {
		t.Fatalf("init: %d %s", c, sink.String())
	}
	return string(memfs(env).Files[secrets.DefaultKeyPath])
}

func TestInit(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "init", "--json"); c != 4 || decode(t, out).Error.Code != "permission_denied" {
		t.Fatalf("не root: %d %s", c, out.String())
	}
	if len(memfs(env).Files) != 2 { // только machine-id и passwd
		t.Error("без root ничего не должно создаваться")
	}

	env, out, _ = testEnv(t)
	if c := run(t, env, "init", "--json"); c != 0 {
		t.Fatal(c, out.String())
	}
	e := decode(t, out)
	d := e.Data.(map[string]any)
	if d["created"] != true || !strings.HasPrefix(d["public_key"].(string), "age1") {
		t.Fatalf("%v", d)
	}
	m := memfs(env)
	if m.Perms[secrets.DefaultKeyPath] != 0o600 || m.Perms[secrets.DefaultPubPath] != 0o644 || m.Dirs[secrets.DefaultStoreDir] != 0o700 {
		t.Errorf("права: %v %v", m.Perms, m.Dirs)
	}
	if strings.Contains(out.String(), "AGE-SECRET-KEY") {
		t.Error("закрытый ключ не должен печататься")
	}
	key := string(m.Files[secrets.DefaultKeyPath])
	out.Reset()
	if c := run(t, env, "init", "--json"); c != 0 || decode(t, out).Data.(map[string]any)["created"] != false || string(m.Files[secrets.DefaultKeyPath]) != key {
		t.Fatalf("повторный init: %s", out.String())
	}
}

func TestSecretPubkey(t *testing.T) {
	env, out, _ := testEnv(t)
	if c := run(t, env, "secret", "pubkey", "--json"); c != 3 || decode(t, out).Error.Code != "precondition_failed" {
		t.Fatalf("без init: %d %s", c, out.String())
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
		t.Fatal("значение попало в вывод")
	}
	ct := decode(t, out).Data.(map[string]any)["ciphertext"].(string)
	v, err := secrets.Decrypt(key, ct, "shop", "api_key")
	if err != nil || string(v) != "hunter2" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := secrets.Decrypt(key, ct, "shop", "other_name"); err == nil {
		t.Error("привязка к имени")
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
		t.Fatalf("CRLF должен отрезаться: %q %v", v, err)
	}
	cases := []struct {
		name  string
		stdin string
		args  []string
		code  int
		err   string
	}{
		{"пустое значение", "", []string{"shop", "api_key", "--pubkey", pub}, 2, "invalid_secret"},
		{"плохое имя", "v", []string{"shop", "Bad", "--pubkey", pub}, 2, "invalid_secret"},
		{"плохой проект", "v", []string{"../x", "api_key", "--pubkey", pub}, 2, "invalid_secret"},
		{"плохой ключ", "v", []string{"shop", "api_key", "--pubkey", "age1zzz"}, 2, "invalid_secret"},
		{"слишком длинное", strings.Repeat("x", 9000), []string{"shop", "api_key", "--pubkey", pub}, 2, "invalid_secret"},
		{"нет ключа", "v", []string{"shop", "api_key"}, 3, "precondition_failed"},
		{"не хватает аргументов", "v", []string{"shop"}, 2, "usage"},
		{"значение в аргументах", "v", []string{"shop", "api_key", "hunter2"}, 2, "usage"},
	}
	for _, c := range cases {
		env, out, _ := testEnv(t)
		env.Stdin = strings.NewReader(c.stdin)
		got := RunEnv(append(append([]string{"secret", "encrypt"}, c.args...), "--json"), env)
		e := decode(t, out)
		if got != c.code || e.Error == nil || e.Error.Code != c.err {
			t.Errorf("%s: код %d, %+v", c.name, got, e.Error)
		}
	}
}

func TestSecretSetAndList(t *testing.T) {
	env, out, _ := testEnv(t)
	env.Stdin = strings.NewReader("topsecret\n")
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "secret", "set", "shop", "api_key", "--json"); c != 4 || decode(t, out).Error.Code != "permission_denied" {
		t.Fatalf("не root: %d %s", c, out.String())
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
		t.Errorf("хранилище: %q %v", m.Files[p], m.Perms[p])
	}
	out.Reset()
	env.Stdin = strings.NewReader("topsecret\n")
	run(t, env, "secret", "set", "shop", "api_key", "--json")
	if decode(t, out).Data.(map[string]any)["changed"] != false {
		t.Error("то же значение: changed=false")
	}

	out.Reset()
	if c := run(t, env, "secret", "list", "shop", "--json"); c != 0 {
		t.Fatal(c, out.String())
	}
	if strings.Contains(out.String(), "topsecret") {
		t.Fatal("list не должен показывать значения")
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
	for _, n := range []string{"ghcr_read_token", "auth_session_secret", "fanvue_webhook_secret", "admin_username", "admin_password", "_db_password"} {
		values[n] = "value-of-" + n
		e := env
		e.Stdin = strings.NewReader(values[n])
		var o strings.Builder
		e.Stdout = &o
		if c := RunEnv([]string{"secret", "encrypt", "fanvue-games", n, "--json"}, e); c != 0 {
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
			t.Fatalf("значение %s попало в план или вывод", v)
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
		t.Fatalf("шагов set_secret %d", n)
	}
}
