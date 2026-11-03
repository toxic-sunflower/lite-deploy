package plan

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
)

var testNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func fanvue(t *testing.T) *project.Project {
	t.Helper()
	data, err := os.ReadFile("../../testdata/fanvue-games/project.json")
	if err != nil {
		t.Fatal(err)
	}
	p, issues := project.Parse(data)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	return p
}

func ct(names ...string) map[string]string {
	m := map[string]string{}
	for _, n := range names {
		m[n] = "-----BEGIN TEST CIPHERTEXT-----\n" + n + "\n-----END TEST CIPHERTEXT-----"
	}
	return m
}

func baseState() State {
	return State{
		ServerID: ServerID("machine-id-1"),
		Now:      testNow,
		NewID:    func(now time.Time) (string, error) { return "01JC3Y8Q4N6R2T7V9K0M5B1XZA", nil },
		Ciphertexts: ct("ghcr_read_token", "auth_session_secret", "fanvue_webhook_secret",
			"admin_username", "admin_password", "_db_password"),
	}
}

func build(t *testing.T, p *project.Project, st State) *Plan {
	t.Helper()
	pl, err := Build(p, st)
	if err != nil {
		t.Fatalf("Build: %v %v", err, err.Issues)
	}
	return pl
}

func actionsOf(pl *Plan) []string {
	var out []string
	for _, s := range pl.Steps {
		out = append(out, s.Action)
	}
	return out
}

func TestBuildFanvueStepOrder(t *testing.T) {
	pl := build(t, fanvue(t), baseState())
	want := []string{
		"verify_host", "create_project_user",
		"set_secret", // _db_password раньше provision_postgres (adopt)
		"provision_postgres",
		"set_secret", "set_secret", "set_secret", "set_secret", "set_secret", // admin_password, admin_username, auth_session_secret, fanvue_webhook_secret, ghcr_read_token
		"registry_login",
		"write_service_env", "write_service_env",
		"install_quadlet", "install_quadlet", "install_quadlet", "install_quadlet",
		"reload_user_units",
		"install_caddy_site", "install_caddy_site",
		"install_reload_rule", "reload_caddy",
		"probe_site", "probe_site",
		"install_deploy_key", "record_project",
	}
	if got := actionsOf(pl); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("порядок шагов:\n got %v\nwant %v", got, want)
	}
	for i, s := range pl.Steps {
		if s.ID != "s"+two(i+1) {
			t.Errorf("id шага %d = %s", i, s.ID)
		}
		if s.Class != "normal" || s.Summary == "" {
			t.Errorf("шаг %s: класс %q, описание %q", s.ID, s.Class, s.Summary)
		}
	}
	if pl.Steps[2].ID != "s03" || !strings.Contains(string(pl.Steps[2].Params), "_db_password") {
		t.Errorf("s03 должен задавать _db_password: %s", pl.Steps[2].Params)
	}
}

func two(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestBuildPlanIsVerifiable(t *testing.T) {
	st := baseState()
	pl := build(t, fanvue(t), st)
	raw, _ := json.Marshal(pl)
	got, verr := Verify(raw, VerifyOptions{Now: testNow.Add(time.Hour), ServerID: st.ServerID})
	if verr != nil {
		t.Fatalf("Verify: %v %v", verr, verr.Issues)
	}
	if got.Hash != pl.Hash {
		t.Error("хэш изменился")
	}
	if pl.ExpiresAt != "2026-11-04T12:00:00Z" || pl.CreatedAt != "2026-11-03T12:00:00Z" {
		t.Errorf("сроки: %s %s", pl.CreatedAt, pl.ExpiresAt)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a := build(t, fanvue(t), baseState())
	b := build(t, fanvue(t), baseState())
	if a.Hash != b.Hash {
		t.Fatal("одинаковый вход даёт разные хэши")
	}
}

func TestPlanContainsNoSecretValues(t *testing.T) {
	// шифртекст в плане есть, но открытых значений нет: в этом тесте «значения» это маркеры
	st := baseState()
	st.Ciphertexts["admin_password"] = "CIPHERTEXT-ONLY"
	pl := build(t, fanvue(t), st)
	raw, _ := json.Marshal(pl)
	for _, bad := range []string{"PLAINTEXT", "password=", "Password="} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Errorf("в плане найдено %q", bad)
		}
	}
	// ссылки secret:// в плане не остаются: только имена
	if bytes.Contains(raw, []byte("secret://")) {
		t.Error("в плане осталась ссылка secret://")
	}
	// строка подключения не вычисляется при построении
	for _, s := range pl.Steps {
		if s.Action == "write_service_env" && strings.Contains(string(s.Params), "postgresql://") {
			t.Error("строка подключения в плане")
		}
	}
}

