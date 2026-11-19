package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// safeFixes are the fixes that may be run automatically: repeatable, without substituting owner values and without
// broadening access to the server. Everything else (the firewall, users, keys, package updates) is only suggested.
var safeFixes = map[string]bool{
	"security ssh": true, "security updates": true, "security sysctl": true, "security fail2ban": true,
	"pg schedule on": true, "backup schedule on": true, "backblaze schedule on": true,
	"podman install": true, "pg install": true, "redis install": true, "caddy install": true,
}

// fixCommands splits a "sudo lite … ; sudo lite …" hint into commands (plugin, arguments) from the safeFixes list.
// The parts that are not in the list are returned separately as needing manual action.
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

// cmdFix: lite fix [--dry-run] [--yes] (the same as verify --autofix). It fixes only what is in safeFixes.
func cmdFix(args []string, env Env) result {
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail("fix", CodeUsage, "usage: lite fix [--dry-run] [--yes]")
	}
	if !*dry && !env.root() {
		if !*yes && !confirm(env, "Run the safe fixes (preview: lite fix --dry-run)?") {
			return fail("fix", "confirmation_required", "this will change the system; run again with --yes")
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
		sb.WriteString("Nothing to fix automatically.\n")
	} else if *dry {
		sb.WriteString("Will run:\n")
		for _, n := range names {
			sb.WriteString("  lite " + n + "\n")
		}
	}
	if !*dry && len(plan) > 0 && !*yes && !confirm(env, "Run: "+strings.Join(names, "; ")+"?") {
		return fail("fix", "confirmation_required", "this will change the system; run again with --yes")
	}
	var applied, failed []string
	if !*dry {
		for _, a := range plan {
			r := cmdPluginRun(a[0], append(append([]string{}, a[1:]...), "--yes"), env)
			key := strings.Join(a, " ")
			if r.err != nil {
				failed = append(failed, key+": "+r.err.Message)
				fmt.Fprintf(&sb, "FAILED lite %s: %s\n", key, r.err.Message)
				continue
			}
			applied = append(applied, key)
			fmt.Fprintf(&sb, "Done: lite %s\n", key)
		}
	}
	if len(manual) > 0 {
		sb.WriteString("Manual:\n")
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
		res.err = &ErrorBody{Code: "step_failed", Message: "not all fixes succeeded: " + strings.Join(failed, "; ")}
	}
	return res
}
