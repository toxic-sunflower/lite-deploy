package builtin

import (
	"context"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"time"

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
		{Name: "setup", Confirm: true, Args: "--token-stdin", Help: "connect the tunnel: the token from the Cloudflare dashboard is read from standard input"},
		{Name: "status", Help: "is cloudflared installed and running"},
		{Name: "remove", Confirm: true, Help: "stop the tunnel and delete its token"},
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

func cfUnitText() string {
	return "[Unit]\nDescription=Cloudflare Tunnel (lite-deploy)\nAfter=network-online.target\nWants=network-online.target\n\n" +
		"[Service]\nUser=" + cfUser + "\nExecStart=/usr/bin/cloudflared --no-autoupdate tunnel run --token-file " + cfToken + "\n" +
		"Restart=on-failure\nRestartSec=5\nNoNewPrivileges=true\nPrivateTmp=true\nProtectSystem=strict\nProtectHome=true\n\n[Install]\nWantedBy=multi-user.target\n"
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
		if len(args) != 1 || args[0] != "--token-stdin" {
			return plugin.Output{}, plugin.Errorf("usage", "usage: echo TOKEN | lite cloudflare setup --token-stdin")
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
		if err := env.FS.MkdirAll(cfDir, fs.FileMode(0o755)); err != nil {
			return plugin.Output{}, plugin.Errorf("io_error", "%v", err)
		}
		if err := env.FS.WriteFile(cfToken, []byte(tok+"\n"), fs.FileMode(0o600)); err != nil {
			return plugin.Output{}, plugin.Errorf("io_error", "%v", err)
		}
		if err := t.must("chown", cfUser+":"+cfUser, cfToken); err != nil {
			return plugin.Output{}, err
		}
		if err := env.FS.WriteFile(cfUnit, []byte(cfUnitText()), fs.FileMode(0o644)); err != nil {
			return plugin.Output{}, plugin.Errorf("io_error", "%v", err)
		}
		if err := t.must("systemctl", "daemon-reload"); err != nil {
			return plugin.Output{}, err
		}
		if err := t.must("systemctl", "enable", "--now", cfService); err != nil {
			return plugin.Output{}, err
		}
		return textOut("The tunnel is connected and running.\nNext: in the Cloudflare dashboard route your domain to http://127.0.0.1:PORT (the port of the site), and keep the site set to “through a Cloudflare Tunnel” in Ingress.\n"), nil
	case "status":
		if err := noArgs(args, cmd, "cloudflare"); err != nil {
			return plugin.Output{}, err
		}
		inst := t.ok("cloudflared", "--version")
		active := t.ok("systemctl", "is-active", "--quiet", cfService)
		_, terr := env.FS.Stat(cfToken)
		text := "cloudflared is not installed.\n"
		if inst {
			text = "cloudflared is installed; the tunnel is " + map[bool]string{true: "running", false: "not running"}[active] + ".\n"
		}
		return plugin.Output{Text: text, Data: map[string]any{"installed": inst, "running": active, "token": terr == nil}}, nil
	case "remove":
		if err := noArgs(args, cmd, "cloudflare"); err != nil {
			return plugin.Output{}, err
		}
		_, _ = t.run("systemctl", "disable", "--now", cfService)
		_ = env.FS.Remove(cfUnit)
		_ = env.FS.Remove(cfToken)
		_, _ = t.run("systemctl", "daemon-reload")
		return textOut("The tunnel is stopped and its token is deleted. Your sites on Cloudflare no longer reach this server.\n"), nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin cloudflare has no command %q", cmd)
}

func (Cloudflare) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if !t.ok("cloudflared", "--version") {
		return []plugin.Finding{finding("cloudflare", "installed", plugin.LevelOK, "Cloudflare Tunnel is not installed (not in use)", "")}
	}
	if _, err := env.FS.Stat(cfToken); err != nil {
		return []plugin.Finding{finding("cloudflare", "token", plugin.LevelWarn, "cloudflared is installed but no tunnel is connected", "echo TOKEN | lite cloudflare setup --token-stdin")}
	}
	if !t.ok("systemctl", "is-active", "--quiet", cfService) {
		return []plugin.Finding{finding("cloudflare", "service", plugin.LevelFail, "the Cloudflare Tunnel is not running: sites published through it are unreachable", "systemctl restart "+cfService)}
	}
	return []plugin.Finding{finding("cloudflare", "service", plugin.LevelOK, "the Cloudflare Tunnel is running", "")}
}
