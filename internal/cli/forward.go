package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/user"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/agent"
	"github.com/toxic-sunflower/lite-deploy/internal/deploy"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// Commands that need root rights are run by a regular administrator (group sudo or wheel) without sudo:
// the CLI hands them to the agent, and the executor (root, started by systemd) performs only actions from a fixed list.
// Under root (sudo) commands still run in place.

func (e Env) agentSock() string {
	if e.Getenv != nil && e.Getenv("LITE_DEPLOY_SOCK") != "" {
		return e.Getenv("LITE_DEPLOY_SOCK")
	}
	return agent.APISocket
}

// callAgent sends a request to the agent and returns the response envelope.
func callAgent(env Env, method, path string, body any) ([]byte, *ErrorBody) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	call := env.AgentCall
	if call == nil {
		call = agent.Call
	}
	out, err := call(context.Background(), env.agentSock(), method, path, raw)
	if err != nil {
		return nil, &ErrorBody{Code: "precondition_failed", Message: err.Error(),
			Hint: "the agent is installed with the server (install.sh); the user must be a system administrator (group sudo or wheel). Or run the command with sudo"}
	}
	return []byte(strings.TrimSpace(string(out))), nil
}

// fromEnvelope turns an agent answer into a command result (for readable output the data is printed as is).
func fromEnvelope(raw []byte, command string) result {
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return fail(command, CodeIO, "the agent returned something that is not JSON")
	}
	if e.Command != "" && command == "" {
		command = e.Command
	}
	r := result{command: command, data: e.Data, warn: e.Warnings, err: e.Error}
	if e.OK {
		r.text = renderData(e.Data)
	}
	return r
}

func renderData(d any) string {
	switch v := d.(type) {
	case nil:
		return "Done.\n"
	case map[string]any:
		if s, ok := v["output"].(string); ok {
			return strings.TrimRight(s, "\n") + "\n"
		}
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	return string(b) + "\n"
}

// pipedStdin reads standard input if it is not a terminal (this is how passwords and keys are passed, not as arguments).
func pipedStdin(env Env) (string, error) {
	if env.Stdin == nil || (env.Interactive != nil && env.Interactive()) {
		return "", nil
	}
	b, err := io.ReadAll(io.LimitReader(env.Stdin, agent.MaxPluginStdin+1))
	if err != nil {
		return "", err
	}
	if len(b) > agent.MaxPluginStdin {
		return "", fmt.Errorf("standard input is larger than %d bytes", agent.MaxPluginStdin)
	}
	return string(b), nil
}

// forwardPlugin runs a plugin command through the agent.
func forwardPlugin(env Env, command, plug, cmd string, args []string) result {
	in, err := pipedStdin(env)
	if err != nil {
		return fail(command, CodeUsage, err.Error())
	}
	if args == nil {
		args = []string{}
	}
	body := map[string]any{"plugin": plug, "command": cmd, "args": args}
	if in != "" {
		body["stdin"] = in
	}
	raw, bad := callAgent(env, "POST", "/v1/plugin", body)
	if bad != nil {
		return result{command: command, err: bad}
	}
	return fromEnvelope(raw, command)
}

// forwardFix runs the safe fixes through the agent.
func forwardFix(env Env) result {
	raw, bad := callAgent(env, "POST", "/v1/fix", map[string]string{})
	if bad != nil {
		return result{command: "fix", err: bad}
	}
	return fromEnvelope(raw, "fix")
}

// forwardProjectCreate creates a project without sudo: the plan preview and the creation go through the agent.
func forwardProjectCreate(env Env, file string, yes, rotate bool, cts []string) result {
	const command = "project.create"
	data, err := readInput(env, file)
	if err != nil {
		return fail(command, CodeIO, "could not read the project description: "+err.Error())
	}
	ciphertexts := map[string]string{}
	for _, c := range cts {
		name, path, ok := strings.Cut(c, "=")
		if !ok || name == "" || path == "" {
			return fail(command, CodeUsage, "--ciphertext expects name=file")
		}
		b, err := env.FS.ReadFile(path)
		if err != nil {
			return fail(command, CodeIO, "could not read the ciphertext "+name+": "+err.Error())
		}
		ciphertexts[name] = strings.TrimSpace(string(b))
	}
	return forwardProjectData(env, command, data, yes, rotate, ciphertexts)
}

// forwardProjectData previews and applies a project description through the agent.
func forwardProjectData(env Env, command string, data []byte, yes, rotate bool, ciphertexts map[string]string) result {
	bundle := map[string]any{"project": json.RawMessage(data), "ciphertexts": ciphertexts, "rotate_password": rotate}
	raw, bad := callAgent(env, "POST", "/v1/project/preview", bundle)
	if bad != nil {
		return result{command: command, err: bad}
	}
	prev := fromEnvelope(raw, command)
	if prev.err != nil {
		return prev
	}
	if !yes {
		if env.Interactive == nil || !env.Interactive() {
			return fail(command, "confirmation_required", "without a terminal, applying requires the --yes flag")
		}
		_, _ = fmt.Fprint(env.Stderr, "Preview (nothing changed):\n"+previewSteps(prev.data)+"\nType yes to apply: ")
		line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			return fail(command, "confirmation_required", "applying was canceled: no confirmation received")
		}
	}
	if m, ok := prev.data.(map[string]any); ok {
		if h, ok := m["project_hash"].(string); ok {
			bundle["expect_project_hash"] = h
		}
	}
	raw, bad = callAgent(env, "POST", "/v1/project/create", bundle)
	if bad != nil {
		return result{command: command, err: bad}
	}
	return fromEnvelope(raw, command)
}

