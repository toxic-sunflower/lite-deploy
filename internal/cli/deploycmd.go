package cli

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/deploy"
)

// deployEnv собирает окружение выкатки из окружения CLI.
func deployEnv(env Env) deploy.Env {
	asUser := env.IsRoot != nil && env.IsRoot()
	sleep := env.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	paths := env.Paths
	return deploy.Env{Runner: env.Runner, FS: env.FS, HTTP: env.HTTP, Lock: env.Lock, Now: env.Now, Sleep: sleep,
		AsUser: asUser, StateDir: paths.StateDir}
}

func deployFail(command string, e *deploy.Error) result {
	return result{command: command, err: &ErrorBody{Code: e.Code, Message: e.Message, Hint: e.Hint}}
}

func openProject(command, name string, env Env) (*deploy.Manager, *result) {
	m, e := deploy.Open(deployEnv(env), name)
	if e != nil {
		r := deployFail(command, e)
		return nil, &r
	}
	return m, nil
}

func cmdDeploy(args []string, env Env) result {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 2 {
		return fail("deploy", CodeUsage, "использование: lite-deploy deploy <проект> <хэш коммита из 40 символов>")
	}
	return doDeploy(pos[0], pos[1], env)
}

func doDeploy(name, sha string, env Env) result {
	m, r := openProject("deploy", name, env)
	if r != nil {
		return *r
	}
	res, e := m.Deploy(context.Background(), sha)
	if e != nil {
		return deployFail("deploy", e)
	}
	return result{command: "deploy", data: res,
		text: fmt.Sprintf("Выкачено %s, проект %s, слот %s (был %s). Трафик переключён.\n", short(res.Version), res.Project, res.Slot, res.PreviousSlot)}
}

func cmdRollback(args []string, env Env) result {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		return fail("rollback", CodeUsage, "использование: lite-deploy rollback <проект>")
	}
	return doRollback(pos[0], env)
}

func doRollback(name string, env Env) result {
	m, r := openProject("rollback", name, env)
	if r != nil {
		return *r
	}
	res, e := m.Rollback(context.Background())
	if e != nil {
		return deployFail("rollback", e)
	}
	return result{command: "rollback", data: res,
		text: fmt.Sprintf("Откат выполнен: проект %s, слот %s (был %s), версия %s.\n", res.Project, res.Slot, res.PreviousSlot, short(res.Version))}
}

func cmdStatus(args []string, env Env) result {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) > 1 {
		return fail("status", CodeUsage, "использование: lite-deploy status [проект]")
	}
	return doStatus(pos, env)
}

func doStatus(pos []string, env Env) result {
	var names []string
	if len(pos) == 1 {
		names = pos
	} else {
		entries, err := env.FS.ReadDir(deploy.DefaultProjectsDir)
		if err != nil {
			return fail("status", CodeProjectMissing, "проектов ещё нет")
		}
		for _, e := range entries {
			if n, ok := strings.CutSuffix(e, ".json"); ok {
				names = append(names, n)
			}
		}
		sort.Strings(names)
	}
	var all []*deploy.StatusResult
	var sb strings.Builder
	for _, n := range names {
		m, r := openProject("status", n, env)
		if r != nil {
			return *r
		}
		st, e := m.Status(context.Background())
		if e != nil {
			return deployFail("status", e)
		}
		all = append(all, st)
		sb.WriteString(statusText(st))
	}
	if len(pos) == 1 {
		return result{command: "status", data: all[0], text: sb.String()}
	}
	return result{command: "status", data: map[string]any{"projects": all}, text: sb.String()}
}

func statusText(st *deploy.StatusResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Проект %s, активный слот: %s\n", st.Project, st.Active)
	for _, slot := range deploy.Slots {
		var svcs []string
		for s := range st.Slots[slot] {
			svcs = append(svcs, s)
		}
		sort.Strings(svcs)
		for _, s := range svcs {
			x := st.Slots[slot][s]
			state := "не отвечает"
			if x.Up {
				state = "работает"
				if x.Version != nil {
					state += ", версия " + short(*x.Version)
				}
			}
			fmt.Fprintf(&sb, "  %-5s %-10s порт %d: %s\n", slot, s, x.Port, state)
		}
	}
	for _, h := range st.History {
		fmt.Fprintf(&sb, "  %s %s %s слот %s\n", h.Time, h.Event, short(h.Version), h.Slot)
	}
	return sb.String()
}

// cmdGate привратник: команда из SSH_ORIGINAL_COMMAND (SPEC-v0.md, 5.5).
func cmdGate(args []string, env Env) result {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	name := fs.String("project", "", "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 || *name == "" {
		return fail("gate", CodeUsage, "использование: lite-deploy gate --project <проект> (команду даёт SSH_ORIGINAL_COMMAND)")
	}
	cmd := ""
	if env.Getenv != nil {
		cmd = env.Getenv("SSH_ORIGINAL_COMMAND")
	}
	gc, gerr := deploy.ParseGate(cmd)
	if gerr != nil {
		return fail("gate", gerr.Code, gerr.Message)
	}
	switch gc.Verb {
	case deploy.VerbDeploy:
		return doDeploy(*name, gc.SHA, env)
	case deploy.VerbRollback:
		return doRollback(*name, env)
	}
	return doStatus([]string{*name}, env)
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
