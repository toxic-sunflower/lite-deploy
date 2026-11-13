// Package ui локальный веб-интерфейс: показывает состояние проектов и даёт кнопки «Выкатить» и «Откатить».
// Интерфейс ничего не решает сам: каждое действие это вызов CLI (`lite-deploy ... --json`) на этом компьютере
// или на сервере по SSH. Слушает только 127.0.0.1; действия требуют токена из адреса, который печатает команда.
package ui

import (
	"context"
	"crypto/subtle"
	_ "embed" // страница интерфейса
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

//go:embed index.html
var page []byte

var (
	projectRe = regexp.MustCompile(`\A[a-z][a-z0-9-]{1,30}\z`)
	shaRe     = regexp.MustCompile(`\A[0-9a-f]{40}\z`)
	hostRe    = regexp.MustCompile(`\A[A-Za-z0-9_][A-Za-z0-9._@:-]{0,100}\z`)
)

// Server интерфейс.
type Server struct {
	Runner host.Runner
	// Host: пользователь@сервер. Пусто: команды выполняются на этом же компьютере (Bin).
	Host string
	// Bin путь к lite-deploy для локального режима.
	Bin   string
	Token string
	// Timeout на одно действие; выкатка может идти минуты.
	Timeout time.Duration
}

// Validate проверяет настройки.
func (s *Server) Validate() error {
	if s.Host != "" && !hostRe.MatchString(s.Host) {
		return errors.New("неверный адрес сервера, ожидается пользователь@сервер")
	}
	if len(s.Token) < 16 {
		return errors.New("токен слишком короткий")
	}
	return nil
}

// cli запускает lite-deploy с проверенными аргументами и возвращает одну строку JSON-ответа.
func (s *Server) cli(ctx context.Context, args ...string) []byte {
	args = append(args, "--json")
	var cmd host.Cmd
	if s.Host != "" {
		a := append([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", s.Host, "sudo", "-n", "lite-deploy"}, args...)
		cmd = host.Cmd{Name: "ssh", Args: a}
	} else {
		bin := s.Bin
		if bin == "" {
			bin = "lite-deploy"
		}
		cmd = host.Cmd{Name: bin, Args: args}
	}
	cmd.Timeout = s.Timeout
	if cmd.Timeout == 0 {
		cmd.Timeout = 15 * time.Minute
	}
	res, err := s.Runner.Run(ctx, cmd)
	out := strings.TrimSpace(string(res.Stdout))
	if err == nil && strings.HasPrefix(out, "{") {
		return []byte(out)
	}
	msg := strings.TrimSpace(string(res.Stderr))
	if err != nil {
		msg = err.Error()
	}
	if msg == "" {
		msg = "команда не вернула ответа"
	}
	b, _ := json.Marshal(map[string]any{"ok": false, "api_version": "v1", "command": "ui",
		"error": map[string]string{"code": "internal", "message": msg}})
	return b
}

// Handler HTTP-обработчик интерфейса.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/api/info", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		b, _ := json.Marshal(map[string]string{"host": s.Host})
		return b, 200
	}))
	mux.HandleFunc("/api/status", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.cli(r.Context(), "status"), 200
	}))
	mux.HandleFunc("/api/deploy", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Project, SHA string }
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "неверный запрос"), code
		}
		if !projectRe.MatchString(in.Project) {
			return errBody("usage", "неверное имя проекта"), 400
		}
		if !shaRe.MatchString(in.SHA) {
			return errBody("invalid_sha", "нужен полный хэш коммита: 40 символов 0-9a-f"), 400
		}
		return s.cli(r.Context(), "deploy", in.Project, in.SHA), 200
	}))
	mux.HandleFunc("/api/rollback", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Project string }
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "неверный запрос"), code
		}
		if !projectRe.MatchString(in.Project) {
			return errBody("usage", "неверное имя проекта"), 400
		}
		return s.cli(r.Context(), "rollback", in.Project), 200
	}))
	mux.HandleFunc("/api/firewall", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.cli(r.Context(), "firewall", "status"), 200
	}))
	mux.HandleFunc("/api/firewall/allow", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		in, code := fwRule(r)
		if code != 0 {
			return errBody("usage", "неверное правило: порт 1-65535, протокол tcp или udp, источник адрес или сеть"), code
		}
		args := []string{"firewall", "allow", fmt.Sprintf("%d/%s", in.Port, in.Proto)}
		if in.From != "" {
			args = append(args, "--from", in.From)
		}
		return s.cli(r.Context(), append(args, "--yes")...), 200
	}))
	mux.HandleFunc("/api/firewall/remove", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		in, code := fwRule(r)
		if code != 0 {
			return errBody("usage", "неверное правило: порт 1-65535, протокол tcp или udp, источник адрес или сеть"), code
		}
		if in.Action != "allow" && in.Action != "limit" && in.Action != "deny" {
			return errBody("usage", "неверное действие правила"), 400
		}
		args := []string{"firewall", "remove", fmt.Sprintf("%d/%s", in.Port, in.Proto), "--action", in.Action}
		if in.From != "" && in.From != "any" {
			args = append(args, "--from", in.From)
		}
		return s.cli(r.Context(), append(args, "--yes")...), 200
	}))
	mux.HandleFunc("/api/firewall/enable", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{}
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "неверный запрос"), code
		}
		return s.cli(r.Context(), "firewall", "enable", "--yes"), 200
	}))
	return s.guard(mux)
}

type fwIn struct {
	Port   int    `json:"port"`
	Proto  string `json:"proto"`
	From   string `json:"from"`
	Action string `json:"action"`
}

// fwRule читает и проверяет правило брандмауэра из тела запроса.
func fwRule(r *http.Request) (fwIn, int) {
	var in fwIn
	if code := readBody(r, &in); code != 0 {
		return in, code
	}
	if e := firewall.ValidateRule(in.Port, in.Proto, in.From); e != nil {
		return in, 400
	}
	return in, 0
}

func errBody(code, msg string) []byte {
	b, _ := json.Marshal(map[string]any{"ok": false, "api_version": "v1", "command": "ui", "error": map[string]string{"code": code, "message": msg}})
	return b
}

func readBody(r *http.Request, v any) int {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return http.StatusUnsupportedMediaType
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return http.StatusBadRequest
	}
	return 0
}

// api оборачивает обработчик API: метод, токен.
func (s *Server) api(method string, h func(*http.Request) ([]byte, int)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "метод не разрешён", http.StatusMethodNotAllowed)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(s.Token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write(errBody("permission_denied", "нужен токен из адреса, который напечатала команда lite-deploy ui"))
			return
		}
		b, code := h(r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_, _ = w.Write(b)
	}
}

// guard защищает от обращений с чужих сайтов: принимается только Host вида localhost или 127.0.0.1
// (защита от DNS rebinding), а для запросов из браузера Origin должен совпадать с Host.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			h = r.Host
		}
		if h != "127.0.0.1" && h != "localhost" && h != "[::1]" && h != "::1" {
			http.Error(w, "неверный Host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "чужой Origin", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Serve слушает addr (только loopback) до отмены ctx.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sc)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