func TestBuildPortsAndRecord(t *testing.T) {
	pl := build(t, fanvue(t), baseState())
	last := pl.Steps[len(pl.Steps)-1]
	if last.Action != "record_project" {
		t.Fatal("последний шаг должен быть record_project")
	}
	v, issues := actions.Decode(last.Action, last.Params)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	rp := v.(*actions.RecordProject)
	if rp.Ports.Services["api"][0] != 18081 || rp.Ports.Services["admin"][1] != 18092 || rp.Ports.Sites["main"] != 18080 || rp.Ports.Sites["admin"] != 18090 {
		t.Errorf("порты: %+v", rp.Ports)
	}
	if !strings.HasPrefix(rp.ProjectHash, "sha256:") {
		t.Error("project_hash")
	}
}

func TestAutoPortsAvoidOtherProjects(t *testing.T) {
	p := fanvue(t)
	p.Adopt = false
	s := p.Services["api"]
	s.HostPorts = nil
	p.Services["api"] = s
	p.Sites[0].ListenPort = 0
	st := baseState()
	delete(st.Ciphertexts, "_db_password")
	st.Projects = map[string]ProjectState{"other": {Ports: actions.Ports{
		Services: map[string][]int{"web": {18000, 18001}}, Sites: map[string]int{"main": 18002}}}}
	pl := build(t, p, st)
	last := pl.Steps[len(pl.Steps)-1]
	v, _ := actions.Decode(last.Action, last.Params)
	rp := v.(*actions.RecordProject)
	got := append(append([]int{}, rp.Ports.Services["api"]...), rp.Ports.Sites["main"])
	// 18000–18002 заняты другим проектом, явные 18080/18090/18091/18092 заняты этим
	want := []int{18003, 18004, 18005}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("порты %v, ожидали %v", got, want)
		}
	}
}

func TestExplicitPortTakenByOtherProject(t *testing.T) {
	st := baseState()
	st.Projects = map[string]ProjectState{"other": {Ports: actions.Ports{Sites: map[string]int{"x": 18080}}}}
	_, err := Build(fanvue(t), st)
	if err == nil || err.Code != CodePrecondition || !strings.Contains(err.Message, "18080") {
		t.Fatalf("ожидали precondition_failed про 18080: %v", err)
	}
}

func TestPortsExhausted(t *testing.T) {
	p := fanvue(t)
	for n, s := range p.Services {
		s.HostPorts = nil
		p.Services[n] = s
	}
	for i := range p.Sites {
		p.Sites[i].ListenPort = 0
	}
	st := baseState()
	st.PortRange = [2]int{18000, 18004} // нужно 6 портов, есть 5
	_, err := Build(p, st)
	if err == nil || err.Code != CodePrecondition || !strings.Contains(err.Message, "нет свободных портов") {
		t.Fatalf("ожидали нехватку портов: %v", err)
	}
	st.PortRange = [2]int{18000, 18005}
	build(t, p, st)
}

func TestSecretMissing(t *testing.T) {
	st := baseState()
	delete(st.Ciphertexts, "admin_password")
	delete(st.Ciphertexts, "ghcr_read_token")
	_, err := Build(fanvue(t), st)
	if err == nil || err.Code != CodeSecretMissing {
		t.Fatalf("ожидали secret_missing: %v", err)
	}
	if !strings.Contains(err.Message, "admin_password") || !strings.Contains(err.Message, "ghcr_read_token") {
		t.Errorf("в сообщении нужны имена: %s", err.Message)
	}
}

