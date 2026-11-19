// Package agent: the unprivileged agent (HTTP over a unix socket, D-31, D-32), the on-request executor (root, started
// systemd per connection) and the client. The executor accepts only requests from a fixed list and itself
// validates the parameters; there are no arbitrary commands.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// MaxRequest is the size limit of an ordinary request to the executor, in bytes; MaxBody is the body limit (a project create bundle).
const (
	MaxRequest = 8192
	MaxBody    = 128 * 1024
	// MaxPluginStdin is the limit of a plugin command standard input (a password, a key).
	MaxPluginStdin = 4096
)

var (
	projectRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	shaRe     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	specRe    = regexp.MustCompile(`^([0-9]{1,5})/(tcp|udp)$`)
	userRe    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	pluginRe  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
)

// Request is an agent request to the executor.
type Request struct {
	Cmd    string   `json:"cmd"`
	Args   []string `json:"args"`
	Caller string   `json:"caller"`
	// Body is a JSON bundle for project.preview and project.create; empty for other requests.
	Body string `json:"body,omitempty"`
}

func checkSpec(spec, from string) error {
	m := specRe.FindStringSubmatch(spec)
	if m == nil {
		return errors.New("the port is given as 8080/tcp")
	}
	n, _ := strconv.Atoi(m[1])
	if e := firewall.ValidateRule(n, m[2], from); e != nil {
		return errors.New(e.Message)
	}
	return nil
}

// CLIArgs checks a request against the list of allowed actions and returns the lite-deploy arguments (without --json).
// Everything not in the list is rejected.
func (r Request) CLIArgs() ([]string, error) {
	a := r.Args
	if r.Body != "" && r.Cmd != "project.preview" && r.Cmd != "project.create" && r.Cmd != "plugin.run" {
		return nil, fmt.Errorf("%s: a request body is not allowed", r.Cmd)
	}
	if r.Cmd == "plugin.run" && len(r.Body) > MaxPluginStdin {
		return nil, fmt.Errorf("plugin.run: standard input is larger than %d bytes", MaxPluginStdin)
	}
	need := func(n int) error {
		if len(a) != n {
			return fmt.Errorf("%s: expected %d arguments", r.Cmd, n)
		}
		return nil
	}
	switch r.Cmd {
	case "status":
		if len(a) == 0 {
			return []string{"status"}, nil
		}
		if err := need(1); err != nil {
			return nil, err
		}
		if !projectRe.MatchString(a[0]) {
			return nil, errors.New("invalid project name")
		}
		return []string{"status", a[0]}, nil
	case "deploy":
		if err := need(2); err != nil {
			return nil, err
		}
		if !projectRe.MatchString(a[0]) {
			return nil, errors.New("invalid project name")
		}
		if !shaRe.MatchString(a[1]) {
			return nil, errors.New("the full commit hash is required: 40 characters 0-9a-f")
		}
		return []string{"deploy", a[0], a[1]}, nil
	case "rollback":
		if err := need(1); err != nil {
			return nil, err
		}
		if !projectRe.MatchString(a[0]) {
			return nil, errors.New("invalid project name")
		}
		return []string{"rollback", a[0]}, nil
	case "project.preview", "project.create": // body: a bundle {project, ciphertexts, ...}; create applies, preview only describes
		if err := need(0); err != nil {
			return nil, err
		}
		if r.Body == "" || len(r.Body) > MaxBody || !json.Valid([]byte(r.Body)) {
			return nil, errors.New(r.Cmd + ": a body is required: a JSON bundle of at most 128 KiB")
		}
		if r.Cmd == "project.preview" {
			return []string{"project", "bundle", "--dry-run"}, nil
		}
		return []string{"project", "bundle", "--yes"}, nil
	case "fix": // fixes only what the list of safe fixes in lite fix allows
		if err := need(0); err != nil {
			return nil, err
		}
		return []string{"fix", "--yes"}, nil
	case "project.key":
		if err := need(0); err != nil {
			return nil, err
		}
		return []string{"secret", "pubkey"}, nil
	case "info", "verify":
		if err := need(0); err != nil {
			return nil, err
		}
		return []string{r.Cmd}, nil
	case "plugin.run": // arguments: plugin, command, command arguments; the interface gives the confirmation (--yes)
		if len(a) < 2 || !pluginRe.MatchString(a[0]) || !pluginRe.MatchString(a[1]) || !plugin.ValidArgs(a[2:]) {
			return nil, errors.New("plugin.run: a plugin name, a command and valid arguments are required")
		}
		return append(append([]string{"plugin", "run"}, a...), "--yes"), nil
	case "firewall.status":
		if err := need(0); err != nil {
			return nil, err
		}
		return []string{"firewall", "status"}, nil
	case "firewall.enable":
		if err := need(0); err != nil {
			return nil, err
		}
		return []string{"firewall", "enable", "--yes"}, nil
	case "firewall.allow": // arguments: port/protocol, source (empty: any)
		if err := need(2); err != nil {
			return nil, err
		}
		if err := checkSpec(a[0], a[1]); err != nil {
			return nil, err
		}
		out := []string{"firewall", "allow", a[0]}
		if a[1] != "" {
			out = append(out, "--from", a[1])
		}
		return append(out, "--yes"), nil
	case "firewall.remove": // arguments: port/protocol, source (empty: any), action
		if err := need(3); err != nil {
			return nil, err
		}
		if err := checkSpec(a[0], a[1]); err != nil {
			return nil, err
		}
		if a[2] != "allow" && a[2] != "limit" && a[2] != "deny" {
			return nil, errors.New("invalid rule action")
		}
		out := []string{"firewall", "remove", a[0], "--action", a[2]}
		if a[1] != "" && a[1] != "any" {
			out = append(out, "--from", a[1])
		}
		return append(out, "--yes"), nil
	}
	return nil, fmt.Errorf("action %q is not allowed", r.Cmd)
}

// ErrorJSON is a response envelope with an error (the `--json` format, SPEC-v0.md, 6.2).
func ErrorJSON(command, code, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"ok": false, "api_version": "v1", "command": command,
		"error": map[string]string{"code": code, "message": msg}})
	return b
}

// Exec handles one executor connection: reads one request, runs it and writes the answer.
// run runs a lite-deploy command with arguments (including --json) and returns its envelope.
// journal receives a line for every action (the systemd journal, which the agent cannot modify).
func Exec(r io.Reader, w io.Writer, run func(args []string) []byte, journal io.Writer) {
	ExecBody(r, w, func(args []string, _ []byte) []byte { return run(args) }, journal)
}

// ExecBody is like Exec, but run also receives the request body (the command standard input).
func ExecBody(r io.Reader, w io.Writer, run func(args []string, stdin []byte) []byte, journal io.Writer) {
	var req Request
	dec := json.NewDecoder(io.LimitReader(r, MaxRequest+2*MaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		_, _ = w.Write(ErrorJSON("exec", "usage", "invalid request"))
		return
	}
	args, err := req.CLIArgs()
	if err != nil {
		_, _ = fmt.Fprintf(journal, "rejected: cmd=%q caller=%q: %v\n", req.Cmd, req.Caller, err)
		_, _ = w.Write(ErrorJSON(req.Cmd, "usage", err.Error()))
		return
	}
	caller := req.Caller
	if !userRe.MatchString(caller) {
		caller = "?"
	}
	_, _ = fmt.Fprintf(journal, "running: caller=%s %s\n", caller, strings.Join(args, " "))
	out := run(append(args, "--json"), []byte(req.Body))
	_, _ = w.Write(out)
}
