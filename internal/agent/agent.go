package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

type ctxKey struct{}

// WithUID кладёт uid вызывающего в контекст запроса (в работе его ставит ConnContext по SO_PEERCRED).
func WithUID(ctx context.Context, uid int) context.Context {
	return context.WithValue(ctx, ctxKey{}, uid)
}

func uidOf(ctx context.Context) int {
	if v, ok := ctx.Value(ctxKey{}).(int); ok {
		return v
	}
	return -1
}

// Agent HTTP-агент. Сам систему не меняет: все действия идут через исполнитель.
type Agent struct {
	// ExecDial соединение с исполнителем (сокет systemd /run/lite-deploy/exec.sock).
	ExecDial func(ctx context.Context) (net.Conn, error)
	// Allowed по uid вызывающего возвращает имя и разрешение.
	Allowed func(uid int) (string, bool)
	// Audit журнал запросов агента (по строке JSON), может быть nil.
	Audit   io.Writer
	Now     func() time.Time
	Version string
	// Timeout обмена с исполнителем; выкатка может идти минуты.
	Timeout time.Duration
}

func (a *Agent) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Agent) audit(name string, uid int, method, path string, status int, resp []byte) {
	if a.Audit == nil {
		return
	}
	var e struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	code := ""
	if json.Unmarshal(resp, &e) == nil && e.Error != nil {
		code = e.Error.Code
	}
	b, _ := json.Marshal(map[string]any{"time": a.now().UTC().Format(time.RFC3339), "uid": uid, "user": name,
		"method": method, "path": path, "http": status, "ok": e.OK, "code": code})
	_, _ = a.Audit.Write(append(b, '\n'))
}

// exec отправляет запрос исполнителю и возвращает его ответ (конверт JSON).
func (a *Agent) exec(ctx context.Context, req Request) []byte {
	to := a.Timeout
	if to == 0 {
		to = 25 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	conn, err := a.ExecDial(ctx)
	if err != nil {
		return ErrorJSON(req.Cmd, "precondition_failed", "исполнитель недоступен: "+err.Error())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(to))
	b, _ := json.Marshal(req)
	if _, err := conn.Write(b); err != nil {
		return ErrorJSON(req.Cmd, "io_error", err.Error())
	}
	out, err := io.ReadAll(io.LimitReader(conn, 4<<20))
	if err != nil {
		return ErrorJSON(req.Cmd, "io_error", err.Error())
	}
	out = []byte(strings.TrimSpace(string(out)))
	if len(out) == 0 || out[0] != '{' {
		return ErrorJSON(req.Cmd, "io_error", "исполнитель не вернул ответа")
	}
	return out
}

func readJSON(r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return false
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

// Handler HTTP-обработчик API /v1.
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	route := func(method, path string, build func(r *http.Request) (Request, bool)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			uid := uidOf(r.Context())
			name, ok := "", false
			if uid >= 0 && a.Allowed != nil {
				name, ok = a.Allowed(uid)
			}
			var status = 200
			var body []byte
			switch {
			case !ok:
				status, body = 403, ErrorJSON("api", "permission_denied", "нет доступа: пользователь должен быть в группе lite-deploy-admins")
			case r.Method != method:
				status, body = 405, ErrorJSON("api", "usage", "метод не разрешён")
			default:
				req, valid := build(r)
				if !valid {
					status, body = 400, ErrorJSON("api", "usage", "неверный запрос")
				} else {
					req.Caller = name
					body = a.exec(r.Context(), req)
				}
			}
			a.audit(name, uid, r.Method, r.URL.Path, status, body)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			_, _ = w.Write(append(body, '\n'))
		})
	}
	route(http.MethodGet, "/v1/status", func(*http.Request) (Request, bool) { return Request{Cmd: "status"}, true })
	route(http.MethodPost, "/v1/deploy", func(r *http.Request) (Request, bool) {
		var in struct{ Project, SHA string }
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "deploy", Args: []string{in.Project, in.SHA}}, true
	})
	route(http.MethodPost, "/v1/rollback", func(r *http.Request) (Request, bool) {
		var in struct{ Project string }
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "rollback", Args: []string{in.Project}}, true
	})
	route(http.MethodGet, "/v1/firewall", func(*http.Request) (Request, bool) { return Request{Cmd: "firewall.status"}, true })
	route(http.MethodPost, "/v1/firewall/enable", func(r *http.Request) (Request, bool) {
		var in struct{}
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "firewall.enable"}, true
	})
	type rule struct {
		Port   int    `json:"port"`
		Proto  string `json:"proto"`
		From   string `json:"from"`
		Action string `json:"action"`
	}
	route(http.MethodPost, "/v1/firewall/allow", func(r *http.Request) (Request, bool) {
		var in rule
		if !readJSON(r, &in) || firewall.ValidateRule(in.Port, in.Proto, in.From) != nil {
			return Request{}, false
		}
		return Request{Cmd: "firewall.allow", Args: []string{strconv.Itoa(in.Port) + "/" + in.Proto, in.From}}, true
	})
	route(http.MethodPost, "/v1/firewall/remove", func(r *http.Request) (Request, bool) {
		var in rule
		if !readJSON(r, &in) || firewall.ValidateRule(in.Port, in.Proto, in.From) != nil {
			return Request{}, false
		}
		return Request{Cmd: "firewall.remove", Args: []string{strconv.Itoa(in.Port) + "/" + in.Proto, in.From, in.Action}}, true
	})
	mux.HandleFunc("/v1/info", func(w http.ResponseWriter, r *http.Request) {
		uid := uidOf(r.Context())
		if _, ok := a.Allowed(uid); !ok || uid < 0 {
			w.WriteHeader(403)
			_, _ = w.Write(ErrorJSON("api", "permission_denied", "нет доступа"))
			return
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "api_version": "v1", "command": "info", "data": map[string]string{"version": a.Version}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append(b, '\n'))
	})
	return mux
}

// Serve обслуживает соединения, пока не отменён ctx. uid вызывающего определяется ядром (SO_PEERCRED).
func (a *Agent) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			uid, err := host.PeerUID(c)
			if err != nil {
				uid = -1
			}
			return WithUID(ctx, uid)
		}}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := srv.Serve(ln); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// GroupAllowed разрешает пользователей, входящих в группу (основная или дополнительная), по /etc/passwd и /etc/group.
func GroupAllowed(fs host.FS, group string) func(uid int) (string, bool) {
	return func(uid int) (string, bool) {
		pw, err := fs.ReadFile("/etc/passwd")
		if err != nil {
			return "", false
		}
		name, gid := "", -1
		for _, l := range strings.Split(string(pw), "\n") {
			f := strings.Split(l, ":")
			if len(f) >= 4 && f[2] == strconv.Itoa(uid) {
				name = f[0]
				gid, _ = strconv.Atoi(f[3])
				break
			}
		}
		if name == "" {
			return "", false
		}
		gr, err := fs.ReadFile("/etc/group")
		if err != nil {
			return name, false
		}
		for _, l := range strings.Split(string(gr), "\n") {
			f := strings.Split(l, ":")
			if len(f) < 4 || f[0] != group {
				continue
			}
			if f[2] == strconv.Itoa(gid) {
				return name, true
			}
			for _, m := range strings.Split(f[3], ",") {
				if m == name {
					return name, true
				}
			}
		}
		return name, false
	}
}

// Call выполняет запрос к агенту через unix-сокет и возвращает тело ответа.
func Call(ctx context.Context, sock, method, path string, body []byte) ([]byte, error) {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", sock)
	}}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, method, "http://agent"+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Transport: tr, Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("агент недоступен (%s): %w", sock, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
