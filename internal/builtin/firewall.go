package builtin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// Firewall плагин `firewall`: файервол сервера (ufw).
type Firewall struct{}

func (Firewall) Name() string       { return "firewall" }
func (Firewall) Kind() string       { return "builtin" }
func (Firewall) Requires() []string { return nil }
func (Firewall) Description() string {
	return "firewall (ufw): mode off, http, max; ports; SSH, 80 and 443 are protected from closing"
}
func (Firewall) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "status", Root: true, Help: "whether the firewall is on, the SSH port, the rules"},
		{Name: "mode", Help: "mode: off (no filtering), http (SSH and web ports), max (SSH only); the previous rules are reset", Args: "off|http|max", Confirm: true},
		{Name: "allow", Help: "open a port", Args: "PORT/tcp|udp [--from ADDRESS]", Confirm: true},
		{Name: "remove", Help: "close a port", Args: "PORT/tcp|udp [--from ADDRESS] [--action allow|limit|deny]", Confirm: true},
		{Name: "enable", Help: "enable the firewall: SSH, 80 and 443 are open, other incoming traffic is closed", Confirm: true},
	}
}

func fwErr(e *firewall.Error) error { return &plugin.Error{Code: e.Code, Message: e.Message} }

func fwOut(st *firewall.State) plugin.Output {
	var sb strings.Builder
	state := "off"
	if st.Active {
		state = "on"
	}
	fmt.Fprintf(&sb, "Firewall %s, SSH port %d\n", state, st.SSHPort)
	for _, r := range st.Rules {
		mark := ""
		if r.Protected {
			mark = "  (protected)"
		}
		fmt.Fprintf(&sb, "  %-6s %-14s from %s%s\n", r.Action, r.Raw0(), r.From, mark)
	}
	return plugin.Output{Text: sb.String(), Data: st}
}

// splitFlags отделяет --from и --action от позиционных аргументов.
func splitFlags(args []string) (pos []string, from, action string, err error) {
	action = "allow"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from", "--action":
			if i+1 >= len(args) {
				return nil, "", "", plugin.Errorf("usage", "%s needs a value", args[i])
			}
			if args[i] == "--from" {
				from = args[i+1]
			} else {
				action = args[i+1]
			}
			i++
		default:
			pos = append(pos, args[i])
		}
	}
	return pos, from, action, nil
}

func (Firewall) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	m := &firewall.Manager{Runner: env.Runner}
	pos, from, action, err := splitFlags(args)
	if err != nil {
		return plugin.Output{}, err
	}
	var st *firewall.State
	var fe *firewall.Error
	switch cmd {
	case "status", "enable":
		if len(pos) != 0 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite firewall %s", cmd)
		}
		if cmd == "status" {
			st, fe = m.Status(ctx)
		} else {
			st, fe = m.Enable(ctx)
		}
	case "mode":
		if len(pos) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite firewall mode off|http|max")
		}
		if fe = m.SetMode(ctx, pos[0]); fe == nil {
			st, fe = m.Status(ctx)
		}
	case "allow", "remove":
		if len(pos) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite firewall %s PORT/tcp|udp [--from ADDRESS]", cmd)
		}
		p, proto, ok := strings.Cut(pos[0], "/")
		port, perr := strconv.Atoi(p)
		if !ok || perr != nil {
			return plugin.Output{}, plugin.Errorf("usage", "the port is given as 8080/tcp")
		}
		if cmd == "allow" {
			st, fe = m.Allow(ctx, port, proto, from)
		} else {
			st, fe = m.Remove(ctx, port, proto, from, action)
		}
	default:
		return plugin.Output{}, plugin.Errorf("usage", "plugin firewall has no command %q", cmd)
	}
	if fe != nil {
		return plugin.Output{}, fwErr(fe)
	}
	return fwOut(st), nil
}

func (Firewall) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	st, fe := (&firewall.Manager{Runner: env.Runner}).Status(ctx)
	if fe != nil || st == nil {
		return []plugin.Finding{finding("firewall", "status", plugin.LevelWarn, "the firewall is not installed or unavailable (ufw)", "sudo lite firewall mode max")}
	}
	if !st.Active {
		return []plugin.Finding{finding("firewall", "status", plugin.LevelWarn, "the firewall is off: incoming traffic is not filtered", "sudo lite firewall mode max")}
	}
	return []plugin.Finding{finding("firewall", "status", plugin.LevelOK, fmt.Sprintf("the firewall is on, SSH port %d", st.SSHPort), "")}
}
