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
	return build(t, example(t), st), st
}

func verifyAt(t *testing.T, raw []byte, st State, now time.Time) *VerifyError {
	t.Helper()
	_, err := Verify(raw, VerifyOptions{Now: now, ServerID: st.ServerID})
	return err
}

// mutate edits a plan as generic JSON and recomputes the hash, so that the check reaches the wanted rule.
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
		{"an edit without recomputing the hash", CodeHashMismatch, false, testNow, func(m map[string]any) { m["project"] = "other" }},
		{"a parameter edit without the hash", CodeHashMismatch, false, testNow, func(m map[string]any) {
			steps(m)[1].(map[string]any)["params"].(map[string]any)["project"] = "evil"
		}},
		{"expired", CodeExpired, false, testNow.Add(25 * time.Hour), func(m map[string]any) {}},
		{"expires right now", CodeExpired, false, testNow.Add(24 * time.Hour), func(m map[string]any) {}},
		{"another server", CodeWrongServer, true, testNow, func(m map[string]any) { m["server_id"] = ServerID("other") }},
		{"unknown action", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[0].(map[string]any)["action"] = "run_shell"
		}},
		{"a foreign project in the step", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[1].(map[string]any)["params"].(map[string]any)["project"] = "evil"
		}},
		{"a bad project name in the parameters", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[1].(map[string]any)["params"].(map[string]any)["project"] = "../etc"
		}},
		{"an extra parameter", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[1].(map[string]any)["params"].(map[string]any)["path"] = "/etc/passwd"
		}},
		{"an invalid class", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[0].(map[string]any)["class"] = "safe"
		}},
		{"a numbering failure", CodeInvalidPlan, true, testNow, func(m map[string]any) {
			steps(m)[2].(map[string]any)["id"] = "s09"
		}},
		{"unknown plan field", CodeInvalidPlan, false, testNow, func(m map[string]any) { m["extra"] = 1 }},
		{"a foreign schema", CodeInvalidPlan, true, testNow, func(m map[string]any) { m["schema"] = "x" }},
		{"empty steps", CodeInvalidPlan, true, testNow, func(m map[string]any) { m["steps"] = []any{} }},
		{"TTL longer than 7 days", CodeInvalidPlan, true, testNow, func(m map[string]any) { m["expires_at"] = "2026-12-01T12:00:00Z" }},
		{"expires before created", CodeInvalidPlan, true, testNow, func(m map[string]any) { m["expires_at"] = "2026-11-01T12:00:00Z" }},
		{"an arbitrary path in a parameter", CodeInvalidPlan, true, testNow, func(m map[string]any) {
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
				t.Fatalf("expected %s, got %v", c.code, err)
			}
		})
	}
}

func TestVerifyLimits(t *testing.T) {
	pl, st := goodPlan(t)
	raw, _ := json.Marshal(pl)
	if _, err := Verify(raw, VerifyOptions{Now: testNow, ServerID: st.ServerID, Limits: Limits{PlanBytes: 1000, PlanSteps: 200, PlanTTLHours: 168}}); err == nil || err.Code != CodeInvalidPlan {
		t.Fatalf("size: %v", err)
	}
	if _, err := Verify(raw, VerifyOptions{Now: testNow, ServerID: st.ServerID, Limits: Limits{PlanBytes: 1 << 20, PlanSteps: 5, PlanTTLHours: 168}}); err == nil || err.Code != CodeInvalidPlan {
		t.Fatalf("steps: %v", err)
	}
	if _, err := Verify(append(raw, []byte(" {}")...), VerifyOptions{Now: testNow, ServerID: st.ServerID}); err == nil {
		t.Fatal("extra data")
	}
	if _, err := Verify([]byte("not json"), VerifyOptions{Now: testNow, ServerID: st.ServerID}); err == nil {
		t.Fatal("not JSON")
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
		t.Errorf("the first character of the ULID is not greater than 7: %s", id)
	}
	id2, _ := NewULID(testNow.Add(time.Millisecond), zeroReader{})
	if id2 <= id {
		t.Errorf("ULIDs must grow with time: %s %s", id, id2)
	}
	// the first 10 characters encode the time in ms (Crockford base32)
	var ms uint64
	for _, c := range id[:10] {
		ms = ms<<5 | uint64(strings.IndexRune(crockford, c))
	}
	if ms != uint64(testNow.UnixMilli()) {
		t.Errorf("time in the ULID %d, expected %d", ms, testNow.UnixMilli())
	}
	if id[10:] != strings.Repeat("0", 16) {
		t.Errorf("the random part with a zero source: %s", id[10:])
	}
	ff, _ := NewULID(testNow, onesReader{})
	if ff[10:] != strings.Repeat("Z", 16) {
		t.Errorf("the random part at 0xFF: %s", ff[10:])
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
	m.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\nshop:x:1001:1001::/home/shop:/bin/bash\n")
	m.Files["/var/lib/lite-deploy/state/projects/shop.json"] = []byte(`{"hash":"sha256:aa","ports":{"services":{"web":[18001,18002]},"sites":{"main":18000}},"secrets":["a_b"],"deployed":true}`)
	st, err := LoadState(m, DefaultPaths())
	if err != nil {
		t.Fatal(err)
	}
	if st.ServerID != ServerID("abc123") {
		t.Error("ServerID")
	}
	if !st.ExistingUsers["shop"] || !st.ExistingUsers["root"] || st.ExistingUsers["x"] {
		t.Errorf("users: %v", st.ExistingUsers)
	}
	ps := st.Projects["shop"]
	if !ps.Deployed || ps.Ports.Sites["main"] != 18000 || ps.Secrets[0] != "a_b" {
		t.Errorf("project: %+v", ps)
	}

	// no projects directory: there are no projects, no error
	m2 := host.NewMemFS()
	m2.Files["/etc/machine-id"] = []byte("abc")
	st2, err := LoadState(m2, DefaultPaths())
	if err != nil || len(st2.Projects) != 0 {
		t.Fatalf("an empty state: %v %v", st2.Projects, err)
	}
	// no machine-id: an error
	if _, err := LoadState(host.NewMemFS(), DefaultPaths()); err == nil {
		t.Fatal("an error without machine-id was expected")
	}
	// a corrupt state file
	m.Files["/var/lib/lite-deploy/state/projects/bad.json"] = []byte("{")
	if _, err := LoadState(m, DefaultPaths()); err == nil {
		t.Fatal("an error for a corrupt state was expected")
	}
}
