package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const ctArmor = "-----BEGIN AGE ENCRYPTED FILE-----\nQUJDREVGR0hJSktMTU5PUFFSU1RVVldY\n-----END AGE ENCRYPTED FILE-----"

func bundleJSON(t *testing.T, mutate func(*bundle)) string {
	t.Helper()
	raw, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	b := bundle{Project: raw, Ciphertexts: map[string]string{}}
	for _, n := range []string{"ghcr_read_token", "auth_session_secret", "webhook_secret", "admin_username", "admin_password", "database_url"} {
		b.Ciphertexts[n] = ctArmor
	}
	if mutate != nil {
		mutate(&b)
	}
	out, _ := json.Marshal(b)
	return string(out)
}

func TestBundlePreviewReturnsHashAndSteps(t *testing.T) {
	env, out, _ := testEnv(t)
	env.Stdin = strings.NewReader(bundleJSON(t, nil))
	if c := run(t, env, "project", "bundle", "--dry-run", "--json"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	e := decode(t, out)
	data := e.Data.(map[string]any)
	if e.Command != "project.bundle" || data["dry_run"] != true || !strings.HasPrefix(data["project_hash"].(string), "sha256:") {
		t.Fatalf("%+v", data)
	}
	if len(data["steps"].([]any)) < 10 {
		t.Fatalf("too few steps: %v", data["steps"])
	}
}

func TestBundleRejectsStaleHashBadFlagsAndBadInput(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		code  string
	}{
		{"the hash is outdated", []string{"--yes"}, bundleJSON(t, func(b *bundle) { b.ExpectHash = "sha256:" + strings.Repeat("0", 64) }), "precondition_failed"},
		{"no flag", nil, bundleJSON(t, nil), "usage"},
		{"both flags", []string{"--yes", "--dry-run"}, bundleJSON(t, nil), "usage"},
		{"not JSON", []string{"--dry-run"}, "not json", "usage"},
		{"unknown field", []string{"--dry-run"}, `{"project":{},"extra":1}`, "usage"},
		{"a bad description", []string{"--dry-run"}, `{"project":{"name":"x"}}`, "invalid_project"},
		{"no secrets", []string{"--dry-run"}, bundleJSON(t, func(b *bundle) { b.Ciphertexts = map[string]string{} }), "secret_missing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, out, _ := testEnv(t)
			env.Stdin = strings.NewReader(c.stdin)
			run(t, env, append(append([]string{"project", "bundle"}, c.args...), "--json")...)
			if got := decode(t, out); got.OK || got.Error.Code != c.code {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestBundleRequiresRoot(t *testing.T) {
	env, out, _ := testEnv(t)
	env.IsRoot = func() bool { return false }
	env.Stdin = strings.NewReader(bundleJSON(t, nil))
	run(t, env, "project", "bundle", "--dry-run", "--json")
	if got := decode(t, out); got.OK || got.Error.Code != "permission_denied" {
		t.Fatalf("%+v", got)
	}
}
