// Package ui is the local web interface: shows the state of projects and gives the "Deploy" and "Roll back" buttons.
// The interface decides nothing itself: every action is a CLI call (`lite-deploy ... --json`) on this computer
// or on the server over SSH. It listens on 127.0.0.1 only; actions need the token from the address the command prints.
package ui

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

//go:embed web
var webFiles embed.FS

var (
	projectRe = regexp.MustCompile(`\A[a-z][a-z0-9-]{1,30}\z`)
	siteRe    = regexp.MustCompile(`\A[a-z][a-z0-9-]{0,15}\z`)
	shaRe     = regexp.MustCompile(`\A[0-9a-f]{40}\z`)
	hostRe    = regexp.MustCompile(`\A[A-Za-z0-9_][A-Za-z0-9._@:-]{0,100}\z`)
	pluginRe  = regexp.MustCompile(`\A[a-z][a-z0-9-]{0,30}\z`)
)

// Server is the interface.
type Server struct {
	Runner host.Runner
	// Host: user@server. Empty: commands run on this same computer (Bin).
	Host string
	// Bin is the path to lite-deploy for local mode.
	Bin   string
	Token string
	// Timeout for one action; a deploy can take minutes.
	Timeout time.Duration
	// Servers is the person's list of servers; the page picks one for every request (header X-Server).
	// nil: the single Host is used (tests, a fixed server).
	Servers *ServerList
}

type ctxKey struct{}

// ServerList is the list of servers the person manages, kept in a file (one USER@ADDRESS per line).
type ServerList struct {
	Path string
	mu   sync.Mutex
}

// List returns the servers in the order of use (the most recently added first).
func (l *ServerList) List() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.read()
}

func (l *ServerList) read() []string {
	b, err := os.ReadFile(filepath.Clean(l.Path))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if h := strings.TrimSpace(line); h != "" && hostRe.MatchString(h) {
			out = append(out, h)
		}
	}
	return out
}

func (l *ServerList) write(list []string) error {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(l.Path, []byte(strings.Join(list, "\n")+"\n"), 0o600)
}

