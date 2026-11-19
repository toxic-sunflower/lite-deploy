// Command lite-ui is the web interface of lite-deploy for your own computer. It is a separate small program: it
// contains only the interface, reaches the server over SSH with your key, and opens the page in your browser.
// Nothing of it is ever installed on the server.
package main

import (
	"bufio"
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
	"github.com/toxic-sunflower/lite-deploy/internal/ui"
)

// Version is set by a release build: -ldflags "-X main.Version=v1.2.3".
var Version = "0.0.0-dev"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, in io.Reader, out, errw io.Writer) int {
	fs := flag.NewFlagSet("lite-ui", flag.ContinueOnError)
	fs.SetOutput(errw)
	hostFlag := fs.String("host", "", "the server as USER@ADDRESS (remembered for next time)")
	listen := fs.String("listen", "127.0.0.1:8787", "local address of the page (loopback only)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
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
	server := *hostFlag
	saved := savedHostPath()
	if server == "" {
		if b, err := os.ReadFile(filepath.Clean(saved)); err == nil {
			server = strings.TrimSpace(string(b))
		}
	}
	if server == "" {
		_, _ = fmt.Fprint(out, "Your server as USER@ADDRESS (for example serafim@203.0.113.7): ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		server = strings.TrimSpace(line)
	}
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR:", err)
		return 1
	}
	runner := host.ExecRunner{}
	srv := &ui.Server{Runner: runner, Host: server, Token: hex.EncodeToString(raw)}
	if server == "" {
		_, _ = fmt.Fprintln(errw, "ERROR: the server address is needed: lite-ui --host USER@ADDRESS")
		return 2
	}
	if err := srv.Validate(); err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR:", err)
		return 2
	}
	if saved != "" && server != "" {
		_ = os.MkdirAll(filepath.Dir(saved), 0o700)
		_ = os.WriteFile(saved, []byte(server+"\n"), 0o600)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		_, _ = fmt.Fprintf(errw, "ERROR: could not take %s (is lite-ui already running?): %v\n", *listen, err)
		return 1
	}
	url := fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), srv.Token)
	_, _ = fmt.Fprintf(out, "The interface for %s is running.\n\n  %s\n\nIt opens in your browser. Close this window or press Ctrl+C to stop.\n", server, url)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if !*noOpen {
		go openBrowser(ctx, runner, url)
	}
	if err := srv.Serve(ctx, ln); err != nil {
		_, _ = fmt.Fprintln(errw, "ERROR:", err)
		return 1
	}
	return 0
}

func savedHostPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "lite-ui", "server")
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
