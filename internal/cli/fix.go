package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// safeFixes исправления, которые разрешено выполнять само: повторяемые, без подстановки значений владельца и без
// расширения доступа к серверу. Всё остальное (файервол, пользователи, ключи, обновление пакетов) только предлагается.
var safeFixes = map[string]bool{
	"security ssh": true, "security updates": true, "security sysctl": true, "security fail2ban": true,
	"pg schedule on": true, "backup schedule on": true, "backblaze schedule on": true,
	"podman install": true, "pg install": true, "redis install": true, "caddy install": true,
}

// fixCommands разбирает подсказку «sudo lite … ; sudo lite …» на команды (плагин, аргументы) из списка safeFixes.
// Части, которых в списке нет, возвращаются отдельно как требующие ручного выполнения.
func fixCommands(hint string) (auto [][]string, manual []string) {
	for _, part := range strings.Split(hint, ";") {
		part = strings.TrimSpace(part)
		rest, ok := strings.CutPrefix(part, "sudo lite ")
		if !ok {
			if part != "" {
				manual = append(manual, part)
			}
			continue
		}
		if safeFixes[rest] {
			auto = append(auto, strings.Fields(rest))
		} else {
			manual = append(manual, part)
		}
	}
	return auto, manual
}

// cmdFix: lite fix [--dry-run] [--yes] (то же, что verify --autofix). Исправляет только то, что в safeFixes.
func cmdFix(args []string, env Env) result {
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail("fix", CodeUsage, "использование: lite fix [--dry-run] [--yes]")
	}
	if !*dry && !env.root() {
		if !*yes && !confirm(env, "Выполнить безопасные исправления (посмотреть: lite fix --dry-run)?") {
			return fail("fix", "confirmation_required", "это изменит систему; повторите с --yes")
		}
		return forwardFix(env)
	}
	var plan [][]string
	seen := map[string]bool{}
	var manual []string
	for _, f := range collectFindings(env) {
		if f.Level == plugin.LevelOK || f.Fix == "" {
			continue
		}
		auto, man := fixCommands(f.Fix)
		for _, a := range auto {
			if k := strings.Join(a, " "); !seen[k] {
				seen[k] = true
				plan = append(plan, a)
			}
		}
		for _, m := range man {
			manual = append(manual, f.Plugin+"/"+f.Check+": "+m)
		}
	}
	var sb strings.Builder
	names := []string{}
	for _, a := range plan {
		names = append(names, strings.Join(a, " "))
	}
	if len(plan) == 0 {
		sb.WriteString("Нечего исправлять автоматически.\n")
	} else if *dry {
		sb.WriteString("Будет выполнено:\n")
		for _, n := range names {
			sb.WriteString("  lite " + n + "\n")
		}
	}
	if !*dry && len(plan) > 0 && !*yes && !confirm(env, "Выполнить: "+strings.Join(names, "; ")+"?") {
		return fail("fix", "confirmation_required", "это изменит систему; повторите с --yes")
	}
	var applied, failed []string
	if !*dry {
		for _, a := range plan {
			r := cmdPluginRun(a[0], append(append([]string{}, a[1:]...), "--yes"), env)
			key := strings.Join(a, " ")
			if r.err != nil {
				failed = append(failed, key+": "+r.err.Message)
				fmt.Fprintf(&sb, "НЕ УДАЛОСЬ lite %s: %s\n", key, r.err.Message)
				continue
			}
			applied = append(applied, key)
			fmt.Fprintf(&sb, "Выполнено: lite %s\n", key)
		}
	}
	if len(manual) > 0 {
		sb.WriteString("Вручную:\n")
		for _, m := range manual {
			sb.WriteString("  " + m + "\n")
		}
	}
	if applied == nil {
		applied = []string{}
	}
	if failed == nil {
		failed = []string{}
	}
	if manual == nil {
		manual = []string{}
	}
	data := map[string]any{"planned": names, "applied": applied, "failed": failed, "manual": manual, "dry_run": *dry}
	res := result{command: "fix", data: data, text: sb.String()}
	if len(failed) > 0 {
		res.err = &ErrorBody{Code: "step_failed", Message: "не все исправления удались: " + strings.Join(failed, "; ")}
	}
	return res
}
