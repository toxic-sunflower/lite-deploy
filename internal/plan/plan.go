package plan

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// SchemaID is the identifier of the plan schema version.
const SchemaID = "lite-deploy/plan/v1"

// KindProjectCreate is the only plan kind in v0.
const KindProjectCreate = "project.create"

// Plan is the list of steps the executor will apply (D-34).
type Plan struct {
	Schema      string `json:"schema" enum:"lite-deploy/plan/v1"`
	ID          string `json:"id" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"ULID"`
	CreatedAt   string `json:"created_at" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"`
	ExpiresAt   string `json:"expires_at" pattern:"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"`
	ServerID    string `json:"server_id" pattern:"^sha256:[0-9a-f]{64}$"`
	Kind        string `json:"kind" enum:"project.create"`
	Project     string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	ProjectHash string `json:"project_hash" pattern:"^sha256:[0-9a-f]{64}$"`
	Steps       []Step `json:"steps" minItems:"1" maxItems:"200"`
	Hash        string `json:"hash" pattern:"^sha256:[0-9a-f]{64}$" doc:"sha256 of the canonical plan JSON without the hash field"`
}

// Step is one executor action. The executor looks only at Action and Params, Summary is for people.
type Step struct {
	ID      string          `json:"id" pattern:"^s[0-9]{2,3}$"`
	Action  string          `json:"action" pattern:"^[a-z_]{1,64}$"`
	Class   string          `json:"class" enum:"safe,normal,dangerous"`
	Params  json.RawMessage `json:"params"`
	Summary string          `json:"summary" maxLen:"500"`
}

// Limits are the plan limits (SPEC-v0.md, section 8).
type Limits struct {
	PlanBytes    int
	PlanSteps    int
	PlanTTLHours int
}

// DefaultLimits are the default values.
func DefaultLimits() Limits { return Limits{PlanBytes: 256 * 1024, PlanSteps: 200, PlanTTLHours: 168} }

// ServerID computes the server identifier from /etc/machine-id: sha256("lite-deploy:" + id).
func ServerID(machineID string) string {
	return HashBytes([]byte("lite-deploy:" + strings.TrimSpace(machineID)))
}

// ComputeHash computes the plan hash without the hash field.
func (p Plan) ComputeHash() (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	delete(m, "hash")
	noHash, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	c, err := Canonical(noHash)
	if err != nil {
		return "", err
	}
	return HashBytes(c), nil
}

// Seal sets the hash.
func (p *Plan) Seal() error {
	h, err := p.ComputeHash()
	if err != nil {
		return err
	}
	p.Hash = h
	return nil
}

// Plan verification error codes (SPEC-v0.md, 6.3).
const (
	CodeInvalidPlan  = "invalid_plan"
	CodeHashMismatch = "plan_hash_mismatch"
	CodeExpired      = "plan_expired"
	CodeWrongServer  = "plan_wrong_server"
)

// VerifyError is a plan verification error.
type VerifyError struct {
	Code    string
	Message string
	Issues  []schema.Issue
}

func (e *VerifyError) Error() string { return e.Code + ": " + e.Message }

func invalid(msg string, issues ...schema.Issue) *VerifyError {
	return &VerifyError{Code: CodeInvalidPlan, Message: msg, Issues: issues}
}

// VerifyOptions is what the verifier knows about the current server and time.
type VerifyOptions struct {
	Now      time.Time
	ServerID string
	Limits   Limits
}