func TestExistingSecretIsNotResent(t *testing.T) {
	st := baseState()
	delete(st.Ciphertexts, "admin_password")
	st.Projects = map[string]ProjectState{"fanvue-games": {Secrets: []string{"admin_password"}}}
	pl := build(t, fanvue(t), st)
	for _, s := range pl.Steps {
		if s.Action == "set_secret" && strings.Contains(string(s.Params), "admin_password") {
			t.Error("секрет уже на сервере, set_secret не нужен")
		}
	}
}

func TestUnusedCiphertextRejected(t *testing.T) {
	st := baseState()
	st.Ciphertexts["stray"] = "x"
	if _, err := Build(fanvue(t), st); err == nil || err.Code != CodeUnusedSecret {
		t.Fatalf("ожидали unused_secret: %v", err)
	}
}

func TestDBPasswordOnlyInAdopt(t *testing.T) {
	p := fanvue(t)
	p.Adopt = false
	if _, err := Build(p, baseState()); err == nil || err.Code != CodeInvalidProject {
		t.Fatalf("_db_password без adopt должен отклоняться: %v", err)
	}
}

func TestAdoptPasswordUnknown(t *testing.T) {
	st := baseState()
	delete(st.Ciphertexts, "_db_password")
	st.ExistingUsers = map[string]bool{"fanvue-games": true}
	if _, err := Build(fanvue(t), st); err == nil || err.Code != CodeAdoptPassword {
		t.Fatalf("ожидали adopt_password_unknown: %v", err)
	}
	st.RotatePassword = true
	pl := build(t, fanvue(t), st)
	for _, s := range pl.Steps {
		if s.Action == "provision_postgres" && !strings.Contains(string(s.Params), `"rotate_password":true`) {
			t.Error("rotate_password должен попасть в шаг")
		}
	}
}

func TestUnmanagedExistingUser(t *testing.T) {
	p := fanvue(t)
	p.Adopt = false
	st := baseState()
	delete(st.Ciphertexts, "_db_password")
	st.ExistingUsers = map[string]bool{"fanvue-games": true}
	if _, err := Build(p, st); err == nil || err.Code != CodeUnmanaged {
		t.Fatalf("ожидали project_exists_unmanaged: %v", err)
	}
	p.Adopt = true
	st.Ciphertexts["_db_password"] = "x"
	build(t, p, st)
}

func TestProbeExpectFollowsState(t *testing.T) {
	st := baseState()
	st.Projects = map[string]ProjectState{"fanvue-games": {Deployed: true, Secrets: []string{"_db_password"}}}
	pl := build(t, fanvue(t), st)
	n := 0
	for _, s := range pl.Steps {
		if s.Action == "probe_site" {
			n++
			if !strings.Contains(string(s.Params), `"expect":"deployed"`) {
				t.Errorf("expect: %s", s.Params)
			}
		}
	}
	if n != 2 {
		t.Fatalf("probe_site шагов %d", n)
	}
}

func TestProjectQuota(t *testing.T) {
	st := baseState()
	st.MaxProjects = 1
	st.Projects = map[string]ProjectState{"other": {}}
	if _, err := Build(fanvue(t), st); err == nil || err.Code != CodeQuotaExceeded {
		t.Fatalf("ожидали quota_exceeded: %v", err)
	}
}

func TestInvalidProjectRejected(t *testing.T) {
	p := fanvue(t)
	p.Name = "Bad Name"
	_, err := Build(p, baseState())
	if err == nil || err.Code != CodeInvalidProject || len(err.Issues) == 0 {
		t.Fatalf("ожидали invalid_project: %v", err)
	}
}

func TestNoDatabaseNoProvision(t *testing.T) {
	p := fanvue(t)
	p.Database = nil
	for n, s := range p.Services {
		s.DatabaseEnv = ""
		p.Services[n] = s
	}
	p.Adopt = false
	st := baseState()
	delete(st.Ciphertexts, "_db_password")
	pl := build(t, p, st)
	for _, a := range actionsOf(pl) {
		if a == "provision_postgres" {
			t.Fatal("без database нет provision_postgres")
		}
	}
}

func TestDatabaseSocketOnlyWithDatabaseEnv(t *testing.T) {
	pl := build(t, fanvue(t), baseState())
	for _, s := range pl.Steps {
		if s.Action == "install_quadlet" && !strings.Contains(string(s.Params), `"database_socket":true`) {
			t.Errorf("database_socket: %s", s.Params)
		}
	}
}