func previewSteps(d any) string {
	m, _ := d.(map[string]any)
	steps, _ := m["steps"].([]any)
	var sb strings.Builder
	for _, s := range steps {
		if st, ok := s.(map[string]any); ok {
			fmt.Fprintf(&sb, "  %v  %-20v %v\n", st["id"], st["action"], st["state"])
		}
	}
	return sb.String()
}

// forwardVerify runs verify through the agent. false: the agent is unavailable, the check has to run on the spot (with incomplete rights).
func forwardVerify(env Env) (result, bool) {
	raw, bad := callAgent(env, "GET", "/v1/verify", nil)
	if bad != nil {
		return result{}, false
	}
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return result{}, false
	}
	m, ok := e.Data.(map[string]any)
	if !ok {
		return fromEnvelope(raw, "verify"), true
	}
	b, _ := json.Marshal(m["findings"])
	var findings []plugin.Finding
	if json.Unmarshal(b, &findings) != nil {
		return fromEnvelope(raw, "verify"), true
	}
	r := verifyResult(findings)
	return r, true
}

func (e Env) currentUser() string {
	if e.CurrentUser != nil {
		return e.CurrentUser()
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

// viaAgent: a deploy and a rollback of a project are run by the project user itself (CI through the gatekeeper) or by root.
// An administrator who is neither passes the command to the agent: the executor runs it as the project.
func (e Env) viaAgent(project string) bool { return !e.root() && e.currentUser() != project }

func forwardProject(env Env, command, path string, body map[string]string) result {
	raw, bad := callAgent(env, "POST", path, body)
	if bad != nil {
		return result{command: command, err: bad}
	}
	r := fromEnvelope(raw, command)
	if m, ok := r.data.(map[string]any); ok && r.err == nil {
		switch command {
		case "deploy":
			r.text = fmt.Sprintf("Deployed %v, project %v, slot %v (was %v). Traffic switched.\n", short(fmt.Sprint(m["version"])), m["project"], m["slot"], m["previous_slot"])
		case "rollback":
			r.text = fmt.Sprintf("Rollback done: project %v, slot %v (was %v), version %v.\n", m["project"], m["slot"], m["previous_slot"], short(fmt.Sprint(m["version"])))
		}
	}
	return r
}

// forwardStatus shows the state of projects through the agent.
func forwardStatus(env Env, pos []string) result {
	path := "/v1/status"
	if len(pos) == 1 {
		path += "?project=" + pos[0]
	}
	raw, bad := callAgent(env, "GET", path, nil)
	if bad != nil {
		return result{command: "status", err: bad}
	}
	r := fromEnvelope(raw, "status")
	if r.err != nil {
		return r
	}
	b, _ := json.Marshal(r.data)
	var text strings.Builder
	var multi struct {
		Projects []*deploy.StatusResult `json:"projects"`
	}
	if json.Unmarshal(b, &multi) == nil && multi.Projects != nil {
		for _, p := range multi.Projects {
			text.WriteString(statusText(p))
		}
	} else {
		var one deploy.StatusResult
		if json.Unmarshal(b, &one) == nil && one.Project != "" {
			text.WriteString(statusText(&one))
		}
	}
	if text.Len() > 0 {
		r.text = text.String()
	}
	return r
}
