package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/agent"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Internal commands: install.sh and systemd run them, they are not needed by hand.
const agentUsage = "usage: lite-deploy agent install | agent uninstall (internal, called by liteup) | agent serve (started by systemd)"

func cmdAgent(args []string, env Env) result {
	if len(args) == 0 {
		return fail("agent", CodeUsage, agentUsage)
	}
	switch args[0] {
	case "uninstall":
		return agentUninstall(args[1:], env)
	case "install":
		return agentInstall(args[1:], env)
	case "serve":
		return agentServe(args[1:], env)
	}
	return fail("agent", CodeUsage, agentUsage)
}

func agentInstall(args []string, env Env) result {
	fs := flag.NewFlagSet("agent install", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail("agent.install", CodeUsage, agentUsage)
	}
	if !env.root() {
		return fail("agent.install", "permission_denied", "installing the agent requires root; it is part of the server install: curl -fsSL https://lite-deploy.prozorov.me/liteup | sudo sh")
	}
	bin, _ := os.Executable()
	group, err := agent.Install(context.Background(), env.Runner, env.FS, bin)
	if err != nil {
		return fail("agent.install", CodeIO, err.Error())
	}
	return result{command: "agent.install", data: map[string]any{"group": group, "socket": agent.APISocket},
		text: fmt.Sprintf("Agent installed and running (socket %s). System administrators (group %s) can manage it.\n", agent.APISocket, group)}
}

func agentUninstall(args []string, env Env) result {
	fs := flag.NewFlagSet("agent uninstall", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail("agent.uninstall", CodeUsage, agentUsage)
	}
	if !env.root() {
		return fail("agent.uninstall", "permission_denied", "uninstalling the agent requires root; use: sudo liteup uninstall")
	}
	if err := agent.Uninstall(context.Background(), env.Runner, env.FS); err != nil {
		return fail("agent.uninstall", CodeIO, err.Error())
	}
	return result{command: "agent.uninstall", text: "Agent removed. Project data, secrets and backups were not touched.\n"}
}

func agentServe(args []string, env Env) result {
	fs := flag.NewFlagSet("agent serve", flag.ContinueOnError)
	sock := fs.String("socket", "", "socket path (development mode without systemd)")
	execSock := fs.String("exec-socket", agent.ExecSocket, "executor socket")
	group := fs.String("group", agent.DefaultGroup, "the group allowed to use the agent")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail("agent.serve", CodeUsage, agentUsage)
	}
	ln, inherited, err := host.InheritedListener(env.Getenv, os.Getpid())
	if err != nil {
		return fail("agent.serve", CodeIO, err.Error())
	}
	if !inherited {
		if *sock == "" {
			return fail("agent.serve", CodeUsage, "the agent is started by systemd (lite-deploy-agent.socket); pass --socket for development")
		}
		if ln, err = host.ListenUnix(*sock); err != nil {
			return fail("agent.serve", CodeIO, err.Error())
		}
	}
	var audit io.Writer
	if f, err := os.OpenFile(agent.AuditPath(env.Getenv("STATE_DIRECTORY")), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640); err == nil { //nolint:gosec // a path from the service state directory
		defer func() { _ = f.Close() }()
		audit = f
	} else {
		_, _ = fmt.Fprintf(env.Stderr, "the agent journal is unavailable: %v\n", err)
	}
	limit := 0
	if cfg, bad := loadConfig(env, "agent.serve"); bad == nil {
		limit = cfg.Quotas.ApplyPerHour
	}
	a := &agent.Agent{Version: Version, Audit: audit, ApplyPerHour: limit, Allowed: agent.GroupAllowed(env.FS, *group),
		ExecDial: func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", *execSock)
		}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := a.Serve(ctx, ln); err != nil {
		return fail("agent.serve", CodeIO, err.Error())
	}
	return result{command: "agent.serve", text: "Agent stopped.\n"}
}

// cmdExec is the executor: one request on standard input (a systemd socket), the answer on standard output.
func cmdExec(env Env) result {
	if !env.root() {
		return fail("exec", "permission_denied", "the executor is started by systemd as root")
	}
	run := func(args []string, stdin []byte) []byte {
		var out bytes.Buffer
		e := realEnv(&out, io.Discard)
		e.Stdin = bytes.NewReader(stdin)
		RunEnv(args, e)
		return bytes.TrimSpace(out.Bytes())
	}
	var out bytes.Buffer
	agent.ExecBody(env.Stdin, &out, run, env.Stderr)
	return result{command: "exec", raw: bytes.TrimSpace(out.Bytes())}
}

// cmdCall is the agent client: lite-deploy call GET|POST /v1/path (the POST body on standard input). The agent answer is printed as is.
func cmdCall(args []string, env Env) result {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 2 || (pos[0] != "GET" && pos[0] != "POST") || !strings.HasPrefix(pos[1], "/v1/") {
		return fail("call", CodeUsage, "usage: lite-deploy call GET|POST /v1/path (the POST body is read from standard input)")
	}
	var body []byte
	if pos[0] == "POST" && env.Stdin != nil {
		body, err = io.ReadAll(io.LimitReader(env.Stdin, agent.MaxRequest+agent.MaxBody))
		if err != nil {
			return fail("call", CodeIO, err.Error())
		}
	}
	sock := agent.APISocket
	if env.Getenv != nil && env.Getenv("LITE_DEPLOY_SOCK") != "" {
		sock = env.Getenv("LITE_DEPLOY_SOCK")
	}
	out, err := agent.Call(context.Background(), sock, pos[0], pos[1], body)
	if err != nil {
		return result{command: "call", err: &ErrorBody{Code: "precondition_failed", Message: err.Error(),
			Hint: "the agent is installed by liteup: curl -fsSL https://lite-deploy.prozorov.me/liteup | sudo sh; the user must be a system administrator (sudo or wheel)"}}
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 || out[0] != '{' {
		return fail("call", CodeIO, "the agent returned something that is not JSON")
	}
	return result{command: "call", raw: out}
}
