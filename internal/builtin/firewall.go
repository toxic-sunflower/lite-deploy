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
	return "файервол (ufw): режим off, http, max; порты; SSH, 80 и 443 защищены от закрытия"
}
func (Firewall) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "status", Root: true, Help: "включён ли файервол, порт SSH, правила"},
		{Name: "mode", Help: "режим: off (без фильтрации), http (SSH и веб-порты), max (только SSH); прежние правила сбрасываются", Args: "off|http|max", Confirm: true},
		{Name: "allow", Help: "открыть порт", Args: "ПОРТ/tcp|udp [--from АДРЕС]", Confirm: true},
		{Name: "remove", Help: "закрыть порт", Args: "ПОРТ/tcp|udp [--from АДРЕС] [--action allow|limit|deny]", Confirm: true},
		{Name: "enable", Help: "включить файервол: открыты SSH, 80 и 443, остальное входящее закрыто", Confirm: true},
	}
}

func fwErr(e *firewall.Error) error { return &plugin.Error{Code: e.Code, Message: e.Message} }

func fwOut(st *firewall.State) plugin.Output {
	var sb strings.Builder
	state := "выключен"
	if st.Active {
		state = "включён"
	}
	fmt.Fprintf(&sb, "Файервол %s, порт SSH %d\n", state, st.SSHPort)
	for _, r := range st.Rules {
		mark := ""
		if r.Protected {
			mark = "  (защищено)"
		}
		fmt.Fprintf(&sb, "  %-6s %-14s от %s%s\n", r.Action, r.Raw0(), r.From, mark)
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
				return nil, "", "", plugin.Errorf("usage", "после %s нужно значение", args[i])
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
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite firewall %s", cmd)
		}
		if cmd == "status" {
			st, fe = m.Status(ctx)
		} else {
			st, fe = m.Enable(ctx)
		}
	case "mode":
		if len(pos) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite firewall mode off|http|max")
		}
		if fe = m.SetMode(ctx, pos[0]); fe == nil {
			st, fe = m.Status(ctx)
		}
	case "allow", "remove":
		if len(pos) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite firewall %s ПОРТ/tcp|udp [--from АДРЕС]", cmd)
		}
		p, proto, ok := strings.Cut(pos[0], "/")
		port, perr := strconv.Atoi(p)
		if !ok || perr != nil {
			return plugin.Output{}, plugin.Errorf("usage", "порт задаётся как 8080/tcp")
		}
		if cmd == "allow" {
			st, fe = m.Allow(ctx, port, proto, from)
		} else {
			st, fe = m.Remove(ctx, port, proto, from, action)
		}
	default:
		return plugin.Output{}, plugin.Errorf("usage", "у плагина firewall нет команды %q", cmd)
	}
	if fe != nil {
		return plugin.Output{}, fwErr(fe)
	}
	return fwOut(st), nil
}

func (Firewall) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	st, fe := (&firewall.Manager{Runner: env.Runner}).Status(ctx)
	if fe != nil || st == nil {
		return []plugin.Finding{finding("firewall", "status", plugin.LevelWarn, "файервол не установлен или недоступен (ufw)", "sudo lite firewall mode max")}
	}
	if !st.Active {
		return []plugin.Finding{finding("firewall", "status", plugin.LevelWarn, "файервол выключен: входящий трафик не фильтруется", "sudo lite firewall mode max")}
	}
	return []plugin.Finding{finding("firewall", "status", plugin.LevelOK, fmt.Sprintf("файервол включён, порт SSH %d", st.SSHPort), "")}
}
