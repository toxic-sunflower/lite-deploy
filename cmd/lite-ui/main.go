// Command lite-ui is the web interface of lite-deploy for your own computer. It is a separate small program: it
// contains only the interface, reaches the server over SSH with your key, and opens the page in your browser.
// Nothing of it is ever installed on the server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/mcp"
	"github.com/toxic-sunflower/lite-deploy/internal/ui"
	"github.com/toxic-sunflower/lite-deploy/internal/uiupdate"
)

// Version is set by a release build: -ldflags "-X main.Version=v1.2.3".
var Version = "0.0.0-dev"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) > 0 && args[0] == "mcp" {
		return runMCP(args[1:], in, out, errw)
	}
	if len(args) > 0 && args[0] == "update" {
		return runUpdate(args[1:], out, errw)
	}
	if len(args) > 0 && args[0] == "service" {
		return runService(args[1:], out, errw)
	}
	fs := flag.NewFlagSet("lite-ui", flag.ContinueOnError)
	fs.SetOutput(errw)
	hostFlag := fs.String("host", "", "add a server as USER@ADDRESS to your list")
	listen := fs.String("listen", "127.0.0.1:8787", "local address of the page (loopback only)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	service := fs.Bool("service", false, "run in the background with a fixed address (used by `lite-ui service install`)")
	version := fs.Bool("version", false, "print the version")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(errw, "usage: lite-ui [--host USER@ADDRESS] [--no-open]")
		return 2
	}
	if *version {
		_, _ = fmt.Fprintf(out, "lite-ui %s\n", Version)
		return 0
	}
	h, _, err := net.SplitHostPort(*listen)
	if err != nil || (h != "127.0.0.1" && h != "localhost" && h != "::1") {
		_, _ = fmt.Fprintln(errw, "ERROR: the page listens on 127.0.0.1 only (it is never exposed to the internet)")
		return 2
	}
	list := &ui.ServerList{Path: savedListPath()}
	if *hostFlag != "" {
		if err := list.Add(*hostFlag); err != nil {
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 2
		}
	}
	if *service {
		host.HideConsole()
	}
	token := os.Getenv("LITE_UI_TOKEN") // handed over by the previous run after an update, so an open page keeps working
	if *service && token == "" {
		tok, err := loadToken(true)
		if err != nil {
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 1
		}
		token = tok
	}
	if token == "" {
		raw := make([]byte, 16)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 1
		}
		token = hex.EncodeToString(raw)
	}
	runner := host.ExecRunner{}
	srv := &ui.Server{Runner: runner, Servers: list, Token: token, SSHOptions: sshSharing(), Streamer: runner, OwnerKeyPath: ownerKeyPath()}
	upd := uiupdate.New(Version)
	upd.Cleanup()
	srv.Updater = upd
	if err := srv.Validate(); err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR:", err)
		return 2
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil && !*service {
		// the background copy already holds the address: open its page instead of failing
		if tok, terr := loadToken(false); terr == nil {
			u := pageURL(*listen, tok)
			_, _ = fmt.Fprintf(out, "Lite Deploy is already running in the background:\n\n  %s\n", u)
			if !*noOpen {
				openBrowser(context.Background(), runner, u)
			}
			return 0
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(errw, "ERROR: could not take %s (is lite-ui already running?): %v\n", *listen, err)
		return 1
	}
	url := fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), srv.Token)
	_, _ = fmt.Fprintf(out, "The interface is running.\n\n  %s\n\nIt opens in your browser; choose or add your server there. Close this window or press Ctrl+C to stop.\n", url)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	updated := false
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv.OnUpdated = func() { updated = true; cancel() }
	if !*noOpen {
		go openBrowser(ctx, runner, url)
	}
	if err := srv.Serve(ctx, ln); err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR:", err)
		return 1
	}
	if updated {
		return restart(upd.Exe, args, token, out, errw)
	}
	return 0
}

// restart starts the new program in place of the old one with the same arguments and the same token, so the open page
// reconnects by itself. Under a service manager (it sets LITE_UI_SERVICE) the manager starts the new program instead.
func restart(exe string, args []string, token string, out, errw io.Writer) int {
	if os.Getenv("LITE_UI_SERVICE") != "" {
		return 0
	}
	args = append([]string{"--no-open"}, withoutNoOpen(args)...)
	if err := host.StartDetached(exe, args, []string{"LITE_UI_TOKEN=" + token}, out, errw); err != nil {
		_, _ = fmt.Fprintln(errw, "Updated. Start lite-ui again to use the new version:", err)
		return 1
	}
	return 0
}

