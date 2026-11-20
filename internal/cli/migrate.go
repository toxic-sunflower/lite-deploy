package cli

import (
	"context"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
)

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
	text := "Nothing to bring up to date.\n"
	if len(done) > 0 {
		text = "Brought up to date: " + strings.Join(done, ", ") + ".\n"
	}
	return result{command: command, data: map[string]any{"updated": done}, text: text}
}
