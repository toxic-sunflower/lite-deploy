package builtin

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/cfaccess"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

const (
	cfUser    = "cloudflared"
	cfDir     = "/etc/cloudflared"
	cfToken   = cfDir + "/token"
	cfService = "lite-cloudflared"
	cfUnit    = "/etc/systemd/system/" + cfService + ".service"
)

// A server can hold several tunnels, one per Cloudflare account (or per group of sites). The first one is called
// "default" and keeps the file names of the single-tunnel versions; the others are tunnel-NAME.
const cfDefault = "default"

var cfNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,20}$`)

func cfTokenPath(name string) string {
	if name == cfDefault {
		return cfToken
	}
	return cfDir + "/tunnel-" + name + ".token"
}

func cfServiceName(name string) string {
	if name == cfDefault {
		return cfService
	}
	return cfService + "-" + name
}

func cfUnitPath(name string) string { return "/etc/systemd/system/" + cfServiceName(name) + ".service" }

// cfName takes the optional "--name NAME" from the arguments and returns the name and the rest.
func cfName(args []string) (string, []string, error) {
	name, rest := cfDefault, []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--name" {
			if i+1 >= len(args) {
				return "", nil, plugin.Errorf("usage", "--name needs a name, for example --name acme")
			}
			name = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if !cfNameRe.MatchString(name) {
		return "", nil, plugin.Errorf("usage", "a tunnel name is lowercase letters, digits and dashes, starting with a letter (up to 21 characters)")
	}
	return name, rest, nil
}

var cfTokenRe = regexp.MustCompile(`^[A-Za-z0-9_=.+/-]{40,1000}$`)

// Cloudflare is the `cloudflare` plugin: a Cloudflare Tunnel that brings visitors to the sites without any open port.
// cloudflared runs as its own unprivileged user, and the tunnel token is kept in a file that only that user can read
// (it is never put on a command line, where everyone could see it).
type Cloudflare struct{}

func (Cloudflare) Name() string       { return "cloudflare" }
func (Cloudflare) Kind() string       { return "builtin" }
func (Cloudflare) Requires() []string { return nil }
func (Cloudflare) Description() string {
	return "Cloudflare Tunnel: visitors reach your sites without opening any port on the server"
}
func (Cloudflare) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Confirm: true, Help: "install cloudflared and a user for it"},
		{Name: "setup", Confirm: true, Args: "--token-stdin [--name NAME]", Help: "connect a tunnel: the token from the Cloudflare dashboard is read from standard input; --name makes another one (a second Cloudflare account)"},
		{Name: "connect", Confirm: true, Args: "--domain DOMAIN --port PORT --token-stdin [--name NAME] [--account ID] [--project NAME --site NAME]", Help: "make everything for a domain: the tunnel, the route and the DNS record at Cloudflare (with an API token read from standard input, used once and never kept), then connect the tunnel on this server"},
		{Name: "access-list", Confirm: true, Args: "--token-stdin [--account ID]", Help: "what is closed with Cloudflare Access in the account (API token from standard input, never kept)"},
		{Name: "access-protect", Confirm: true, Args: "--token-stdin --domain DOMAIN [--path /admin*] --who NAME@EXAMPLE.COM[,@EXAMPLE.COM] [--account ID]", Help: "close a domain or a path with a Cloudflare login page"},
		{Name: "access-remove", Confirm: true, Args: "--token-stdin --app ID [--account ID]", Help: "open a closed address again"},
		{Name: "under-attack", Confirm: true, Args: "--token-stdin --domain DOMAIN --mode on|off|status", Help: "Cloudflare \"I'm Under Attack!\" mode: every visitor passes a short check page first (an attack on the site: turn it on, afterwards off). It applies to the whole domain, not to one address. API token from standard input, never kept"},
		{Name: "status", Args: "[--name NAME]", Help: "is cloudflared installed, and which tunnels run"},
		{Name: "remove", Confirm: true, Args: "[--name NAME]", Help: "stop a tunnel and delete its token"},
	}
}

// Installed reports whether cloudflared is on this server.
func (Cloudflare) Installed(ctx context.Context, env plugin.Env) bool {
	return tools{ctx, env}.ok("cloudflared", "--version")
}

func (t tools) cloudflaredApt() error {
	if err := t.install("curl", "gnupg"); err != nil {
		return err
	}
	get := func(url string) ([]byte, error) {
		res, err := t.env.Runner.Run(t.ctx, host.Cmd{Name: "curl", Args: []string{"-fsSL", "--proto", "=https", "--tlsv1.2", url}, Timeout: 2 * time.Minute})
		if err != nil {
			return nil, plugin.Errorf("step_failed", "curl: %v", err)
		}
		if res.ExitCode != 0 {
			return nil, plugin.Errorf("step_failed", "could not download %s: %s", url, strings.TrimSpace(string(res.Stderr)))
		}
		return res.Stdout, nil
	}
	key, err := get("https://pkg.cloudflare.com/cloudflare-main.gpg")
	if err != nil {
		return err
	}
	const keyring = "/usr/share/keyrings/cloudflare-main.gpg"
	if err := t.env.FS.WriteFile(keyring, key, fs.FileMode(0o644)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	list := "deb [signed-by=" + keyring + "] https://pkg.cloudflare.com/cloudflared any main\n"
	if err := t.env.FS.WriteFile("/etc/apt/sources.list.d/cloudflared.list", []byte(list), fs.FileMode(0o644)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	return t.install("cloudflared")
}

// InstallCloudflare installs cloudflared and creates the user it runs as. It does not connect any tunnel.
func InstallCloudflare(ctx context.Context, env plugin.Env, say Say) error {
	t := tools{ctx, env}
	if t.manager() != "apt" {
		return plugin.Errorf("precondition_failed", "cloudflared is installed automatically on Debian and Ubuntu only: install it by hand, then run lite cloudflare setup")
	}
	say.say("Plugin cloudflare: installing cloudflared...")
	if !t.ok("cloudflared", "--version") {
		if err := t.cloudflaredApt(); err != nil {
			return err
		}
	}
	if !t.ok("id", cfUser) {
		if err := t.must("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", cfUser); err != nil {
			return err
		}
	}
	return t.env.FS.MkdirAll(cfDir, fs.FileMode(0o755))
}

func cfUnitText(name string) string {
	return "[Unit]\nDescription=Cloudflare Tunnel " + name + " (lite-deploy)\nAfter=network-online.target\nWants=network-online.target\n\n" +
		"[Service]\nUser=" + cfUser + "\nExecStart=/usr/bin/cloudflared --no-autoupdate tunnel run --token-file " + cfTokenPath(name) + "\n" +
		"Restart=on-failure\nRestartSec=5\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\nProtectHome=true\n\n[Install]\nWantedBy=multi-user.target\n"
}

// setupTunnel keeps the token of a tunnel in a file only cloudflared can read and runs the tunnel as a service.
func setupTunnel(t tools, env plugin.Env, name, tok string) error {
	if err := env.FS.MkdirAll(cfDir, fs.FileMode(0o755)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	path, svc := cfTokenPath(name), cfServiceName(name)
	if err := env.FS.WriteFile(path, []byte(tok+"\n"), fs.FileMode(0o600)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := t.must("chown", cfUser+":"+cfUser, path); err != nil {
		return err
	}
	if err := env.FS.WriteFile(cfUnitPath(name), []byte(cfUnitText(name)), fs.FileMode(0o644)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := t.must("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := t.must("systemctl", "enable", "--now", svc); err != nil {
		return err
	}
	return t.must("systemctl", "restart", svc) // a new token for an existing tunnel takes effect
}

// cfAPIBase is the Cloudflare API; changed in tests only.
var cfAPIBase = cfaccess.DefaultBase

// connect makes everything for a domain: the tunnel, the route and the DNS record at Cloudflare (with an API token that is
// read from standard input, used once and never kept), then the connector on this server.
func (Cloudflare) connect(ctx context.Context, env plugin.Env, args []string) (plugin.Output, error) {
	usage := plugin.Errorf("usage", "usage: echo APITOKEN | lite cloudflare connect --domain DOMAIN --port PORT --token-stdin [--name NAME] [--account ID]")
	var domain, account string
	name, port, stdin := cfDefault, 0, false
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 >= len(args) {
				return ""
			}
			i++
			return args[i]
		}
		switch args[i] {
		case "--domain":
			domain = next()
		case "--port":
			port, _ = strconv.Atoi(next())
		case "--name":
			name = next()
		case "--account":
			account = next()
		case "--token-stdin":
			stdin = true
		case "--project", "--site":
			next() // used by lite after this command to point the site at the tunnel
		default:
			return plugin.Output{}, usage
		}
	}
	if domain == "" || port < 18000 || port > 18999 || !stdin || !cfNameRe.MatchString(name) {
		return plugin.Output{}, usage
	}
	if _, err := cfaccess.Address(domain, ""); err != nil {
		return plugin.Output{}, plugin.Errorf("usage", "%v", err)
	}
	if env.Stdin == nil {
		return plugin.Output{}, usage
	}
	b, err := io.ReadAll(io.LimitReader(env.Stdin, 4096))
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "read the token: %v", err)
	}
	apiToken := strings.TrimSpace(string(b))
	if len(apiToken) < 20 {
		return plugin.Output{}, plugin.Errorf("usage", "the Cloudflare API token is read from standard input")
	}
	c, account, err := cfClient(ctx, apiToken, account)
	if err != nil {
		return plugin.Output{}, err
	}
	prov, perr := c.Provision(ctx, account, name, domain, port)
	if perr != nil {
		e := plugin.Errorf("step_failed", "%v", perr)
		if len(prov.Steps) > 0 {
			e.Hint = "done before this: " + strings.Join(prov.Steps, ", ")
		}
		return plugin.Output{}, e
	}
	if err := InstallCloudflare(ctx, env, nil); err != nil {
		return plugin.Output{}, err
	}
	if err := setupTunnel(tools{ctx, env}, env, name, prov.Token); err != nil {
		return plugin.Output{}, err
	}
	steps := append(prov.Steps, "tunnel "+name+" connected on this server")
	text := strings.Join(steps, "\n") + "\nNext: point the site at it: lite publish PROJECT SITE tunnel " + name + "\n"
	return plugin.Output{Text: text, Data: map[string]any{"steps": steps, "name": name, "domain": domain}}, nil
}

func readCfToken(in io.Reader) (string, error) {
	if in == nil {
		return "", plugin.Errorf("usage", "the token is read from standard input: echo TOKEN | lite cloudflare setup --token-stdin")
	}
	b, err := io.ReadAll(io.LimitReader(in, 4096))
	if err != nil {
		return "", plugin.Errorf("step_failed", "read the token: %v", err)
	}
	tok := strings.TrimSpace(string(b))
	if !cfTokenRe.MatchString(tok) {
		return "", plugin.Errorf("usage", "this does not look like a tunnel token: copy the whole token from the Cloudflare dashboard (Zero Trust, Networks, Tunnels)")
	}
	return tok, nil
}

func (Cloudflare) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	t := tools{ctx, env}
	switch cmd {
	case "install":
		if err := noArgs(args, cmd, "cloudflare"); err != nil {
			return plugin.Output{}, err
		}
		if err := InstallCloudflare(ctx, env, nil); err != nil {
			return plugin.Output{}, err
		}
		return textOut("cloudflared is installed.\nNext: create a tunnel in the Cloudflare dashboard, copy its token and run: lite cloudflare setup --token-stdin\n"), nil
	case "setup":
		name, rest, err := cfName(args)
		if err != nil {
			return plugin.Output{}, err
		}
		if len(rest) != 1 || rest[0] != "--token-stdin" {
			return plugin.Output{}, plugin.Errorf("usage", "usage: echo TOKEN | lite cloudflare setup --token-stdin [--name NAME]")
		}
		if !t.ok("cloudflared", "--version") || !t.ok("id", cfUser) {
			e := plugin.Errorf("precondition_failed", "cloudflared is not installed")
			e.Hint = "lite cloudflare install"
			return plugin.Output{}, e
		}
		tok, err := readCfToken(env.Stdin)
		if err != nil {
			return plugin.Output{}, err
		}
		if err := setupTunnel(t, env, name, tok); err != nil {
			return plugin.Output{}, err
		}
		return textOut("The tunnel " + name + " is connected and running.\nNext: in the Cloudflare dashboard of that account route your domain to http://127.0.0.1:PORT (the port of the site), and keep the site set to “through a Cloudflare Tunnel” in Ingress.\n"), nil
	case "connect":
		return Cloudflare{}.connect(ctx, env, args)
	case "under-attack":
		return Cloudflare{}.underAttack(ctx, env, args)
	case "access-list", "access-protect", "access-remove":
		return Cloudflare{}.access(ctx, env, cmd, args)
	case "status":
		name, rest, err := cfName(args)
		if err != nil || len(rest) != 0 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite cloudflare status [--name NAME]")
		}
		inst := t.ok("cloudflared", "--version")
		var tunnels []map[string]any
		for _, n := range cfTunnelNames(env) {
			if len(args) > 0 && n != name {
				continue
			}
			tunnels = append(tunnels, map[string]any{"name": n, "running": t.ok("systemctl", "is-active", "--quiet", cfServiceName(n))})
		}
		text := "cloudflared is not installed.\n"
		if inst {
			text = "cloudflared is installed; tunnels: " + fmt.Sprint(len(tunnels)) + ".\n"
			for _, x := range tunnels {
				text += "  " + x["name"].(string) + ": " + map[bool]string{true: "running", false: "not running"}[x["running"].(bool)] + "\n"
			}
		}
		if tunnels == nil {
			tunnels = []map[string]any{}
		}
		return plugin.Output{Text: text, Data: map[string]any{"installed": inst, "tunnels": tunnels}}, nil
	case "remove":
		name, rest, err := cfName(args)
		if err != nil || len(rest) != 0 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite cloudflare remove [--name NAME]")
		}
		_, _ = t.run("systemctl", "disable", "--now", cfServiceName(name))
		_ = env.FS.Remove(cfUnitPath(name))
		_ = env.FS.Remove(cfTokenPath(name))
		_, _ = t.run("systemctl", "daemon-reload")
		return textOut("The tunnel " + name + " is stopped and its token is deleted. Sites on Cloudflare that used it no longer reach this server.\n"), nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin cloudflare has no command %q", cmd)
}

// cfTunnelNames lists the connected tunnels (those with a token file), the default one first.
func cfTunnelNames(env plugin.Env) []string {
	var names []string
	if _, err := env.FS.Stat(cfToken); err == nil {
		names = append(names, cfDefault)
	}
	entries, _ := env.FS.ReadDir(cfDir)
	for _, e := range entries {
		if strings.HasPrefix(e, "tunnel-") && strings.HasSuffix(e, ".token") {
			if n := strings.TrimSuffix(strings.TrimPrefix(e, "tunnel-"), ".token"); cfNameRe.MatchString(n) {
				names = append(names, n)
			}
		}
	}
	return names
}

func (Cloudflare) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if !t.ok("cloudflared", "--version") {
		return []plugin.Finding{finding("cloudflare", "installed", plugin.LevelOK, "Cloudflare Tunnel is not installed (not in use)", "")}
	}
	names := cfTunnelNames(env)
	if len(names) == 0 {
		return []plugin.Finding{finding("cloudflare", "token", plugin.LevelWarn, "cloudflared is installed but no tunnel is connected", "echo TOKEN | lite cloudflare setup --token-stdin")}
	}
	var out []plugin.Finding
	for _, n := range names {
		label := "the Cloudflare Tunnel"
		check := "service"
		if len(names) > 1 || n != cfDefault {
			label, check = "the Cloudflare Tunnel "+n, "service-"+n
		}
		if !t.ok("systemctl", "is-active", "--quiet", cfServiceName(n)) {
			out = append(out, finding("cloudflare", check, plugin.LevelFail, label+" is not running: sites published through it are unreachable", "systemctl restart "+cfServiceName(n)))
		} else {
			out = append(out, finding("cloudflare", check, plugin.LevelOK, label+" is running", ""))
		}
	}
	return out
}

// cfClient makes the API client for a token and finds the account (the only one the token sees, or the one given).
func cfClient(ctx context.Context, apiToken, account string) (*cfaccess.Client, string, error) {
	c := cfaccess.New(apiToken)
	c.Base = cfAPIBase
	if account != "" {
		return c, account, nil
	}
	accs, err := c.Accounts(ctx)
	if err != nil {
		return nil, "", plugin.Errorf("step_failed", "%v", err)
	}
	if len(accs) != 1 {
		var l []string
		for _, a := range accs {
			l = append(l, a.ID+" ("+a.Name+")")
		}
		e := plugin.Errorf("usage", "this token sees %d Cloudflare accounts: choose one with --account ID", len(accs))
		e.Hint = strings.Join(l, ", ")
		return nil, "", e
	}
	return c, accs[0].ID, nil
}

// access handles access-list, access-protect and access-remove: a Cloudflare login page in front of a domain (or a path).
// The API token is read from standard input, used once and never kept.
func (Cloudflare) access(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	usage := plugin.Errorf("usage", "usage: echo APITOKEN | lite cloudflare %s --token-stdin [--account ID] %s", cmd,
		map[string]string{"access-list": "", "access-protect": "--domain DOMAIN [--path /admin*] --who NAME@EXAMPLE.COM[,@EXAMPLE.COM]", "access-remove": "--app ID"}[cmd])
	var account, domain, path, who, app string
	stdin := false
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 >= len(args) {
				return ""
			}
			i++
			return args[i]
		}
		switch args[i] {
		case "--token-stdin":
			stdin = true
		case "--account":
			account = next()
		case "--domain":
			domain = next()
		case "--path":
			path = next()
		case "--who":
			who = next()
		case "--app":
			app = next()
		default:
			return plugin.Output{}, usage
		}
	}
	if !stdin || env.Stdin == nil {
		return plugin.Output{}, usage
	}
	b, _ := io.ReadAll(io.LimitReader(env.Stdin, 4096))
	apiToken := strings.TrimSpace(string(b))
	if len(apiToken) < 20 {
		return plugin.Output{}, plugin.Errorf("usage", "the Cloudflare API token is read from standard input")
	}
	c, acc, err := cfClient(ctx, apiToken, account)
	if err != nil {
		return plugin.Output{}, err
	}
	switch cmd {
	case "access-list":
		apps, err := c.Apps(ctx, acc)
		if err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
		}
		var lines []string
		for _, a := range apps {
			lines = append(lines, a.ID+"  "+a.Domain)
		}
		if apps == nil {
			apps = []cfaccess.App{}
		}
		return plugin.Output{Text: strings.Join(lines, "\n") + "\n", Data: map[string]any{"apps": apps}}, nil
	case "access-protect":
		addr, err := cfaccess.Address(domain, path)
		if err != nil {
			return plugin.Output{}, plugin.Errorf("usage", "%v", err)
		}
		app, err := c.Protect(ctx, acc, addr, strings.Split(who, ","))
		if err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
		}
		return plugin.Output{Text: addr + " is now closed with Cloudflare Access.\n", Data: map[string]any{"app": app}}, nil
	default: // access-remove
		if app == "" {
			return plugin.Output{}, usage
		}
		if err := c.Remove(ctx, acc, app); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
		}
		return plugin.Output{Text: "The application is removed: the address is open again.\n"}, nil
	}
}

// underAttack switches the Cloudflare security level of the domain to "I'm Under Attack!" and back. The token is read from
// standard input, used once and never kept; it needs Zone: Zone Settings: Edit and Zone: Zone: Read.
func (Cloudflare) underAttack(ctx context.Context, env plugin.Env, args []string) (plugin.Output, error) {
	usage := plugin.Errorf("usage", "usage: echo APITOKEN | lite cloudflare under-attack --token-stdin --domain DOMAIN --mode on|off|status")
	var domain, mode string
	stdin := false
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 >= len(args) {
				return ""
			}
			i++
			return args[i]
		}
		switch args[i] {
		case "--token-stdin":
			stdin = true
		case "--domain":
			domain = next()
		case "--mode":
			mode = next()
		case "--account": // sent by the page for every Cloudflare command; the zone is found by the domain
			next()
		default:
			return plugin.Output{}, usage
		}
	}
	if !stdin || env.Stdin == nil || domain == "" || (mode != "on" && mode != "off" && mode != "status") {
		return plugin.Output{}, usage
	}
	b, _ := io.ReadAll(io.LimitReader(env.Stdin, 4096))
	apiToken := strings.TrimSpace(string(b))
	if len(apiToken) < 20 {
		return plugin.Output{}, plugin.Errorf("usage", "the Cloudflare API token is read from standard input")
	}
	c := cfaccess.New(apiToken)
	c.Base = cfAPIBase
	z, err := c.ZoneFor(ctx, domain)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
	}
	before, err := c.SecurityLevel(ctx, z.ID)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
	}
	data := map[string]any{"zone": z.Name, "before": before, "level": before, "under_attack": before == cfaccess.UnderAttack}
	switch mode {
	case "status":
		text := "The domain " + z.Name + " is in the normal mode (security level " + before + ").\n"
		if before == cfaccess.UnderAttack {
			text = "The domain " + z.Name + " is in the \"I'm Under Attack!\" mode.\n"
		}
		return plugin.Output{Text: text, Data: data}, nil
	case "on":
		if before != cfaccess.UnderAttack {
			if err := c.SetSecurityLevel(ctx, z.ID, cfaccess.UnderAttack); err != nil {
				return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
			}
		}
		data["level"], data["under_attack"] = cfaccess.UnderAttack, true
		return plugin.Output{Text: "\"I'm Under Attack!\" is on for the whole domain " + z.Name + ": every visitor passes a short check first. Turn it off when the attack is over.\n", Data: data}, nil
	default: // off
		if before == cfaccess.UnderAttack {
			if err := c.SetSecurityLevel(ctx, z.ID, cfaccess.NormalLevel); err != nil {
				return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
			}
		}
		data["level"], data["under_attack"] = cfaccess.NormalLevel, false
		if before != cfaccess.UnderAttack {
			data["level"] = before
		}
		return plugin.Output{Text: "\"I'm Under Attack!\" is off for " + z.Name + " (the security level is back to the normal one).\n", Data: data}, nil
	}
}
