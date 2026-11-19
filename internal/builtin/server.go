package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
	"github.com/toxic-sunflower/lite-deploy/internal/servers"
)

// Server плагин `server`: реестр других серверов (имя и адрес), на которые ссылаются проекты.
type Server struct{}

func (Server) Name() string       { return "server" }
func (Server) Kind() string       { return "builtin" }
func (Server) Requires() []string { return nil }
func (Server) Description() string {
	return "server registry: names and addresses that project resources refer to (for example a database on another server)"
}
func (Server) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "list", Help: "known servers"},
		{Name: "add", Confirm: true, Args: "NAME ADDRESS", Help: "add or change a server (address on the internal network)"},
		{Name: "remove", Confirm: true, Args: "NAME", Help: "remove a server from the registry"},
	}
}

func (Server) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	m, err := servers.Load(env.FS)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
	}
	switch cmd {
	case "list":
		if len(args) != 0 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite server list")
		}
		var sb strings.Builder
		for _, n := range servers.Names(m) {
			fmt.Fprintf(&sb, "%s\t%s\n", n, m[n])
		}
		if sb.Len() == 0 {
			sb.WriteString("No servers.\n")
		}
		return plugin.Output{Text: sb.String(), Data: map[string]any{"servers": m}}, nil
	case "add":
		if len(args) != 2 || !servers.ValidName(args[0]) {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite server add NAME ADDRESS (name: lowercase letters, digits, \"-\", not \"local\")")
		}
		if err := servers.ValidAddress(args[1]); err != nil {
			return plugin.Output{}, plugin.Errorf("usage", "%v", err)
		}
		m[args[0]] = args[1]
		if err := servers.Save(env.FS, m); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "write the registry: %v", err)
		}
		return textOut("Server " + args[0] + " → " + args[1] + " saved.\n"), nil
	case "remove":
		if len(args) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite server remove NAME")
		}
		if _, ok := m[args[0]]; !ok {
			return plugin.Output{}, plugin.Errorf("precondition_failed", "server %s is not in the registry", args[0])
		}
		delete(m, args[0])
		if err := servers.Save(env.FS, m); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "write the registry: %v", err)
		}
		return textOut("Server " + args[0] + " removed from the registry.\n"), nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin server has no command %q", cmd)
}

func (Server) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	if _, err := servers.Load(env.FS); err != nil {
		return []plugin.Finding{finding("server", "registry", plugin.LevelFail, err.Error(), "")}
	}
	return nil
}
