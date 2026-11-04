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

// SchemaID идентификатор версии схемы плана.
const SchemaID = "lite-deploy/plan/v1"

// KindProjectCreate единственный вид плана в v0.
const KindProjectCreate = "project.create"

// Plan список шагов, которые применит исполнитель (D-34).
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
	Hash        string `json:"hash" pattern:"^sha256:[0-9a-f]{64}$" doc:"sha256 от канонического JSON плана без поля hash"`
}

// Step одно действие исполнителя. Исполнитель смотрит только на Action и Params, Summary нужен людям.
type Step struct {
	ID      string          `json:"id" pattern:"^s[0-9]{2,3}$"`
	Action  string          `json:"action" pattern:"^[a-z_]{1,64}$"`
	Class   string          `json:"class" enum:"safe,normal,dangerous"`
	Params  json.RawMessage `json:"params"`
	Summary string          `json:"summary" maxLen:"500"`
}

// Limits пределы плана (SPEC-v0.md, раздел 8).
type Limits struct {
	PlanBytes    int
	PlanSteps    int
	PlanTTLHours int
}

// DefaultLimits значения по умолчанию.
func DefaultLimits() Limits { return Limits{PlanBytes: 256 * 1024, PlanSteps: 200, PlanTTLHours: 168} }

// ServerID вычисляет идентификатор сервера из /etc/machine-id: sha256("lite-deploy:" + id).
func ServerID(machineID string) string {
	return HashBytes([]byte("lite-deploy:" + strings.TrimSpace(machineID)))
}

// ComputeHash считает хэш плана без поля hash.
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

// Seal проставляет хэш.
func (p *Plan) Seal() error {
	h, err := p.ComputeHash()
	if err != nil {
		return err
	}
	p.Hash = h
	return nil
}

// Коды ошибок проверки плана (SPEC-v0.md, 6.3).
const (
	CodeInvalidPlan  = "invalid_plan"
	CodeHashMismatch = "plan_hash_mismatch"
	CodeExpired      = "plan_expired"
	CodeWrongServer  = "plan_wrong_server"
)

// VerifyError ошибка проверки плана.
type VerifyError struct {
	Code    string
	Message string
	Issues  []schema.Issue
}

func (e *VerifyError) Error() string { return e.Code + ": " + e.Message }

func invalid(msg string, issues ...schema.Issue) *VerifyError {
	return &VerifyError{Code: CodeInvalidPlan, Message: msg, Issues: issues}
}

// VerifyOptions что известно проверяющему о текущем сервере и времени.
type VerifyOptions struct {
	Now      time.Time
	ServerID string
	Limits   Limits
}

// Verify разбирает и проверяет план по шагам раздела 3 («Применение»): схема, размер, шаги, хэш, срок, сервер.
// Проверка идёт в таком порядке, чтобы чужой или повреждённый план отклонялся раньше, чем что-либо будет выполняться.
func Verify(data []byte, o VerifyOptions) (*Plan, *VerifyError) {
	if o.Limits == (Limits{}) {
		o.Limits = DefaultLimits()
	}
	if len(data) > o.Limits.PlanBytes {
		return nil, invalid(fmt.Sprintf("план больше %d байт", o.Limits.PlanBytes))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		return nil, invalid("план не разобран: " + err.Error())
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid("после плана есть лишние данные")
	}
	issues := schema.Check(&p)
	if len(p.Steps) > o.Limits.PlanSteps {
		issues = append(issues, schema.Issue{Path: "/steps", Code: "maxItems", Message: fmt.Sprintf("шагов больше %d", o.Limits.PlanSteps)})
	}
	if len(issues) > 0 {
		return nil, invalid("план не прошёл проверку", issues...)
	}

	want, err := p.ComputeHash()
	if err != nil {
		return nil, invalid("хэш не посчитан: " + err.Error())
	}
	if want != p.Hash {
		return nil, &VerifyError{Code: CodeHashMismatch, Message: fmt.Sprintf("хэш плана не совпадает: в файле %s, посчитан %s", p.Hash, want)}
	}

	created, err1 := time.Parse(time.RFC3339, p.CreatedAt)
	expires, err2 := time.Parse(time.RFC3339, p.ExpiresAt)
	if err1 != nil || err2 != nil {
		return nil, invalid("время в плане не разобрано")
	}
	if !expires.After(created) || expires.Sub(created) > time.Duration(o.Limits.PlanTTLHours)*time.Hour {
		return nil, invalid(fmt.Sprintf("срок жизни плана должен быть положительным и не больше %d ч", o.Limits.PlanTTLHours))
	}
	if !o.Now.Before(expires) {
		return nil, &VerifyError{Code: CodeExpired, Message: "срок действия плана истёк: " + p.ExpiresAt}
	}
	if p.ServerID != o.ServerID {
		return nil, &VerifyError{Code: CodeWrongServer, Message: "план построен для другого сервера"}
	}
	// Шаги проверяются последними: к этому моменту план целый, свежий и для этого сервера.
	if issues := checkSteps(&p); len(issues) > 0 {
		return nil, invalid("шаги плана не прошли проверку", issues...)
	}
	return &p, nil
}

func checkSteps(p *Plan) []schema.Issue {
	var out []schema.Issue
	for i, s := range p.Steps {
		base := fmt.Sprintf("/steps/%d", i)
		if want := fmt.Sprintf("s%02d", i+1); s.ID != want {
			out = append(out, schema.Issue{Path: base + "/id", Code: "sequence", Message: "ожидался id " + want})
		}
		def, ok := actions.Lookup(s.Action)
		if !ok {
			out = append(out, schema.Issue{Path: base + "/action", Code: "unknown_action", Message: fmt.Sprintf("действие %q не входит в список исполнителя", s.Action)})
			continue
		}
		if def.Class != s.Class {
			out = append(out, schema.Issue{Path: base + "/class", Code: "class_mismatch", Message: fmt.Sprintf("класс действия %s: %s", s.Action, def.Class)})
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
			out = append(out, schema.Issue{Path: base + "/params/project", Code: "foreign_project", Message: "шаг относится к другому проекту, чем план"})
		}
	}
	return out
}

// Describe возвращает строки для чтения человеком: «s03 provision_postgres: описание».
func Describe(p *Plan) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, fmt.Sprintf("%s %s: %s", s.ID, s.Action, s.Summary))
	}
	return out
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID создаёт ULID: 48 бит времени (мс) и 80 бит случайности.
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
	// 128 бит → 26 символов по 5 бит (старшие 2 бита первого символа нулевые)
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
