package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const (
	serviceName  = "lite-ui"
	launchdLabel = "me.prozorov.lite-deploy"
	defaultAddr  = "127.0.0.1:8787"
)

// tokenPath is where the background program keeps its token. It is a file only you can read; with it the address of the
// page stays the same after a restart or a reboot, so it can be a bookmark.
func tokenPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "lite-ui", "token")
}

// loadToken returns the saved token; with create it makes one when there is none.
func loadToken(create bool) (string, error) {
	p := tokenPath()
	if p == "" {
		return "", errors.New("no place for the settings of the current user")
	}
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) >= 16 {
		return strings.TrimSpace(string(b)), nil
	}
	if !create {
		return "", os.ErrNotExist
	}
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	return tok, os.WriteFile(p, []byte(tok+"\n"), 0o600)
}

func pageURL(addr, token string) string { return fmt.Sprintf("http://%s/?t=%s", addr, token) }

// systemdUnit is the service of the current user (no root): it starts at login and is restarted if it stops.
func systemdUnit(exe string) string {
	return fmt.Sprintf(`[Unit]
Description=Lite Deploy (the page on your workstation)

[Service]
ExecStart=%q --service --no-open
Environment=LITE_UI_SERVICE=1
Restart=always
RestartSec=2

[Install]
WantedBy=default.target
`, exe)
}

// launchdPlist is the agent of the current user on macOS: it starts at login and is restarted if it stops.
func launchdPlist(exe, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>--service</string><string>--no-open</string></array>
<key>EnvironmentVariables</key><dict><key>LITE_UI_SERVICE</key><string>1</string></dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, launchdLabel, exe, logPath, logPath)
}

func isWSL() bool {
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}

// runService handles `lite-ui service install|uninstall|status`.
func runService(args []string, out, errw io.Writer) int {
	if len(args) != 1 || (args[0] != "install" && args[0] != "uninstall" && args[0] != "status") {
		_, _ = fmt.Fprintln(errw, "usage: lite-ui service install | uninstall | status")
		return 2
	}
	if runtime.GOOS == "windows" {
		_, _ = fmt.Fprintln(errw, "Running in the background is not available on Windows yet: start lite-ui from the Start menu or a terminal when you need the page.")
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR:", err)
		return 1
	}
	if r, e := filepath.EvalSymlinks(exe); e == nil {
		exe = r
	}
	ctx := context.Background()
	r := host.ExecRunner{}
	sh := func(name string, a ...string) (string, bool) {
		res, e := r.Run(ctx, host.Cmd{Name: name, Args: a})
		return strings.TrimSpace(string(res.Stdout) + string(res.Stderr)), e == nil && res.ExitCode == 0
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return macService(args[0], exe, home, out, errw, sh)
	}
	return linuxService(args[0], exe, home, out, errw, sh)
}

func linuxService(action, exe, home string, out, errw io.Writer, sh func(string, ...string) (string, bool)) int {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		msg := "This system does not run systemd, so lite-ui cannot be started in the background automatically."
		if isWSL() {
			msg = "systemd is off in this WSL. Turn it on once: put\n\n  [boot]\n  systemd=true\n\ninto /etc/wsl.conf, run `wsl --shutdown` in PowerShell, open WSL again and repeat this command.\nNote: WSL itself stops when no window is open; to keep the page always running use lite-ui on Windows instead."
		}
		_, _ = fmt.Fprintln(errw, msg)
		return 1
	}
	dir := filepath.Join(home, ".config", "systemd", "user")
	unit := filepath.Join(dir, serviceName+".service")
	switch action {
	case "install":
		tok, err := loadToken(true)
		if err != nil {
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 1
		}
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a user service folder
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 1
		}
		if err := os.WriteFile(unit, []byte(systemdUnit(exe)), 0o644); err != nil { //nolint:gosec // a unit file is not secret
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 1
		}
		for _, c := range [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", serviceName}} {
			if o, ok := sh("systemctl", c...); !ok {
				_, _ = fmt.Fprintln(errw, "ERROR: systemctl", strings.Join(c, " "), "failed:", o)
				return 1
			}
		}
		sh("loginctl", "enable-linger", os.Getenv("USER")) // keeps it running with no login; fine if refused
		_, _ = fmt.Fprintf(out, "Lite Deploy now runs in the background and starts by itself.\n\nOpen it (and make this a bookmark):\n\n  %s\n", pageURL(defaultAddr, tok))
	case "uninstall":
		sh("systemctl", "--user", "disable", "--now", serviceName)
		_ = os.Remove(unit)
		sh("systemctl", "--user", "daemon-reload")
		_, _ = fmt.Fprintln(out, "Lite Deploy no longer runs in the background. You can still start it with `lite-ui`.")
	case "status":
		o, ok := sh("systemctl", "--user", "is-active", serviceName)
		if !ok {
			_, _ = fmt.Fprintf(out, "Not running in the background (%s). Turn it on with `lite-ui service install`.\n", o)
			return 0
		}
		printRunning(out)
	}
	return 0
}

func macService(action, exe, home string, out, errw io.Writer, sh func(string, ...string) (string, bool)) int {
	plist := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	target := fmt.Sprintf("gui/%d", os.Getuid())
	switch action {
	case "install":
		tok, err := loadToken(true)
		if err != nil {
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 1
		}
		logPath := filepath.Join(home, "Library", "Logs", "lite-ui.log")
		if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil { //nolint:gosec // the user's own folder
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 1
		}
		if err := os.WriteFile(plist, []byte(launchdPlist(exe, logPath)), 0o644); err != nil { //nolint:gosec // an agent file is not secret
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 1
		}
		sh("launchctl", "bootout", target+"/"+launchdLabel) // a previous copy, if any
		if o, ok := sh("launchctl", "bootstrap", target, plist); !ok {
			_, _ = fmt.Fprintln(errw, "ERROR: launchctl bootstrap failed:", o)
			return 1
		}
		_, _ = fmt.Fprintf(out, "Lite Deploy now runs in the background and starts by itself.\n\nOpen it (and make this a bookmark):\n\n  %s\n", pageURL(defaultAddr, tok))
	case "uninstall":
		sh("launchctl", "bootout", target+"/"+launchdLabel)
		_ = os.Remove(plist)
		_, _ = fmt.Fprintln(out, "Lite Deploy no longer runs in the background. You can still start it with `lite-ui`.")
	case "status":
		if _, ok := sh("launchctl", "print", target+"/"+launchdLabel); !ok {
			_, _ = fmt.Fprintln(out, "Not running in the background. Turn it on with `lite-ui service install`.")
			return 0
		}
		printRunning(out)
	}
	return 0
}

func printRunning(out io.Writer) {
	tok, err := loadToken(false)
	if err != nil {
		_, _ = fmt.Fprintln(out, "Running in the background.")
		return
	}
	_, _ = fmt.Fprintf(out, "Running in the background:\n\n  %s\n", pageURL(defaultAddr, tok))
}
