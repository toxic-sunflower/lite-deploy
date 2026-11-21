package actions

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

const goodKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDtqJ7zOtqQtYqOo0CpvDXNlMhV3HeJDpjrASKGLWdop github-actions"

var goodParams = map[string]string{
	"verify_host":         `{}`,
	"create_project_user": `{"project":"shop"}`,
	"set_secret":          `{"project":"shop","name":"api_key","ciphertext":"-----BEGIN AGE ENCRYPTED FILE-----\nQUJDREVGR0hJSktMTU5PUFFSU1RVVldY\n-----END AGE ENCRYPTED FILE-----"}`,
	"registry_login":      `{"project":"shop","registry":"ghcr.io","username":"me","secret":"tok"}`,
	"write_service_env":   `{"project":"shop","service":"api","env":[{"name":"A","kind":"literal","value":"1"},{"name":"B","kind":"secret","secret":"b_secret"}]}`,
	"install_quadlet":     `{"project":"shop","service":"api","slot":"blue","host_port":18001,"container_port":8080,"memory":"512m","pids":256}`,
	"install_unit":        `{"project":"shop","service":"api","slot":"blue","host_port":18001,"exec":["./bin/app","--port","8080"],"memory":"512m","pids":256}`,
	"write_release_auth":  `{"project":"shop","service":"api","secret":"rel_token"}`,
	"reload_user_units":   `{"project":"shop"}`,
	"install_caddy_site":  `{"project":"shop","site":"main","host":"shop.example.com","listen_port":18000,"upstream_initial":"not_deployed","trusted_proxy":"none","max_body":"5MB"}`,
	"install_reload_rule": `{"project":"shop"}`,
	"reload_caddy":        `{}`,
	"probe_site":          `{"host":"shop.example.com","listen_port":18000,"expect":"not_deployed"}`,
	"install_deploy_key":  `{"project":"shop","public_key":"` + goodKey + `"}`,
	"record_project":      `{"project":"shop","project_hash":"sha256:` + strings.Repeat("a", 64) + `","ports":{"services":{"api":[18001,18002]},"sites":{"main":18000}}}`,
}

func TestEveryActionHasGoodExample(t *testing.T) {
	for _, n := range Names() {
		raw, ok := goodParams[n]
		if !ok {
			t.Errorf("no example for the action %s", n)
			continue
		}
		if _, issues := Decode(n, json.RawMessage(raw)); len(issues) > 0 {
			t.Errorf("%s: %v", n, issues)
		}
	}
	if len(Names()) != 15 {
		t.Errorf("the list has exactly 15 actions, got %d: %v", len(Names()), Names())
	}
}

func TestDangerousActionsAreUnknown(t *testing.T) {
	for _, n := range []string{"delete_project", "delete_database", "delete_site", "rotate_deploy_key", "trust_key_add", "trust_key_remove", "install_plugin", "update_agent", "run", "exec", "shell"} {
		if _, issues := Decode(n, json.RawMessage(`{}`)); len(issues) != 1 || issues[0].Code != "unknown_action" {
			t.Errorf("%s: expected unknown_action, got %v", n, issues)
		}
	}
}

func TestEmptyParamsAreAccepted(t *testing.T) {
	for _, raw := range []string{"", "{}", " {} "} {
		if _, issues := Decode("verify_host", json.RawMessage(raw)); len(issues) > 0 {
			t.Errorf("%q: %v", raw, issues)
		}
	}
}

