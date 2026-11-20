package cli

import (
	"context"
	"flag"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Repo is the GitHub repository the releases come from.
const Repo = "toxic-sunflower/lite-deploy"

// liteupPath is where liteup lives on a server.
const liteupPath = "/usr/local/bin/liteup"

const updateUsage = "usage: lite update [--check] [--yes]"

// latestVersion asks GitHub for the tag of the latest release (the redirect of /releases/latest).
func latestVersion(ctx context.Context, h host.HTTPClient) (string, error) {
	if h == nil {
		return "", fmt.Errorf("no network client")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	r, err := h.Get(ctx, "https://github.com/"+Repo+"/releases/latest", "")
	if err != nil {
		return "", err
	}
	loc := r.Header.Get("Location")
	tag := path.Base(loc)
	if loc == "" || !strings.HasPrefix(tag, "v") || parseVersion(tag) == nil {
		return "", fmt.Errorf("could not read the latest version from GitHub")
	}
	return tag, nil
}

// parseVersion turns v1.2.3 into numbers; nil if it is not a release version (a development build).
func parseVersion(v string) []int {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

// newer reports whether latest is a higher release than current. A development build never reports an update.
func newer(latest, current string) bool {
	l, c := parseVersion(latest), parseVersion(current)
	if l == nil || c == nil {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// cmdUpdate: `lite update --check` tells whether a newer release exists; `lite update` installs it (through the agent,
// without sudo). The server side starts `liteup update` as a separate systemd unit, because the update restarts the agent
// that is answering the request.
func cmdUpdate(args []string, env Env) result {
	const command = "update"
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	check := fs.Bool("check", false, "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail(command, CodeUsage, updateUsage)
	}
	latest, lerr := latestVersion(context.Background(), env.HTTP)
	if *check {
		if lerr != nil {
			return fail(command, CodeIO, "could not check for a new version: "+lerr.Error())
		}
		avail := newer(latest, Version)
		text := fmt.Sprintf("You have %s. The latest is %s.\n", Version, latest)
		if avail {
			text += "A new version is available: run lite update.\n"
		}
		return result{command: command, data: map[string]any{"current": Version, "latest": latest, "available": avail}, text: text}
	}
	if !env.root() {
		return forwardProject(env, command, "/v1/update/apply", map[string]string{})
	}
	if lerr != nil {
		return fail(command, CodeIO, "could not check for a new version: "+lerr.Error())
	}
	if !newer(latest, Version) {
		return result{command: command, data: map[string]any{"current": Version, "latest": latest, "started": false}, text: fmt.Sprintf("Already up to date: %s.\n", Version)}
	}
	if !*yes && (env.Interactive == nil || !env.Interactive()) {
		return fail(command, "confirmation_required", "this changes the system; run again with --yes")
	}
	res, rerr := env.Runner.Run(context.Background(), host.Cmd{Name: "systemd-run", Args: []string{"--unit=lite-update", "--collect", "--no-block", liteupPath, "update"}, Timeout: 30 * time.Second})
	if rerr != nil || res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if rerr != nil {
			msg = rerr.Error()
		}
		return fail(command, "step_failed", "could not start the update: "+msg)
	}
	return result{command: command, data: map[string]any{"current": Version, "latest": latest, "started": true},
		text: fmt.Sprintf("The update to %s has started. It takes about a minute and restarts the agent. Check with: lite version\n", latest)}
}
