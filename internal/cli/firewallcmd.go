package cli

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
)

const firewallUsage = "использование: lite-deploy firewall status | allow <порт>/<tcp|udp> [--from адрес] --yes | remove <порт>/<tcp|udp> [--from адрес] [--action allow|limit|deny] --yes | enable --yes"

func firewallFail(command string, e *firewall.Error) result {
	return result{command: command, err: &ErrorBody{Code: e.Code, Message: e.Message}}
}

// parsePortSpec разбирает «8080/tcp».
func parsePortSpec(s string) (int, string, bool) {
	p, proto, ok := strings.Cut(s, "/")
	n, err := strconv.Atoi(p)
	return n, proto, ok && err == nil
}

func cmdFirewall(args []string, env Env) result {
	if len(args) == 0 {
		return fail("firewall", CodeUsage, firewallUsage)
	}
	sub := "firewall." + args[0]
	fs := flag.NewFlagSet("firewall", flag.ContinueOnError)
	from := fs.String("from", "", "")
	action := fs.String("action", "allow", "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args[1:])
	if err != nil {
		return fail(sub, CodeUsage, firewallUsage)
	}
	if !env.root() {
		return fail(sub, "permission_denied", "управление брандмауэром требует root (sudo lite-deploy firewall …)")
	}
	m := &firewall.Manager{Runner: env.Runner}
	ctx := context.Background()
	needYes := func() *result {
		if *yes {
			return nil
		}
		r := fail(sub, "confirmation_required", "это изменит брандмауэр; повторите с --yes")
		return &r
	}
	var st *firewall.State
	var e *firewall.Error
	switch args[0] {
	case "status":
		if len(pos) != 0 {
			return fail(sub, CodeUsage, firewallUsage)
		}
		st, e = m.Status(ctx)
	case "allow", "remove":
		if len(pos) != 1 {
			return fail(sub, CodeUsage, firewallUsage)
		}
		port, proto, ok := parsePortSpec(pos[0])
		if !ok {
			return fail(sub, CodeUsage, "порт задаётся как 8080/tcp")
		}
		if r := needYes(); r != nil {
			return *r
		}
		if args[0] == "allow" {
			st, e = m.Allow(ctx, port, proto, *from)
		} else {
			st, e = m.Remove(ctx, port, proto, *from, *action)
		}
	case "enable":
		if len(pos) != 0 {
			return fail(sub, CodeUsage, firewallUsage)
		}
		if r := needYes(); r != nil {
			return *r
		}
		st, e = m.Enable(ctx)
	default:
		return fail("firewall", CodeUsage, firewallUsage)
	}
	if e != nil {
		return firewallFail(sub, e)
	}
	return result{command: sub, data: st, text: firewallText(st)}
}

func firewallText(st *firewall.State) string {
	var sb strings.Builder
	state := "выключен"
	if st.Active {
		state = "включён"
	}
	fmt.Fprintf(&sb, "Брандмауэр %s, порт SSH %d\n", state, st.SSHPort)
	for _, r := range st.Rules {
		mark := ""
		if r.Protected {
			mark = "  (защищено)"
		}
		fmt.Fprintf(&sb, "  %-6s %-14s от %s%s\n", r.Action, r.Raw0(), r.From, mark)
	}
	return sb.String()
}
