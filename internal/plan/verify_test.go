package plan

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

func goodPlan(t *testing.T) (*Plan, State) {
	st := baseState()
	return build(t, fanvue(t), st), st
}

func verifyAt(t *testing.T, raw []byte, st State, now time.Time) *VerifyError {
	t.Helper()
	_, err := Verify(raw, VerifyOptions{Now: now, ServerID: st.ServerID})
	return err
}

// mutate правит план как generic JSON и пересчитывает хэш, чтобы проверка дошла до нужного правила.
func mutate(t *testing.T, pl *Plan, f func(m map[string]any), reseal bool) []byte {
	t.Helper()
	raw, _ := json.Marshal(pl)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	raw, _ = json.Marshal(m)
	if !reseal {
		return raw
	}
	var p2 Plan
	if err := json.Unmarshal(raw, &p2); err != nil {
		t.Fatal(err)
	}
	if err := p2.Seal(); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(p2)
	return raw
}

func steps(m map[string]any) []any { return m["steps"].([]any) }

func TestVerifyOK(t *testing.T) {
	pl, st := goodPlan(t)
	raw, _ := json.Marshal(pl)
	if err := verifyAt(t, raw, st, testNow); err != nil {
		t.Fatal(err, err.Issues)
	}
}

func TestVerifyRejects(t *testing.T) {
	pl, st := goodPlan(t)
	cases := []struct {
		name   string
		code   string
		reseal bool
		now    time.Time
		f      func(m map[string]any)
	}{
		{"правка без пересчёта хэша", CodeHashMismatch, false, testNow, func(m map[string]any) { m["project"] = "other" }},
		{"правка параметров без хэша", CodeHashMismatch, false, testNow, func(m map[string]any) {
			steps(m)[1].(map[string]any)["params"].(map[string]any)["project"] = "evil"
		}},
		{"срок истёк", CodeExpired, false, testNow.Add(25 * time.Hour), func(m map[string]any) {}},
		{"срок ровно сейчас", CodeExpired, false, testNow.Add(24 * time.Hour), func(m map[string]any) {}},
		{"другой сервер", CodeWrongServer, true, testNow, func(m map[string]any) { m["server_id"] = ServerID("other") }},
		{"неизвестное действие", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[0].(map[string]any)["action"] = "run_shell"
		}},
		{"чужой проект в шаге", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[1].(map[string]any)["params"].(map[string]any)["project"] = "evil"
		}},
		{"плохое имя проекта в параметрах", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[1].(map[string]any)["params"].(map[string]any)["project"] = "../etc"
		}},
		{"лишний параметр", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[1].(map[string]any)["params"].(map[string]any)["path"] = "/etc/passwd"
		}},
		{"неверный класс", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[0].(map[string]any)["class"] = "safe"
		}},
		{"сбой нумерации", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[2].(map[string]any)["id"] = "s09"
		}},
		{"неизвестное поле плана", CodeInvalidPlan, false, testNow, func(m map[string]any) { m["extra"] = 1 }},
		{"чужая схема", CodeInvalidPlan, true, testNow, func(m map[string]any) { m["schema"] = "x" }},
		{"пустые шаги", CodeInvalidPlan, true, testNow, func(m map[string]any) { m["steps"] = []any{} }},
		{"TTL больше 7 суток", CodeInvalidPlan, true, testNow, func(m map[string]any) { m["expires_at"] = "2026-12-01T12:00:00Z" }},
		{"expires раньше created", CodeInvalidPlan, true, testNow, func(m map[string]any) { m["expires_at"] = "2026-11-01T12:00:00Z" }},
		{"произвольный путь в параметре", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			for _, s := range steps(m) {
				sm := s.(map[string]any)
				if sm["action"] == "install_caddy_site" {
					sm["params"].(map[string]any)["site"] = "../../etc/cron.d/x"
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := verifyAt(t, mutate(t, pl, c.f, c.reseal), st, c.now)
			if err == nil || err.Code != c.code {
				t.Fatalf("ожидали %s, получили %v", c.code, err)
			}
		})
	}
}

