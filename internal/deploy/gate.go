// Package deploy: deploy, rollback, state and the gatekeeper (SPEC-v0.md, section 5).
// S3 has only the parsing of gatekeeper commands; blue-green arrives in S5.
package deploy

import (
	"regexp"
	"strings"
)

// Gatekeeper error codes.
const (
	CodeNotAllowed = "command_not_allowed"
	CodeInvalidSHA = "invalid_sha"
)

// GateError is a gatekeeper refusal.
type GateError struct {
	Code    string
	Message string
}

func (e *GateError) Error() string { return e.Code + ": " + e.Message }

// Verb is what the CI key is allowed to do.
type Verb string

// Allowed commands.
const (
	VerbDeploy   Verb = "deploy"
	VerbRollback Verb = "rollback"
	VerbStatus   Verb = "status"
)

// GateCommand is a parsed command.
type GateCommand struct {
	Verb Verb
	SHA  string // only for deploy
	JSON bool   // only for status
}

var shaRe = regexp.MustCompile(`\A[0-9a-f]{40}\z`)

// ParseGate parses SSH_ORIGINAL_COMMAND. Exactly these are allowed:
//
//	deploy <40 hex>, rollback, status, status --json.
//
// Everything else is rejected entirely: no partial matches, no surrounding spaces, no line breaks.
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
			return GateCommand{}, &GateError{Code: CodeInvalidSHA, Message: "the full commit hash is required: 40 characters 0-9a-f"}
		}
		return GateCommand{Verb: VerbDeploy, SHA: sha}, nil
	}
	return GateCommand{}, &GateError{Code: CodeNotAllowed, Message: "the command is not allowed (allowed: deploy <hash>, rollback, status)"}
}
