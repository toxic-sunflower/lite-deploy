package cli

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/agent"
	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
	"github.com/toxic-sunflower/lite-deploy/internal/serverconfig"
	"github.com/toxic-sunflower/lite-deploy/internal/setup"
)

// cmdConfig: `lite config export` writes the configuration of this server as a file that `lite init --config` can use on
// a new server. It holds only public things (public SSH keys, the public backup key), never a password or a secret.
func cmdConfig(args []string, env Env) result {
	const command = "config.export"
	if len(args) != 1 || args[0] != "export" {
		return fail(command, CodeUsage, "usage: lite-deploy config export")
	}
	ctx := context.Background()
	cfg := serverconfig.Config{Schema: serverconfig.Schema}

	// administrators and their public keys
	if group, err := agent.AdminGroup(ctx, env.Runner); err == nil {
		if res, err := env.Runner.Run(ctx, host.Cmd{Name: "getent", Args: []string{"group", group}}); err == nil && res.ExitCode == 0 {
			f := strings.Split(strings.TrimSpace(string(res.Stdout)), ":")
			if len(f) >= 4 {
				members := strings.Split(f[3], ",")
				sort.Strings(members)
				for _, name := range members {
					if name == "" {
						continue
					}
					u := serverconfig.User{Name: name}
					if home := homeOf(env, name); home != "" {
						if b, err := env.FS.ReadFile(home + "/.ssh/authorized_keys"); err == nil {
							for _, l := range strings.Split(string(b), "\n") {
								if setup.ValidKey(l) {
									u.SSHKeys = append(u.SSHKeys, strings.TrimSpace(l))
								}
							}
						}
					}
					cfg.Users = append(cfg.Users, u)
				}
			}
		}
	}

	// the firewall mode
	if st, e := (&firewall.Manager{Runner: env.Runner}).Status(ctx); e == nil {
		switch {
		case !st.Active:
			cfg.Firewall = "off"
		case openTo(st, 80) && openTo(st, 443):
			cfg.Firewall = "http"
		default:
			cfg.Firewall = "max"
		}
	}

	// the programs that are installed
	pe := env.pluginEnv()
	r, _ := registry(env)
	want := map[string]bool{"caddy": true, "podman": true, "pg": true, "redis": true, "cloudflare": true, "backblaze": true, "s3": true, "sftp": true}
	for _, p := range r.List() {
		if pr, ok := p.(plugin.Presence); ok && want[p.Name()] && pr.Installed(ctx, pe) {
			cfg.Plugins = append(cfg.Plugins, p.Name())
		}
	}
	sort.Strings(cfg.Plugins)

	// protection
	if _, err := env.FS.Stat(builtin.SSHSecurity); err != nil {
		off := false
		cfg.Security = &off
	}

	// backups (public values only)
	if b, err := env.FS.ReadFile("/etc/lite-deploy/backup.json"); err == nil {
		var bc struct {
			Recipient    string `json:"recipient"`
			EncryptDumps bool   `json:"encrypt_dumps"`
		}
		if json.Unmarshal(b, &bc) == nil && bc.Recipient != "" {
			sched := false
			if res, err := env.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"is-enabled", "--quiet", "lite-backup.timer"}}); err == nil && res.ExitCode == 0 {
				sched = true
			}
			cfg.Backup = &serverconfig.Backup{Recipient: bc.Recipient, EncryptDumps: bc.EncryptDumps, Schedule: sched}
		}
	}

	pretty, _ := json.MarshalIndent(cfg, "", "  ")
	return result{command: command, data: map[string]any{"config": cfg}, text: string(pretty) + "\n"}
}

// homeOf finds the home directory of a user in /etc/passwd.
func homeOf(env Env, name string) string {
	b, err := env.FS.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) >= 6 && f[0] == name {
			return f[5]
		}
	}
	return ""
}