func withoutNoOpen(args []string) []string {
	var r []string
	for _, a := range args {
		if a != "--no-open" && a != "-no-open" {
			r = append(r, a)
		}
	}
	return r
}

func savedListPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "lite-ui", "servers")
}

// openBrowser tries the usual openers of the system; a failure is not an error: the address is printed anyway.
func openBrowser(ctx context.Context, r host.Runner, url string) {
	var tries [][]string
	switch runtime.GOOS {
	case "windows":
		tries = [][]string{{"rundll32", "url.dll,FileProtocolHandler", url}}
	case "darwin":
		tries = [][]string{{"open", url}}
	default:
		if b, err := os.ReadFile("/proc/version"); err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft") {
			tries = append(tries, []string{"wslview", url}, []string{"cmd.exe", "/c", "start", "", strings.ReplaceAll(url, "&", "^&")})
		}
		tries = append(tries, []string{"xdg-open", url})
	}
	for _, t := range tries {
		if res, err := r.Run(ctx, host.Cmd{Name: t[0], Args: t[1:]}); err == nil && res.ExitCode == 0 {
			return
		}
	}
}

// sshSharing makes all requests to a server share one ssh connection where the system allows it (not on Windows),
// so the page does not open a new login for every request.
func sshSharing() []string {
	// A server seen for the first time is accepted (and remembered), a server whose key CHANGED is refused. Without this a
	// non-interactive ssh stops at the first question "do you trust this host".
	opts := []string{"-o", "StrictHostKeyChecking=accept-new"}
	if runtime.GOOS == "windows" {
		return opts
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("lite-ui-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return opts
	}
	return append(opts, "-o", "ControlMaster=auto", "-o", "ControlPath="+filepath.Join(dir, "%C"), "-o", "ControlPersist=120")
}

// ownerKeyPath is where the key that confirms dangerous actions is kept (next to the list of servers).
func ownerKeyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "lite-ui", "owner.key")
}

// runMCP serves the Model Context Protocol on standard input and output: an AI assistant can read the state of your servers
// and deploy or roll back, through the same list of servers and the same ssh connection as the page. It never signs
// dangerous changes with your key; those stay in the interface.
func runMCP(args []string, in io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet("lite-ui mcp", flag.ContinueOnError)
	fs.SetOutput(errw)
	server := fs.String("server", "", "a server as USER@ADDRESS to add to your list (the Claude Desktop extension passes the one you typed)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	list := &ui.ServerList{Path: savedListPath()}
	if s := strings.TrimSpace(*server); s != "" {
		if err := list.Add(s); err != nil {
			_, _ = fmt.Fprintln(errw, "ERROR:", err)
			return 2
		}
	}
	runner := host.ExecRunner{}
	srv := &ui.Server{Runner: runner, Servers: list, Token: "mcp-mode-has-no-web-page", SSHOptions: sshSharing(), Streamer: runner}
	m := &mcp.Server{Name: "lite-deploy", Version: Version, Tools: srv.MCPTools()}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := m.Serve(ctx, in, out); err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR:", err)
		return 1
	}
	return 0
}

// runUpdate is `lite-ui update [--check]`: look for a newer release and install it (the same as the button on the page).
func runUpdate(args []string, out, errw io.Writer) int {
	check := len(args) == 1 && args[0] == "--check"
	if len(args) > 1 || (len(args) == 1 && !check) {
		_, _ = fmt.Fprintln(errw, "usage: lite-ui update [--check]")
		return 2
	}
	upd := uiupdate.New(Version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	info, err := upd.Check(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR: could not ask for the newest version:", err)
		return 1
	}
	if !info.Available {
		if _, ok := uiupdate.Parse(Version); !ok {
			_, _ = fmt.Fprintf(out, "This is a development build (%s); the newest release is %s.\n", Version, info.Latest)
			return 0
		}
		_, _ = fmt.Fprintf(out, "lite-ui %s is up to date.\n", Version)
		return 0
	}
	_, _ = fmt.Fprintf(out, "A newer lite-ui exists: %s (you have %s).\n", info.Latest, Version)
	if check {
		_, _ = fmt.Fprintln(out, "Install it with: lite-ui update")
		return 0
	}
	tag, err := upd.Apply(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR:", err)
		return 1
	}
	_, _ = fmt.Fprintf(out, "Updated to %s. If lite-ui runs in the background, restart it:\n  Linux/WSL: systemctl --user restart lite-ui\n  macOS: launchctl kickstart -k gui/$(id -u)/%s\n  Windows: lite-ui service install\n", tag, launchdLabel)
	return 0
}
