// Package config reads /etc/lite-deploy/config.json: limits and quotas (SPEC-v0.md, section 8).
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// DefaultPath is the path of the settings file on the server.
const DefaultPath = "/etc/lite-deploy/config.json"

// Limits are limits on sizes and numbers of objects. A zero field means "default".
type Limits struct {
	PlanBytes          int   `json:"plan_bytes,omitempty" min:"1024" max:"4194304"`
	PlanSteps          int   `json:"plan_steps,omitempty" min:"1" max:"1000"`
	ProjectBytes       int   `json:"project_bytes,omitempty" min:"1024" max:"1048576"`
	SecretBytes        int   `json:"secret_bytes,omitempty" min:"1" max:"8192"`
	Projects           int   `json:"projects,omitempty" min:"1" max:"1000"`
	ServicesPerProject int   `json:"services_per_project,omitempty" min:"1" max:"8"`
	PortRange          []int `json:"port_range,omitempty" min:"1024" max:"65535" minItems:"2" maxItems:"2"`
	PlanTTLHours       int   `json:"plan_ttl_hours,omitempty" min:"1" max:"168"`
}

// Quotas are quotas on the number of actions.
type Quotas struct {
	DeployPer10MinPerProject int `json:"deploy_per_10min_per_project,omitempty" min:"1" max:"1000"`
	ApplyPerHour             int `json:"apply_per_hour,omitempty" min:"1" max:"10000"`
	SecretSetsPerHour        int `json:"secret_sets_per_hour,omitempty" min:"1" max:"10000"`
}

// Config is the content of config.json.
type Config struct {
	Limits Limits `json:"limits,omitempty"`
	Quotas Quotas `json:"quotas,omitempty"`
}

// Default values from SPEC-v0.md, section 8.
func Default() Config {
	return Config{
		Limits: Limits{PlanBytes: 262144, PlanSteps: 200, ProjectBytes: 65536, SecretBytes: 8192, Projects: 50,
			ServicesPerProject: 8, PortRange: []int{18000, 18999}, PlanTTLHours: 24},
		Quotas: Quotas{DeployPer10MinPerProject: 10, ApplyPerHour: 60, SecretSetsPerHour: 120},
	}
}

// Load reads the file. No file: the defaults. Unknown fields and out-of-range values are rejected.
func Load(fsys host.FS, path string) (Config, []schema.Issue, error) {
	def := Default()
	raw, err := fsys.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return def, nil, nil
	}
	if err != nil {
		return def, nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return def, []schema.Issue{{Path: "/", Code: "syntax", Message: err.Error()}}, nil
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return def, []schema.Issue{{Path: "/", Code: "syntax", Message: "extra data after the JSON"}}, nil
	}
	issues := schema.Check(&c)
	if pr := c.Limits.PortRange; len(pr) == 2 && pr[0] > pr[1] {
		issues = append(issues, schema.Issue{Path: "/limits/port_range", Code: "range", Message: "the start of the range is greater than the end"})
	}
	if len(issues) > 0 {
		return def, issues, nil
	}
	return merge(def, c), nil, nil
}

func merge(d, c Config) Config {
	set := func(dst *int, v int) {
		if v != 0 {
			*dst = v
		}
	}
	set(&d.Limits.PlanBytes, c.Limits.PlanBytes)
	set(&d.Limits.PlanSteps, c.Limits.PlanSteps)
	set(&d.Limits.ProjectBytes, c.Limits.ProjectBytes)
	set(&d.Limits.SecretBytes, c.Limits.SecretBytes)
	set(&d.Limits.Projects, c.Limits.Projects)
	set(&d.Limits.ServicesPerProject, c.Limits.ServicesPerProject)
	set(&d.Limits.PlanTTLHours, c.Limits.PlanTTLHours)
	if len(c.Limits.PortRange) == 2 {
		d.Limits.PortRange = c.Limits.PortRange
	}
	set(&d.Quotas.DeployPer10MinPerProject, c.Quotas.DeployPer10MinPerProject)
	set(&d.Quotas.ApplyPerHour, c.Quotas.ApplyPerHour)
	set(&d.Quotas.SecretSetsPerHour, c.Quotas.SecretSetsPerHour)
	return d
}
