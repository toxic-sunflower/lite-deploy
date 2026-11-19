package cli

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/ui"
)

// cmdUI starts the local web interface and waits for Ctrl+C.
func cmdUI(args []string, env Env) result {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	hostFlag := fs.String("host", "", "user@server: manage the server over SSH (without the flag: this computer)")
	listen := fs.String("listen", "127.0.0.1:8787", "address, loopback only")
	tokenFile := fs.String("token-file", "", "file with a permanent token (created with mode 0600); for running as a service")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail("ui", CodeUsage, "usage: lite-deploy ui [--host user@server] [--listen 127.0.0.1:8787] [--token-file path]")
	}
	h, _, err := net.SplitHostPort(*listen)
	if err != nil || (h != "127.0.0.1" && h != "localhost" && h != "::1") {
		return fail("ui", CodeUsage, "the interface listens on 127.0.0.1 only (never expose it to the internet)")
	}
	rnd := env.Rand
	if rnd == nil {
		return fail("ui", CodeInternal, "no source of random numbers")
	}
	token := ""
	if *tokenFile != "" {
		if b, err := os.ReadFile(*tokenFile); err == nil {
			token = strings.TrimSpace(string(b))
		}
	}
	if token == "" {
		raw := make([]byte, 16)
		if _, err := io.ReadFull(rnd, raw); err != nil {
			return fail("ui", CodeInternal, err.Error())
		}
		token = hex.EncodeToString(raw)
		if *tokenFile != "" {
			if err := os.WriteFile(*tokenFile, []byte(token+"\n"), 0o600); err != nil {
				return fail("ui", CodeIO, "could not write the token: "+err.Error())
			}
		}
	}
	bin, _ := os.Executable()
	srv := &ui.Server{Runner: env.Runner, Host: *hostFlag, Bin: bin, Token: token}
	if err := srv.Validate(); err != nil {
		return fail("ui", CodeUsage, err.Error())
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fail("ui", CodeIO, "could not take the address "+*listen+": "+err.Error())
	}
	_, _ = fmt.Fprintf(env.Stdout, "The interface is running. Open it in a browser:\n\n  http://%s/?t=%s\n\nStop with Ctrl+C.\n", ln.Addr().String(), srv.Token)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Serve(ctx, ln); err != nil {
		return fail("ui", CodeIO, err.Error())
	}
	return result{command: "ui", text: "The interface has stopped.\n"}
}
