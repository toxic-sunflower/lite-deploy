// Package deploy: выкатка, откат, состояние и привратник (SPEC-v0.md, раздел 5).
// В S3 есть только разбор команд привратника; blue-green появится в S5.
package deploy

import (
	"regexp"
	"strings"
)

// Коды ошибок привратника.
const (
	CodeNotAllowed = "command_not_allowed"
	CodeInvalidSHA = "invalid_sha"
)

// GateError отказ привратника.
type GateError struct {
	Code    string
	Message string
}

func (e *GateError) Error() string { return e.Code + ": " + e.Message }

// Verb что разрешено ключу CI.
type Verb string

// Разрешённые команды.
const (
	VerbDeploy   Verb = "deploy"
	VerbRollback Verb = "rollback"
	VerbStatus   Verb = "status"
)

// GateCommand разобранная команда.
type GateCommand struct {
	Verb Verb
	SHA  string // только для deploy
	JSON bool   // только для status
}

var shaRe = regexp.MustCompile(`\A[0-9a-f]{40}\z`)

// ParseGate разбирает SSH_ORIGINAL_COMMAND. Разрешено ровно:
//
//	deploy <40 hex>, rollback, status, status --json.
//
// Всё остальное отклоняется целиком: никаких частичных совпадений, пробелов по краям, переводов строк.
func ParseGate(cmd string) (GateCommand, *GateError) {
	switch cmd {
	case "rollback":
		return GateCommand{Verb: VerbRollback}, nil
	case "status":
		return GateCommand{Verb: VerbStatus}, nil
	case "status --json":
		return GateCommand{Verb: VerbStatus, JSON: true}, nil
	}
	if sha, ok := strings.CutPrefix(cmd, "deploy "); ok {
		if !shaRe.MatchString(sha) {
			return GateCommand{}, &GateError{Code: CodeInvalidSHA, Message: "нужен полный хэш коммита: 40 символов 0-9a-f"}
		}
		return GateCommand{Verb: VerbDeploy, SHA: sha}, nil
	}
	return GateCommand{}, &GateError{Code: CodeNotAllowed, Message: "команда не разрешена (можно: deploy <хэш>, rollback, status)"}
}
