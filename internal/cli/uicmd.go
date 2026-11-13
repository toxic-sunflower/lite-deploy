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

	"github.com/toxic-sunflower/lite-deploy/internal/ui"
)

// cmdUI запускает локальный веб-интерфейс и ждёт Ctrl+C.
func cmdUI(args []string, env Env) result {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	hostFlag := fs.String("host", "", "пользователь@сервер: управлять сервером по SSH (без флага: этот компьютер)")
	listen := fs.String("listen", "127.0.0.1:8787", "адрес, только loopback")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		return fail("ui", CodeUsage, "использование: lite-deploy ui [--host пользователь@сервер] [--listen 127.0.0.1:8787]")
	}
	h, _, err := net.SplitHostPort(*listen)
	if err != nil || (h != "127.0.0.1" && h != "localhost" && h != "::1") {
		return fail("ui", CodeUsage, "интерфейс слушает только 127.0.0.1 (в интернет его выставлять нельзя)")
	}
	rnd := env.Rand
	if rnd == nil {
		return fail("ui", CodeInternal, "нет источника случайных чисел")
	}
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rnd, raw); err != nil {
		return fail("ui", CodeInternal, err.Error())
	}
	bin, _ := os.Executable()
	srv := &ui.Server{Runner: env.Runner, Host: *hostFlag, Bin: bin, Token: hex.EncodeToString(raw)}
	if err := srv.Validate(); err != nil {
		return fail("ui", CodeUsage, err.Error())
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return fail("ui", CodeIO, "не удалось занять адрес "+*listen+": "+err.Error())
	}
	_, _ = fmt.Fprintf(env.Stdout, "Интерфейс запущен. Откройте в браузере:\n\n  http://%s/?t=%s\n\nОстановить: Ctrl+C.\n", ln.Addr().String(), srv.Token)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Serve(ctx, ln); err != nil {
		return fail("ui", CodeIO, err.Error())
	}
	return result{command: "ui", text: "Интерфейс остановлен.\n"}
}