func TestVerifyLimits(t *testing.T) {
	pl, st := goodPlan(t)
	raw, _ := json.Marshal(pl)
	if _, err := Verify(raw, VerifyOptions{Now: testNow, ServerID: st.ServerID, Limits: Limits{PlanBytes: 1000, PlanSteps: 200, PlanTTLHours: 168}}); err == nil || err.Code != CodeInvalidPlan {
		t.Fatalf("размер: %v", err)
	}
	if _, err := Verify(raw, VerifyOptions{Now: testNow, ServerID: st.ServerID, Limits: Limits{PlanBytes: 1 << 20, PlanSteps: 5, PlanTTLHours: 168}}); err == nil || err.Code != CodeInvalidPlan {
		t.Fatalf("шаги: %v", err)
	}
	if _, err := Verify(append(raw, []byte(" {}")...), VerifyOptions{Now: testNow, ServerID: st.ServerID}); err == nil {
		t.Fatal("лишние данные")
	}
	if _, err := Verify([]byte("не json"), VerifyOptions{Now: testNow, ServerID: st.ServerID}); err == nil {
		t.Fatal("не JSON")
	}
}

func TestDescribe(t *testing.T) {
	pl, _ := goodPlan(t)
	d := Describe(pl)
	if len(d) != len(pl.Steps) || !strings.HasPrefix(d[0], "s01 verify_host: ") {
		t.Errorf("Describe: %v", d[:1])
	}
}

func TestULID(t *testing.T) {
	id, err := NewULID(testNow, zeroReader{})
	if err != nil || len(id) != 26 {
		t.Fatal(id, err)
	}
	if id[0] > '7' {
		t.Errorf("первый символ ULID не больше 7: %s", id)
	}
	id2, _ := NewULID(testNow.Add(time.Millisecond), zeroReader{})
	if id2 <= id {
		t.Errorf("ULID должны расти со временем: %s %s", id, id2)
	}
	// первые 10 символов кодируют время в мс (Crockford base32)
	var ms uint64
	for _, c := range id[:10] {
		ms = ms<<5 | uint64(strings.IndexRune(crockford, c))
	}
	if ms != uint64(testNow.UnixMilli()) {
		t.Errorf("время в ULID %d, ожидали %d", ms, testNow.UnixMilli())
	}
	if id[10:] != strings.Repeat("0", 16) {
		t.Errorf("случайная часть при нулевом источнике: %s", id[10:])
	}
	ff, _ := NewULID(testNow, onesReader{})
	if ff[10:] != strings.Repeat("Z", 16) {
		t.Errorf("случайная часть при 0xFF: %s", ff[10:])
	}
}

type onesReader struct{}

func (onesReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0xFF
	}
	return len(p), nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestLoadState(t *testing.T) {
	m := host.NewMemFS()
	m.Files["/etc/machine-id"] = []byte("abc123\n")
	m.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\nfanvue-games:x:1001:1001::/home/fanvue-games:/bin/bash\n")
	m.Files["/var/lib/lite-deploy/state/projects/shop.json"] = []byte(`{"hash":"sha256:aa","ports":{"services":{"web":[18001,18002]},"sites":{"main":18000}},"secrets":["a_b"],"deployed":true}`)
	st, err := LoadState(m, DefaultPaths())
	if err != nil {
		t.Fatal(err)
	}
	if st.ServerID != ServerID("abc123") {
		t.Error("ServerID")
	}
	if !st.ExistingUsers["fanvue-games"] || !st.ExistingUsers["root"] || st.ExistingUsers["x"] {
		t.Errorf("пользователи: %v", st.ExistingUsers)
	}
	ps := st.Projects["shop"]
	if !ps.Deployed || ps.Ports.Sites["main"] != 18000 || ps.Secrets[0] != "a_b" {
		t.Errorf("проект: %+v", ps)
	}

	// без каталога проектов: проектов нет, ошибки нет
	m2 := host.NewMemFS()
	m2.Files["/etc/machine-id"] = []byte("abc")
	st2, err := LoadState(m2, DefaultPaths())
	if err != nil || len(st2.Projects) != 0 {
		t.Fatalf("пустое состояние: %v %v", st2.Projects, err)
	}
	// нет machine-id: ошибка
	if _, err := LoadState(host.NewMemFS(), DefaultPaths()); err == nil {
		t.Fatal("ожидали ошибку без machine-id")
	}
	// повреждённый файл состояния
	m.Files["/var/lib/lite-deploy/state/projects/bad.json"] = []byte("{")
	if _, err := LoadState(m, DefaultPaths()); err == nil {
		t.Fatal("ожидали ошибку для повреждённого состояния")
	}
}
