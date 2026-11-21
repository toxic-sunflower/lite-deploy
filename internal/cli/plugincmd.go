package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/pkgmgr"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
)

const pluginUsage = "usage: lite plugin list | lite plugin run PLUGIN COMMAND [arguments] | lite PLUGIN COMMAND [arguments]"

// registry gathers the built-in and external plugins. Load errors of external plugins are returned separately.
func registry(env Env) (*plugin.Registry, []error) {
	r := plugin.NewRegistry()
	for _, p := range []plugin.Plugin{builtin.Pkg{}, builtin.Security{}, builtin.Firewall{}, builtin.Caddy{}, builtin.Podman{}, builtin.PG{}, builtin.Redis{}, builtin.Cloudflare{}, builtin.Backblaze, builtin.S3, builtin.SFTP, builtin.Backup{}} {
		if err := r.Add(p); err != nil {
			panic(err) // an error in the program itself: the built-in names are fixed
		}
	}
	dir := env.PluginDir
	if dir == "" {
		dir = plugin.Dir
	}
	installed := func(ctx context.Context, name string) bool {
		m := &pkgmgr.Manager{Runner: env.Runner, FS: env.FS}
		in, err := m.Check(ctx, name)
		return err == nil && in.Installed
	}
	return r, plugin.LoadDir(env.FS, dir, r, installed)
}

func (e Env) pluginEnv() plugin.Env {
	user := ""
	if e.Getenv != nil {
		user = e.Getenv("SUDO_USER")
	}
	return plugin.Env{Runner: e.Runner, FS: e.FS, Now: e.Now, User: user, Projects: projectNames(e), Stdin: e.Stdin}
}

func pluginFail(command string, err error) result {
	var pe *plugin.Error
	if errors.As(err, &pe) {
		return result{command: command, err: &ErrorBody{Code: pe.Code, Message: pe.Message, Hint: pe.Hint}}
	}
	return fail(command, CodeInternal, err.Error())
}