func TestParamsRejected(t *testing.T) {
	bad := []struct{ action, raw string }{
		{"verify_host", `{"x":1}`},
		{"verify_host", `[]`},
		{"verify_host", `{} {}`},
		{"create_project_user", `{}`},
		{"create_project_user", `{"project":"root"}`},
		{"create_project_user", `{"project":"systemd-x"}`},
		{"create_project_user", `{"project":"Shop"}`},
		{"create_project_user", `{"project":"a;id"}`},
		{"create_project_user", `{"project":"../x"}`},
		{"create_project_user", `{"project":"shop\n"}`},
		{"create_project_user", `{"project":"shop","path":"/etc"}`},
		{"set_secret", `{"project":"shop","name":"Bad","ciphertext":"-----BEGIN AGE ENCRYPTED FILE-----\nQUJDREVGR0hJSktMTU5PUFFSU1RVVldY\n-----END AGE ENCRYPTED FILE-----"}`},
		{"set_secret", `{"project":"shop","name":"ok_name"}`},
		{"set_secret", `{"project":"shop","name":"ok_name","ciphertext":"plain-text-value"}`},
		{"set_secret", `{"project":"shop","name":"ok_name","ciphertext":"-----BEGIN AGE ENCRYPTED FILE-----\n` + strings.Repeat("a", 16385) + `\n-----END AGE ENCRYPTED FILE-----"}`},
		{"registry_login", `{"project":"shop","registry":"ghcr.io/x","username":"me","secret":"tok"}`},
		{"write_service_env", `{"project":"shop","service":"api","env":[{"name":"A-B","kind":"literal"}]}`},
		{"write_service_env", `{"project":"shop","service":"api","env":[{"name":"A","kind":"secret"}]}`},
		{"write_service_env", `{"project":"shop","service":"api","env":[{"name":"A","kind":"literal","secret":"x_y"}]}`},
		{"write_service_env", `{"project":"shop","service":"api","env":[{"name":"A","kind":"literal","value":"a\nb"}]}`},
		{"write_service_env", `{"project":"shop","service":"api","env":[{"name":"A","kind":"literal"},{"name":"A","kind":"literal"}]}`},
		{"write_service_env", `{"project":"shop","service":"api","env":[{"name":"A","kind":"exec","value":"id"}]}`},
		{"install_quadlet", `{"project":"shop","service":"api","slot":"red","host_port":18001,"container_port":8080,"memory":"512m","pids":256}`},
		{"install_quadlet", `{"project":"shop","service":"api","slot":"blue","host_port":80,"container_port":8080,"memory":"512m","pids":256}`},
		{"install_quadlet", `{"project":"shop","service":"api","slot":"blue","host_port":18001,"container_port":8080,"memory":"512m\nExec=x","pids":256}`},
		{"install_quadlet", `{"project":"shop","service":"api","slot":"blue","host_port":18001,"container_port":8080,"memory":"512m","pids":3}`},
		{"install_caddy_site", `{"project":"shop","site":"main","host":"a b.example.com","listen_port":18000,"upstream_initial":"not_deployed","trusted_proxy":"none","max_body":"5MB"}`},
		{"install_caddy_site", `{"project":"shop","site":"main","host":"x.example.com","listen_port":18000,"upstream_initial":"respond","trusted_proxy":"none","max_body":"5MB"}`},
		{"install_caddy_site", `{"project":"shop","site":"main","host":"x.example.com","listen_port":18000,"upstream_initial":"not_deployed","trusted_proxy":"none","max_body":"5MB","routes":[{"path":"/a{b}","max_body":"1MB"}]}`},
		{"install_caddy_site", `{"project":"shop","site":"main","host":"x.example.com","listen_port":18000,"upstream_initial":"not_deployed","trusted_proxy":"none","max_body":"5MB\n}"}`},
		{"probe_site", `{"host":"x.example.com","listen_port":18000,"expect":"anything"}`},
		{"install_deploy_key", `{"project":"shop","public_key":"command=\"id\" ` + goodKey + `"}`},
		{"install_deploy_key", `{"project":"shop","public_key":"` + goodKey + `\nssh-ed25519 AAAA"}`},
		{"record_project", `{"project":"shop","project_hash":"sha256:zz","ports":{"services":{},"sites":{}}}`},
		{"record_project", `{"project":"shop","project_hash":"sha256:` + strings.Repeat("a", 64) + `","ports":{"services":{"api":[18001]},"sites":{}}}`},
	}
	for _, c := range bad {
		if _, issues := Decode(c.action, json.RawMessage(c.raw)); len(issues) == 0 {
			t.Errorf("%s %s: expected a refusal", c.action, c.raw)
		}
	}
}

func TestCiphertextPatternMatchesSecretsPackage(t *testing.T) {
	f, _ := reflect.TypeOf(SetSecret{}).FieldByName("Ciphertext")
	if f.Tag.Get("pattern") != secrets.ArmorPattern {
		t.Fatalf("the pattern in actions.SetSecret differs from secrets.ArmorPattern:\n%s\n%s", f.Tag.Get("pattern"), secrets.ArmorPattern)
	}
}

func TestIssuePathsArePrefixed(t *testing.T) {
	_, issues := Decode("create_project_user", json.RawMessage(`{"project":"Bad"}`))
	if len(issues) != 1 || issues[0].Path != "/params/project" {
		t.Fatalf("%v", issues)
	}
}

func TestClassesAreNormalInV0(t *testing.T) {
	for _, n := range Names() {
		d, _ := Lookup(n)
		if d.Class != ClassNormal {
			t.Errorf("%s: class %s", n, d.Class)
		}
	}
}

func TestInstallUnitRejectsBadExec(t *testing.T) {
	for _, raw := range []string{
		`{"project":"shop","service":"api","slot":"blue","host_port":18001,"exec":["../x"],"memory":"512m","pids":256}`,
		`{"project":"shop","service":"api","slot":"blue","host_port":18001,"exec":["a/../../x"],"memory":"512m","pids":256}`,
		`{"project":"shop","service":"api","slot":"blue","host_port":18001,"exec":["app","a\nb"],"memory":"512m","pids":256}`,
		`{"project":"shop","service":"api","slot":"blue","host_port":18001,"exec":[""],"memory":"512m","pids":256}`,
	} {
		if _, issues := Decode("install_unit", json.RawMessage(raw)); len(issues) == 0 {
			t.Errorf("must be rejected: %s", raw)
		}
	}
}
