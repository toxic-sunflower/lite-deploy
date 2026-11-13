package cli

import (
	"encoding/json"
	"io"

	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// APIVersion версия формата ответов (D-44).
const APIVersion = "v1"

// Warning предупреждение, не мешающее успеху.
type Warning struct {
	Code    string `json:"code" maxLen:"64"`
	Message string `json:"message" maxLen:"1024"`
}

// ErrorBody тело ошибки (SPEC-v0.md, 6.2).
type ErrorBody struct {
	Code    string         `json:"code" pattern:"^[a-z_]{1,64}$"`
	Message string         `json:"message" maxLen:"2048"`
	Details []schema.Issue `json:"details,omitempty"`
	Hint    string         `json:"hint,omitempty" maxLen:"1024"`
}

// Envelope конверт ответа `--json`: одна строка JSON.
type Envelope struct {
	OK         bool       `json:"ok"`
	APIVersion string     `json:"api_version" enum:"v1"`
	Command    string     `json:"command" pattern:"^[a-z_.]{1,64}$"`
	Data       any        `json:"data,omitempty"`
	Warnings   []Warning  `json:"warnings,omitempty"`
	Error      *ErrorBody `json:"error,omitempty"`
}

// Коды ошибок, которые знает CLI сверх кодов плана и привратника.
const (
	CodeInternal       = "internal"
	CodeIO             = "io_error"
	CodeUsage          = "usage"
	CodeNotImplemented = "not_implemented"
	CodeProjectMissing = "project_not_found"
)

var exitCodes = map[string]int{
	"internal": 1, "caddy_reload_failed": 1, "io_error": 1, "not_implemented": 1,
	"invalid_project": 2, "invalid_plan": 2, "unknown_action": 2, "usage": 2, "command_not_allowed": 2, "invalid_sha": 2, "unused_secret": 2, "invalid_secret": 2,
	"precondition_failed": 3, "secret_missing": 3, "project_exists_unmanaged": 3, "adopt_password_unknown": 3,
	"permission_denied": 4, "confirmation_required": 4, "protected_rule": 4,
	"step_failed":        5,
	"plan_hash_mismatch": 6, "plan_expired": 6, "plan_wrong_server": 6, "deploy_in_progress": 6, "quota_exceeded": 6,
	"project_not_found": 7, "secret_not_found": 7, "rule_not_found": 7,
	"deploy_unhealthy": 10, "deploy_failed": 10, "rollback_unavailable": 10, "nothing_deployed": 10,
}

// ExitCode код выхода процесса для кода ошибки (SPEC-v0.md, 6.3). Неизвестный код это общая ошибка 1.
func ExitCode(code string) int {
	if c, ok := exitCodes[code]; ok {
		return c
	}
	return 1
}

// ErrorCodes перечисляет все известные коды ошибок.
func ErrorCodes() []string {
	out := make([]string, 0, len(exitCodes))
	for c := range exitCodes {
		out = append(out, c)
	}
	return out
}

func writeJSON(w io.Writer, e Envelope) {
	b, _ := json.Marshal(e)
	_, _ = w.Write(append(b, '\n'))
}
