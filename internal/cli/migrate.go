package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
)

// migrations are the steps that are done ONCE when a server moves to a version that needs them, in order. Each has a number
// that never changes; the last number done is kept in migrationState. (The settings lite manages are re-applied on every
// run by cmdMigrate itself, so a step is only for things that must happen once, for example moving a file.)
type migration struct {
	ID   int
	Name string
	Run  func(env Env) error
}

var migrations = []migration{}

// migrationState holds the number of the last migration that was done.
const migrationState = "/var/lib/lite-deploy/migrated"

// cmdMigrate brings the settings that lite manages on the server up to date with the installed version. liteup runs it
// after every update, so a person never has to run anything by hand after upgrading. It only rewrites what lite itself
// manages and only where that is already switched on, and it keeps choices the person made (for example "no-root").
func cmdMigrate(env Env) result {
	const command = "migrate"
	if !env.root() {
		return fail(command, "permission_denied", "migrate runs as root (liteup runs it after an update)")
	}
	var done []string
	if b, err := env.FS.ReadFile(builtin.SSHSecurity); err == nil {
		var args []string
		if strings.Contains(string(b), "PermitRootLogin no") {
			args = []string{"no-root"}
		}
		if _, err := (builtin.Security{}).Run(context.Background(), env.pluginEnv(), "enable", args); err != nil {
			return fail(command, "step_failed", "server protection: "+err.Error())
		}
		done = append(done, "server protection")
	}
	if changed, err := reconcileSwitchHelper(env); err != nil {
		return fail(command, "step_failed", "traffic switch helper: "+err.Error())
	} else if changed {
		done = append(done, "traffic switch helper")
	}
	last := 0
	if b, err := env.FS.ReadFile(migrationState); err == nil {
		last, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	for _, m := range migrations {
		if m.ID <= last {
			continue
		}
		if err := m.Run(env); err != nil {
			return fail(command, "step_failed", fmt.Sprintf("step %d (%s): %v", m.ID, m.Name, err))
		}
		last = m.ID
		_ = env.FS.MkdirAll("/var/lib/lite-deploy", 0o755)
		if err := env.FS.WriteFile(migrationState, []byte(strconv.Itoa(last)+"\n"), 0o644); err != nil {
			return fail(command, CodeIO, err.Error())
		}
		done = append(done, m.Name)
	}
	text := "Nothing to bring up to date.\n"
	if len(done) > 0 {
		text = "Brought up to date: " + strings.Join(done, ", ") + ".\n"
	}
	return result{command: command, data: map[string]any{"updated": done}, text: text}
}

// reconcileSwitchHelper moves a server from the old way of switching traffic (the project user writes the Caddy upstream file and
// runs a sudo rule) to the root helper: once the helper is installed, the upstream files become root-owned and the sudo rules
// of lite-deploy go away. It is safe to run again, and does nothing while the helper is missing.
func reconcileSwitchHelper(env Env) (bool, error) {
	if _, err := env.FS.Stat("/etc/systemd/system/lite-deploy-switch.socket"); err != nil {
		return false, nil
	}
	changed := false
	if names, err := env.FS.ReadDir("/etc/caddy/upstreams"); err == nil {
		for _, n := range names {
			if !strings.HasSuffix(n, ".caddy") {
				continue
			}
			p := "/etc/caddy/upstreams/" + n
			if info, err := env.FS.Stat(p); err == nil && info.Owner != "root" {
				if err := env.FS.Chown(p, "root", "root"); err != nil {
					return changed, err
				}
				changed = true
			}
		}
	}
	if names, err := env.FS.ReadDir("/etc/sudoers.d"); err == nil {
		for _, n := range names {
			if !strings.HasPrefix(n, "lite-deploy-") {
				continue
			}
			p := "/etc/sudoers.d/" + n
			if b, err := env.FS.ReadFile(p); err == nil && strings.Contains(string(b), "NOPASSWD: /usr/bin/systemctl reload caddy") {
				if err := env.FS.Remove(p); err != nil {
					return changed, err
				}
				changed = true
			}
		}
	}
	return changed, nil
}