// Add puts a server first (or moves it there).
func (l *ServerList) Add(host string) error {
	if !hostRe.MatchString(host) || !strings.Contains(host, "@") {
		return errors.New("the server must look like USER@ADDRESS, for example serafim@203.0.113.7")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	list := []string{host}
	for _, h := range l.read() {
		if h != host {
			list = append(list, h)
		}
	}
	return l.write(list)
}

// Remove deletes a server from the list.
func (l *ServerList) Remove(host string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var list []string
	for _, h := range l.read() {
		if h != host {
			list = append(list, h)
		}
	}
	return l.write(list)
}

// Has tells whether the server is in the list.
func (l *ServerList) Has(host string) bool {
	for _, h := range l.List() {
		if h == host {
			return true
		}
	}
	return false
}

// Validate validates the settings.
func (s *Server) Validate() error {
	if s.Host != "" && !hostRe.MatchString(s.Host) {
		return errors.New("invalid server address, expected user@server")
	}
	if len(s.Token) < 16 {
		return errors.New("the token is too short")
	}
	return nil
}

// call talks to the agent on the server: `lite-deploy call METHOD PATH` (the request body on standard input).
// For a server over SSH the command runs there, without sudo: system administrators (group sudo or wheel) can reach the agent socket.
func (s *Server) call(ctx context.Context, method, path string, body any) []byte {
	var in []byte
	if body != nil {
		in, _ = json.Marshal(body)
	}
	target := s.Host
	if s.Servers != nil {
		target, _ = ctx.Value(ctxKey{}).(string)
		if target == "" {
			return errBody("no_server", "no server is selected: add one with \"Add server\" at the top of the page")
		}
		if !s.Servers.Has(target) {
			return errBody("unknown_server", "this server is not in your list: choose another or add it")
		}
	}
	var cmd host.Cmd
	if target != "" {
		cmd = host.Cmd{Name: "ssh", Args: []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", target, "lite-deploy", "call", method, path}}
	} else {
		bin := s.Bin
		if bin == "" {
			bin = "lite-deploy"
		}
		cmd = host.Cmd{Name: bin, Args: []string{"call", method, path}}
	}
	cmd.Stdin = in
	cmd.Timeout = s.Timeout
	if cmd.Timeout == 0 {
		cmd.Timeout = 30 * time.Minute
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
		msg = "the command returned no answer"
	}
	b, _ := json.Marshal(map[string]any{"ok": false, "api_version": "v1", "command": "ui",
		"error": map[string]string{"code": "internal", "message": msg, "hint": "the agent must be running on the server: it is installed by liteup (curl -fsSL https://lite-deploy.prozorov.me/liteup | sudo sh), and the user must be a system administrator (sudo or wheel)"}})
	return b
}

// Handler is the HTTP handler of the interface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, err := webFiles.ReadFile("web/index.html")
		if err != nil {
			http.Error(w, "the page is missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	if sub, err := fs.Sub(webFiles, "web"); err == nil {
		static := http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
		mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			static.ServeHTTP(w, r)
		})
	}
	mux.HandleFunc("/api/info", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		b, _ := json.Marshal(map[string]string{"host": s.Host})
		return b, 200
	}))
	mux.HandleFunc("/api/servers", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		list := []string{}
		if s.Servers != nil {
			list = append(list, s.Servers.List()...)
		} else if s.Host != "" {
			list = append(list, s.Host)
		}
		b, _ := json.Marshal(map[string]any{"servers": list, "editable": s.Servers != nil})
		return b, 200
	}))
	mux.HandleFunc("/api/servers/add", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Host string }
		if code := readBody(r, &in); code != 0 || s.Servers == nil {
			return errBody("usage", "invalid request"), 400
		}
		if err := s.Servers.Add(strings.TrimSpace(in.Host)); err != nil {
			return errBody("usage", err.Error()), 400
		}
		return []byte(`{"ok":true}`), 200
	}))
	mux.HandleFunc("/api/servers/remove", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Host string }
		if code := readBody(r, &in); code != 0 || s.Servers == nil {
			return errBody("usage", "invalid request"), 400
		}
		if err := s.Servers.Remove(strings.TrimSpace(in.Host)); err != nil {
			return errBody("internal", err.Error()), 500
		}
		return []byte(`{"ok":true}`), 200
	}))
	mux.HandleFunc("/api/update", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.call(r.Context(), "GET", "/v1/update", nil), 200
	}))
	mux.HandleFunc("/api/update/apply", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		return s.call(r.Context(), "POST", "/v1/update/apply", map[string]string{}), 200
	}))
	mux.HandleFunc("/api/ingress", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.call(r.Context(), "GET", "/v1/ingress", nil), 200
	}))
	mux.HandleFunc("/api/publish", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Project, Site, Mode string }
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		if !projectRe.MatchString(in.Project) || !siteRe.MatchString(in.Site) || (in.Mode != "tunnel" && in.Mode != "direct") {
			return errBody("usage", "invalid request"), 400
		}
		return s.call(r.Context(), "POST", "/v1/publish", map[string]string{"project": in.Project, "site": in.Site, "mode": in.Mode}), 200
	}))
	mux.HandleFunc("/api/status", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.call(r.Context(), "GET", "/v1/status", nil), 200
	}))
	mux.HandleFunc("/api/deploy", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Project, SHA string }
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		if !projectRe.MatchString(in.Project) {
			return errBody("usage", "invalid project name"), 400
		}
		if !shaRe.MatchString(in.SHA) {
			return errBody("invalid_sha", "the full commit hash is required: 40 characters 0-9a-f"), 400
		}
		return s.call(r.Context(), "POST", "/v1/deploy", map[string]string{"project": in.Project, "sha": in.SHA}), 200
	}))
	mux.HandleFunc("/api/rollback", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Project string }
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		if !projectRe.MatchString(in.Project) {
			return errBody("usage", "invalid project name"), 400
		}
		return s.call(r.Context(), "POST", "/v1/rollback", map[string]string{"project": in.Project}), 200
	}))
	mux.HandleFunc("/api/plugins", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.call(r.Context(), "GET", "/v1/plugins", nil), 200
	}))
	mux.HandleFunc("/api/verify", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.call(r.Context(), "GET", "/v1/verify", nil), 200
	}))
	mux.HandleFunc("/api/fix", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{}
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		return s.call(r.Context(), "POST", "/v1/fix", map[string]string{}), 200
	}))
	mux.HandleFunc("/api/plugin", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct {
			Plugin  string   `json:"plugin"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		if !pluginRe.MatchString(in.Plugin) || !pluginRe.MatchString(in.Command) || !plugin.ValidArgs(in.Args) {
			return errBody("usage", "invalid plugin, command or arguments"), 400
		}
		if in.Args == nil {
			in.Args = []string{}
		}
		return s.call(r.Context(), "POST", "/v1/plugin", in), 200
	}))
	mux.HandleFunc("/api/cloudflare/setup", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Token string }
		if code := readBody(r, &in); code != 0 || len(in.Token) < 20 || len(in.Token) > 2000 {
			return errBody("usage", "invalid request"), 400
		}
		// the token travels as standard input of the plugin, never as an argument
		return s.call(r.Context(), "POST", "/v1/plugin", map[string]any{"plugin": "cloudflare", "command": "setup", "args": []string{"--token-stdin"}, "stdin": in.Token + "\n"}), 200
	}))
	mux.HandleFunc("/api/firewall", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.call(r.Context(), "GET", "/v1/firewall", nil), 200
	}))
	mux.HandleFunc("/api/firewall/allow", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		in, code := fwRule(r)
		if code != 0 {
			return errBody("usage", "invalid rule: port 1-65535, protocol tcp or udp, source an address or a network"), code
		}
		return s.call(r.Context(), "POST", "/v1/firewall/allow", in), 200
	}))
	mux.HandleFunc("/api/firewall/remove", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		in, code := fwRule(r)
		if code != 0 {
			return errBody("usage", "invalid rule: port 1-65535, protocol tcp or udp, source an address or a network"), code
		}
		if in.Action != "allow" && in.Action != "limit" && in.Action != "deny" {
			return errBody("usage", "invalid rule action"), 400
		}
		return s.call(r.Context(), "POST", "/v1/firewall/remove", in), 200
	}))
	mux.HandleFunc("/api/firewall/enable", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{}
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		return s.call(r.Context(), "POST", "/v1/firewall/enable", struct{}{}), 200
	}))
	s.projectRoutes(mux)
	return s.guard(mux)
}

type fwIn struct {
	Port   int    `json:"port"`
	Proto  string `json:"proto"`
	From   string `json:"from"`
	Action string `json:"action"`
}

// fwRule reads and validates a firewall rule from the request body.
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

// api wraps an API handler: method, token.
func (s *Server) api(method string, h func(*http.Request) ([]byte, int)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(s.Token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write(errBody("permission_denied", "a token from the address printed by lite-ui is required"))
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, r.Header.Get("X-Server")))
		b, code := h(r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_, _ = w.Write(b)
	}
}

// guard protects against requests from foreign sites: only a Host like localhost or 127.0.0.1 is accepted
// (protection against DNS rebinding), and for browser requests Origin must match Host.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			h = r.Host
		}
		if h != "127.0.0.1" && h != "localhost" && h != "[::1]" && h != "::1" {
			http.Error(w, "invalid Host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "foreign Origin", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-eval'; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Serve listens on addr (loopback only) until ctx is canceled.
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