// Verify parses and validates a plan by the steps of section 3 ("Applying"): schema, size, steps, hash, expiry, server.
// The checks go in this order so that a foreign or corrupt plan is rejected before anything is executed.
func Verify(data []byte, o VerifyOptions) (*Plan, *VerifyError) {
	if o.Limits == (Limits{}) {
		o.Limits = DefaultLimits()
	}
	if len(data) > o.Limits.PlanBytes {
		return nil, invalid(fmt.Sprintf("the plan is larger than %d bytes", o.Limits.PlanBytes))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		return nil, invalid("the plan could not be parsed: " + err.Error())
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid("extra data after the plan")
	}
	issues := schema.Check(&p)
	if len(p.Steps) > o.Limits.PlanSteps {
		issues = append(issues, schema.Issue{Path: "/steps", Code: "maxItems", Message: fmt.Sprintf("more than %d steps", o.Limits.PlanSteps)})
	}
	if len(issues) > 0 {
		return nil, invalid("the plan did not pass validation", issues...)
	}

	want, err := p.ComputeHash()
	if err != nil {
		return nil, invalid("hash was not computed: " + err.Error())
	}
	if want != p.Hash {
		return nil, &VerifyError{Code: CodeHashMismatch, Message: fmt.Sprintf("plan hash does not match: the file says %s, computed %s", p.Hash, want)}
	}

	created, err1 := time.Parse(time.RFC3339, p.CreatedAt)
	expires, err2 := time.Parse(time.RFC3339, p.ExpiresAt)
	if err1 != nil || err2 != nil {
		return nil, invalid("time in the plan could not be parsed")
	}
	if !expires.After(created) || expires.Sub(created) > time.Duration(o.Limits.PlanTTLHours)*time.Hour {
		return nil, invalid(fmt.Sprintf("the plan lifetime must be positive and at most %d h", o.Limits.PlanTTLHours))
	}
	if !o.Now.Before(expires) {
		return nil, &VerifyError{Code: CodeExpired, Message: "the plan has expired: " + p.ExpiresAt}
	}
	if p.ServerID != o.ServerID {
		return nil, &VerifyError{Code: CodeWrongServer, Message: "the plan was built for another server"}
	}
	// The steps are checked last: by then the plan is whole, fresh and for this server.
	if issues := checkSteps(&p); len(issues) > 0 {
		return nil, invalid("the plan steps did not pass validation", issues...)
	}
	return &p, nil
}

func checkSteps(p *Plan) []schema.Issue {
	var out []schema.Issue
	for i, s := range p.Steps {
		base := fmt.Sprintf("/steps/%d", i)
		if want := fmt.Sprintf("s%02d", i+1); s.ID != want {
			out = append(out, schema.Issue{Path: base + "/id", Code: "sequence", Message: "expected id " + want})
		}
		def, ok := actions.Lookup(s.Action)
		if !ok {
			out = append(out, schema.Issue{Path: base + "/action", Code: "unknown_action", Message: fmt.Sprintf("action %q is not in the executor list", s.Action)})
			continue
		}
		if def.Class != s.Class {
			out = append(out, schema.Issue{Path: base + "/class", Code: "class_mismatch", Message: fmt.Sprintf("action class %s: %s", s.Action, def.Class)})
		}
		if _, iss := actions.Decode(s.Action, s.Params); len(iss) > 0 {
			for _, is := range iss {
				is.Path = base + is.Path
				out = append(out, is)
			}
			continue
		}
		var m map[string]any
		_ = json.Unmarshal(s.Params, &m)
		if prj, ok := m["project"]; ok && prj != p.Project {
			out = append(out, schema.Issue{Path: base + "/params/project", Code: "foreign_project", Message: "the step belongs to a different project than the plan"})
		}
	}
	return out
}

// Describe returns human-readable lines: "s03 provision_postgres: description".
func Describe(p *Plan) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, fmt.Sprintf("%s %s: %s", s.ID, s.Action, s.Summary))
	}
	return out
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID creates a ULID: 48 bits of time (ms) and 80 bits of randomness.
func NewULID(now time.Time, r io.Reader) (string, error) {
	var rnd [10]byte
	if _, err := io.ReadFull(r, rnd[:]); err != nil {
		return "", err
	}
	ms := max(now.UnixMilli(), 0)
	var b [16]byte
	for i := 0; i < 6; i++ {
		b[i] = byte((ms >> (8 * (5 - i))) & 0xff)
	}
	copy(b[6:], rnd[:])
	// 128 bits → 26 characters of 5 bits (the top 2 bits of the first character are zero)
	out := make([]byte, 26)
	var acc uint
	bits := 0
	idx := 25
	for i := 15; i >= 0; i-- {
		acc |= uint(b[i]) << bits
		bits += 8
		for bits >= 5 && idx >= 0 {
			out[idx] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			idx--
		}
	}
	if idx >= 0 {
		out[idx] = crockford[acc&31]
	}
	return string(out), nil
}

func defaultID(now time.Time) (string, error) { return NewULID(now, rand.Reader) }
