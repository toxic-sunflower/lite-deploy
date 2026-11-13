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

const agentUsage = "использование: lite-deploy agent install [--admin пользователь]… | agent serve (запускает systemd)"

func cmdAgent(args []string, env Env) result {
	if len(args) == 0 {
		return fail("agent", CodeUsage, agentUsage)
	}
	switch args[0] {
	case "install":
		return agentInstall(args[1:], env)
	case "serve":
		return agentServe(args[1:], env)
	}
	return fail("agent", CodeUsage, agentUsage)
}

func agentInstall(args []string, env Env) result {
	fs := flag.NewFlagSet("agent install", flag.ContinueOnError)
	var admins listFlag
	fs.Var(&admins, "admin", "пользователь, которому разрешено управлять через агента (можно повторять)")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail("agent.install", CodeUsage, agentUsage)
	}
	if !env.root() {
		return fail("agent.install", "permission_denied", "установка агента требует root (sudo lite-deploy agent install --admin ВАШ_ПОЛЬЗОВАТЕЛЬ)")
	}
	bin, _ := os.Executable()
	if err := agent.Install(context.Background(), env.Runner, env.FS, bin, admins); err != nil {
		return fail("agent.install", CodeIO, err.Error())
	}
	return result{command: "agent.install", data: map[string]any{"admins": []string(admins), "socket": agent.APISocket},
		text: fmt.Sprintf("Агент установлен и запущен (сокет %s).\nАдминистраторы: %s. Новой группе нужен новый вход в систему (выйдите и зайдите по SSH заново).\n",
			agent.APISocket, strings.Join(admins, ", "))}
}

func agentServe(args []string, env Env) result {
	fs := flag.NewFlagSet("agent serve", flag.ContinueOnError)
	sock := fs.String("socket", "", "путь сокета (режим разработки без systemd)")
	execSock := fs.String("exec-socket", agent.ExecSocket, "сокет исполнителя")
	group := fs.String("group", agent.AdminGroup, "группа, которой разрешён доступ")
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
			return fail("agent.serve", CodeUsage, "агент запускается через systemd (lite-deploy-agent.socket); для разработки задайте --socket")
		}
		if ln, err = host.ListenUnix(*sock); err != nil {
			return fail("agent.serve", CodeIO, err.Error())
		}
	}
	var audit io.Writer
	if f, err := os.OpenFile(agent.AuditPath(env.Getenv("STATE_DIRECTORY")), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640); err == nil { //nolint:gosec // путь из каталога состояния службы
		defer f.Close()
		audit = f
	} else {
		_, _ = fmt.Fprintf(env.Stderr, "журнал агента недоступен: %v\n", err)
	}
	a := &agent.Agent{Version: Version, Audit: audit, Allowed: agent.GroupAllowed(env.FS, *group),
		ExecDial: func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", *execSock)
		}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := a.Serve(ctx, ln); err != nil {
		return fail("agent.serve", CodeIO, err.Error())
	}
	return result{command: "agent.serve", text: "Агент остановлен.\n"}
}

// cmdExec исполнитель: один запрос на стандартном вводе (сокет systemd), ответ на стандартный вывод.
func cmdExec(env Env) result {
	if !env.root() {
		return fail("exec", "permission_denied", "исполнитель запускается systemd от root")
	}
	run := func(args []string) []byte {
		var out bytes.Buffer
		e := realEnv(&out, io.Discard)
		RunEnv(args, e)
		return bytes.TrimSpace(out.Bytes())
	}
	var out bytes.Buffer
	agent.Exec(env.Stdin, &out, run, env.Stderr)
	return result{command: "exec", raw: bytes.TrimSpace(out.Bytes())}
}

// cmdCall клиент агента: lite-deploy call GET|POST /v1/путь (тело POST со стандартного ввода). Ответ агента выводится как есть.
func cmdCall(args []string, env Env) result {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 2 || (pos[0] != "GET" && pos[0] != "POST") || !strings.HasPrefix(pos[1], "/v1/") {
		return fail("call", CodeUsage, "использование: lite-deploy call GET|POST /v1/путь (тело POST читается со стандартного ввода)")
	}
	var body []byte
	if pos[0] == "POST" && env.Stdin != nil {
		body, err = io.ReadAll(io.LimitReader(env.Stdin, agent.MaxRequest))
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
			Hint: "агент не установлен или не запущен: sudo lite-deploy agent install --admin ВАШ_ПОЛЬЗОВАТЕЛЬ"}}
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 || out[0] != '{' {
		return fail("call", CodeIO, "агент вернул не JSON")
	}
	return result{command: "call", raw: out}
}