// confirm asks for confirmation in the terminal. Without a terminal and without --yes the command refuses.
func confirm(env Env, question string) bool {
	if env.Interactive == nil || !env.Interactive() {
		return false
	}
	_, _ = fmt.Fprintf(env.Stderr, "%s [y/N]: ", question)
	line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// cmdPluginRun runs a plugin command: lite PLUGIN COMMAND [arguments] [--yes].
func cmdPluginRun(name string, args []string, env Env) result {
	command := name
	r, _ := registry(env)
	p, ok := r.Get(name)
	if !ok {
		return fail(command, CodeUsage, fmt.Sprintf("no plugin %q (list: lite plugin list)", name))
	}
	yes := false
	var rest []string
	for _, a := range args {
		if a == "--yes" {
			yes = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		return fail(command, CodeUsage, "usage: lite "+name+" COMMAND [arguments]; commands: "+commandNames(p))
	}
	cmd, cargs := rest[0], rest[1:]
	command = name + "." + cmd
	c, ok := plugin.Find(p, cmd)
	if !ok {
		return fail(command, CodeUsage, fmt.Sprintf("plugin %s has no command %q; available: %s", name, cmd, commandNames(p)))
	}
	if !plugin.ValidArgs(cargs) {
		return fail(command, CodeUsage, "invalid arguments: no control characters, at most 256 characters each, at most 16")
	}
	if c.Confirm {
		if !yes && !confirm(env, fmt.Sprintf("Run: lite %s %s %s?", name, cmd, strings.Join(cargs, " "))) {
			return fail(command, "confirmation_required", "this will change the system; run again with --yes")
		}
	}
	if (c.Confirm || c.Root) && !env.root() {
		// without sudo: the agent and the executor run the command as root; the confirmation was already obtained above
		return forwardPlugin(env, command, name, cmd, cargs)
	}
	out, err := p.Run(context.Background(), env.pluginEnv(), cmd, cargs)
	if err != nil {
		return pluginFail(command, err)
	}
	res := result{command: command, data: out.Data, text: out.Text}
	if name == "cloudflare" && cmd == "connect" {
		return pointSiteAtTunnel(cargs, res, env)
	}
	return res
}

// pointSiteAtTunnel finishes `lite cloudflare connect ... --project P --site S`: the site goes through the tunnel just made.
func pointSiteAtTunnel(args []string, res result, env Env) result {
	flag := func(n string) string {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == n {
				return args[i+1]
			}
		}
		return ""
	}
	proj, site, tun := flag("--project"), flag("--site"), flag("--name")
	if proj == "" || site == "" {
		return res
	}
	pub := []string{proj, site, "tunnel"}
	if tun != "" && tun != "default" {
		pub = append(pub, tun)
	}
	pr := cmdPublish(append(pub, "--yes"), env)
	if pr.err != nil {
		pr.text = res.text + "The tunnel is ready, but pointing the site at it failed.\n"
		return pr
	}
	res.text += "Site " + proj + "/" + site + " now goes through the tunnel.\n"
	return res
}

func commandNames(p plugin.Plugin) string {
	var n []string
	for _, c := range p.Commands() {
		n = append(n, c.Name)
	}
	return strings.Join(n, ", ")
}

// cmdPlugin: lite plugin list | run.
func cmdPlugin(args []string, env Env) result {
	if len(args) == 0 {
		return fail("plugin", CodeUsage, pluginUsage)
	}
	switch args[0] {
	case "list":
		return cmdInfo(env, "plugin.list")
	case "run":
		if len(args) < 2 {
			return fail("plugin.run", CodeUsage, pluginUsage)
		}
		return cmdPluginRun(args[1], args[2:], env)
	}
	return fail("plugin", CodeUsage, pluginUsage)
}

type pluginInfo struct {
	Name        string           `json:"name"`
	Kind        string           `json:"kind"`
	Description string           `json:"description"`
	Requires    []string         `json:"requires"`
	Commands    []plugin.Command `json:"commands"`
	// Installed is set for plugins that can tell: false means the plugin is not in use on this server.
	Installed *bool `json:"installed,omitempty"`
}

// cmdInfo: lite info (plugins and their commands).
func cmdInfo(env Env, command string) result {
	r, errs := registry(env)
	var list []pluginInfo
	var sb strings.Builder
	fmt.Fprintf(&sb, "lite %s\nPlugins:\n", Version)
	for _, p := range r.List() {
		req := p.Requires()
		if req == nil {
			req = []string{}
		}
		info := pluginInfo{Name: p.Name(), Kind: p.Kind(), Description: p.Description(), Requires: req, Commands: p.Commands()}
		if pr, ok := p.(plugin.Presence); ok {
			in := pr.Installed(context.Background(), env.pluginEnv())
			info.Installed = &in
		}
		list = append(list, info)
		fmt.Fprintf(&sb, "  %-12s %-9s %s\n", p.Name(), p.Kind(), p.Description())
		for _, c := range p.Commands() {
			fmt.Fprintf(&sb, "      %s — %s\n", strings.TrimSpace("lite "+p.Name()+" "+c.Name+" "+c.Args), c.Help)
		}
	}
	var warns []Warning
	var msgs []string
	for _, e := range errs {
		warns = append(warns, Warning{Code: "plugin_load_failed", Message: e.Error()})
		msgs = append(msgs, e.Error())
	}
	if msgs == nil {
		msgs = []string{}
	}
	return result{command: command, data: map[string]any{"version": Version, "plugins": list, "load_errors": msgs}, text: sb.String(), warn: warns}
}

// cmdVerify: lite verify (read-only): the checks of all plugins.
func cmdVerify(env Env) result {
	// without root some checks (sshd -T, fail2ban-client) give false answers: the executor runs them through the agent
	if !env.root() {
		if r, ok := forwardVerify(env); ok {
			return r
		}
	}
	return verifyResult(collectFindings(env))
}

// verifyResult formats what was found in a readable and a machine form.
func verifyResult(all []plugin.Finding) result {
	var sb strings.Builder
	counts := map[string]int{}
	for _, f := range all {
		counts[f.Level]++
		mark := map[string]string{plugin.LevelOK: "  ok  ", plugin.LevelWarn: "warning", plugin.LevelFail: "ERROR"}[f.Level]
		fmt.Fprintf(&sb, "[%s] %s/%s: %s\n", mark, f.Plugin, f.Check, f.Message)
		if f.Fix != "" && f.Level != plugin.LevelOK {
			fmt.Fprintf(&sb, "           fix: %s\n", f.Fix)
		}
	}
	if all == nil {
		all = []plugin.Finding{}
	}
	data := map[string]any{"findings": all, "ok": counts[plugin.LevelOK], "warnings": counts[plugin.LevelWarn], "failures": counts[plugin.LevelFail]}
	fmt.Fprintf(&sb, "Total: ok %d, warnings %d, errors %d\n", counts[plugin.LevelOK], counts[plugin.LevelWarn], counts[plugin.LevelFail])
	switch {
	case counts[plugin.LevelFail] > 0:
		sb.WriteString("Next: fix the errors above; lite fix --dry-run shows what can be fixed automatically.\n")
	case counts[plugin.LevelWarn] > 0:
		sb.WriteString("Next: the warnings are not urgent; each one shows the command that fixes it.\n")
	default:
		sb.WriteString("Next: nothing to fix. Create a project in the interface (lite-ui) or with lite project create.\n")
	}
	res := result{command: "verify", data: data, text: sb.String()}
	if counts[plugin.LevelFail] > 0 {
		res.err = &ErrorBody{Code: "verify_failed", Message: fmt.Sprintf("the check found errors: %d", counts[plugin.LevelFail])}
	}
	return res
}

// collectFindings gathers the checks of all plugins and of project dependencies (read-only).
func collectFindings(env Env) []plugin.Finding {
	r, errs := registry(env)
	ctx := context.Background()
	var all []plugin.Finding
	for _, p := range r.List() {
		all = append(all, p.Check(ctx, env.pluginEnv())...)
	}
	all = append(all, projectRequirements(ctx, env)...)
	all = append(all, projectPublishing(ctx, env)...)
	all = append(all, versionFinding(ctx, env)...)
	for _, e := range errs {
		all = append(all, plugin.Finding{Plugin: "plugin", Check: "load", Level: plugin.LevelFail, Message: e.Error()})
	}
	return all
}

// projectNames are the project names (also the project users) from the descriptions in /etc/lite-deploy/projects.
func projectNames(env Env) []string {
	var out []string
	if names, err := env.FS.ReadDir("/etc/lite-deploy/projects"); err == nil {
		for _, n := range names {
			if p, ok := strings.CutSuffix(n, ".json"); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// requirementProbe is the command that shows a plugin a project needs is installed.
var requirementProbe = map[string][]string{"podman": {"podman", "--version"}, "pg": {"psql", "--version"}, "caddy": {"caddy", "version"}}

// projectRequirements checks that for every project the server has what it requires (R-20).
func projectRequirements(ctx context.Context, env Env) []plugin.Finding {
	var out []plugin.Finding
	for _, name := range projectNames(env) {
		b, err := env.FS.ReadFile("/etc/lite-deploy/projects/" + name + ".json")
		if err != nil {
			continue
		}
		p, issues := project.Parse(b)
		if len(issues) > 0 {
			out = append(out, plugin.Finding{Plugin: "project", Check: name, Level: plugin.LevelFail, Message: "project description " + name + " cannot be read: " + issues[0].Message})
			continue
		}
		for _, need := range project.Requires(p) {
			probe := requirementProbe[need]
			res, err := env.Runner.Run(ctx, host.Cmd{Name: probe[0], Args: probe[1:]})
			if err != nil || res.ExitCode != 0 {
				out = append(out, plugin.Finding{Plugin: "project", Check: name + "/" + need, Level: plugin.LevelFail,
					Message: "project " + name + " needs the plugin " + need + ", it is not on the server", Fix: "lite " + need + " install"})
				continue
			}
			out = append(out, plugin.Finding{Plugin: "project", Check: name + "/" + need, Level: plugin.LevelOK, Message: "project " + name + " needs " + need + ": present"})
		}
	}
	return out
}

// projectPublishing checks that a site published directly can be reached: the firewall must let ports 80 and 443 in.
func projectPublishing(ctx context.Context, env Env) []plugin.Finding {
	var out []plugin.Finding
	var st *firewall.State
	for _, name := range projectNames(env) {
		b, err := env.FS.ReadFile("/etc/lite-deploy/projects/" + name + ".json")
		if err != nil {
			continue
		}
		p, issues := project.Parse(b)
		if len(issues) > 0 {
			continue
		}
		for _, s := range p.Sites {
			if s.Publish != "direct" {
				if f, ok := tunnelFinding(ctx, env, name, s); ok {
					out = append(out, f)
				}
				continue
			}
			if st == nil {
				var e *firewall.Error
				if st, e = (&firewall.Manager{Runner: env.Runner}).Status(ctx); e != nil {
					return append(out, plugin.Finding{Plugin: "project", Check: name + "/" + s.Name + "/publish", Level: plugin.LevelWarn,
						Message: "could not read the firewall to check that site " + s.Name + " of " + name + " is reachable"})
				}
			}
			check := name + "/" + s.Name + "/publish"
			if !st.Active || (openTo(st, 80) && openTo(st, 443)) {
				out = append(out, plugin.Finding{Plugin: "project", Check: check, Level: plugin.LevelOK, Message: "site " + s.Name + " of " + name + " is published directly: ports 80 and 443 are not blocked"})
				continue
			}
			out = append(out, plugin.Finding{Plugin: "project", Check: check, Level: plugin.LevelWarn,
				Message: "site " + s.Name + " of " + name + " is published directly, but the firewall does not allow ports 80 and 443 (no certificate can be issued)", Fix: "lite firewall mode http"})
		}
	}
	return out
}

// tunnelFinding checks that the named Cloudflare Tunnel of a site is connected and running. Sites on the default tunnel
// are covered by the check of the cloudflare plugin itself.
func tunnelFinding(ctx context.Context, env Env, proj string, s project.Site) (plugin.Finding, bool) {
	if s.Tunnel == "" || s.Tunnel == "default" || s.Publish == "direct" {
		return plugin.Finding{}, false
	}
	check := proj + "/" + s.Name + "/tunnel"
	what := "site " + s.Name + " of " + proj + " is reached through the tunnel " + s.Tunnel
	if _, err := env.FS.Stat("/etc/cloudflared/tunnel-" + s.Tunnel + ".token"); err != nil {
		return plugin.Finding{Plugin: "project", Check: check, Level: plugin.LevelFail, Message: what + ", but that tunnel is not connected on this server",
			Fix: "echo TOKEN | lite cloudflare setup --token-stdin --name " + s.Tunnel}, true
	}
	if res, err := env.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"is-active", "--quiet", "lite-cloudflared-" + s.Tunnel}}); err != nil || res.ExitCode != 0 {
		return plugin.Finding{Plugin: "project", Check: check, Level: plugin.LevelFail, Message: what + ", and that tunnel is not running", Fix: "systemctl restart lite-cloudflared-" + s.Tunnel}, true
	}
	return plugin.Finding{Plugin: "project", Check: check, Level: plugin.LevelOK, Message: what + " and it is running"}, true
}

func openTo(st *firewall.State, port int) bool {
	for _, r := range st.Rules {
		if r.Port == port && r.Action == "allow" && r.From == "any" {
			return true
		}
	}
	return false
}

// versionFinding warns when a newer release of lite exists. If GitHub cannot be reached nothing is reported.
func versionFinding(ctx context.Context, env Env) []plugin.Finding {
	latest, err := latestVersion(ctx, env.HTTP)
	if err != nil || !newer(latest, Version) {
		return nil
	}
	return []plugin.Finding{{Plugin: "lite", Check: "version", Level: plugin.LevelWarn,
		Message: "a new version of lite is available: " + latest + " (you have " + Version + ")", Fix: "lite update"}}
}
