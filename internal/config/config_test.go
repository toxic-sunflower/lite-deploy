package config

import (
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

func TestDefaultsWhenMissing(t *testing.T) {
	c, issues, err := Load(host.NewMemFS(), DefaultPath)
	if err != nil || len(issues) > 0 || c.Limits.PlanBytes != 262144 || c.Limits.PortRange[1] != 18999 || c.Quotas.ApplyPerHour != 60 {
		t.Fatalf("%+v %v %v", c, issues, err)
	}
}

func TestOverridesMerge(t *testing.T) {
	m := host.NewMemFS()
	m.Files[DefaultPath] = []byte(`{"limits":{"projects":5,"port_range":[20000,20100]},"quotas":{"apply_per_hour":3}}`)
	c, issues, err := Load(m, DefaultPath)
	if err != nil || len(issues) > 0 {
		t.Fatal(issues, err)
	}
	if c.Limits.Projects != 5 || c.Limits.PortRange[0] != 20000 || c.Limits.PlanSteps != 200 || c.Quotas.ApplyPerHour != 3 || c.Quotas.SecretSetsPerHour != 120 {
		t.Fatalf("%+v", c)
	}
}

func TestRejected(t *testing.T) {
	for _, in := range []string{`{"x":1}`, `{"limits":{"projects":0,"plan_steps":5000}}`, `{"limits":{"port_range":[20100,20000]}}`,
		`{"limits":{"port_range":[20000]}}`, `{"limits":{"secret_bytes":99999}}`, `{} {}`, `nope`} {
		m := host.NewMemFS()
		m.Files[DefaultPath] = []byte(in)
		if _, issues, err := Load(m, DefaultPath); err != nil || len(issues) == 0 {
			t.Errorf("%s: ожидали ошибку, получили %v %v", in, issues, err)
		}
	}
}
