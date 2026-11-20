package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/owner"
)

type ctxKey struct{}

// WithUID puts the caller uid into the request context (in production ConnContext sets it from SO_PEERCRED).
func WithUID(ctx context.Context, uid int) context.Context {
	return context.WithValue(ctx, ctxKey{}, uid)
}

func uidOf(ctx context.Context) int {
	if v, ok := ctx.Value(ctxKey{}).(int); ok {
		return v
	}
	return -1
}

// Agent is the HTTP agent. It does not change the system itself: every action goes through the executor.
type Agent struct {
	// ExecDial connects to the executor (the systemd socket /run/lite-deploy/exec.sock).
	ExecDial func(ctx context.Context) (net.Conn, error)
	// Allowed returns the name and the permission for the caller uid.
	Allowed func(uid int) (string, bool)
	// Audit is the agent request journal (one JSON line each), may be nil.
	Audit   io.Writer
	Now     func() time.Time
	Version string
	// Timeout of the exchange with the executor; a deploy can take minutes.
	Timeout time.Duration
	// ApplyPerHour limits the actions that change the system (0: no limit). It stops a runaway script, or someone who got
	// hold of an administrator account, from changing the server hundreds of times.
	ApplyPerHour int
	// FS and KeysPath: where the registered owner keys are (public, readable). With at least one key registered, the
	// actions that change the server must be signed by it (package owner).
	FS       host.FS
	KeysPath string

	qmu      sync.Mutex
	changes  []time.Time
	verifier owner.Verifier
	omu      sync.Mutex
	serverID string
	confirm  map[string]bool
	confirmT time.Time
}

// ownerKeys returns the registered owner keys.
func (a *Agent) ownerKeys() []owner.Key {
	if a.FS == nil {
		return nil
	}
	p := a.KeysPath
	if p == "" {
		p = owner.KeysFile
	}
	b, err := a.FS.ReadFile(p)
	if err != nil {
		return nil
	}
	return owner.ParseKeys(string(b))
}

// pluginConfirm tells which plugin commands change the system (they need confirmation), from what the executor reports.
func (a *Agent) pluginConfirm(ctx context.Context) map[string]bool {
	a.omu.Lock()
	defer a.omu.Unlock()
	if a.confirm != nil && a.now().Sub(a.confirmT) < 10*time.Minute {
		return a.confirm
	}
	var env struct {
		Data struct {
			Plugins []struct {
				Name     string `json:"name"`
				Commands []struct {
					Name    string `json:"name"`
					Confirm bool   `json:"confirm"`
				} `json:"commands"`
			} `json:"plugins"`
		} `json:"data"`
	}
	if json.Unmarshal(a.exec(ctx, Request{Cmd: "info"}), &env) != nil || len(env.Data.Plugins) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, p := range env.Data.Plugins {
		for _, c := range p.Commands {
			m[p.Name+"/"+c.Name] = c.Confirm
		}
	}
	a.confirm, a.confirmT = m, a.now()
	return m
}

// ServerID is the public key of the executor: it ties a signature to this server.
func (a *Agent) ServerID(ctx context.Context) string {
	a.omu.Lock()
	defer a.omu.Unlock()
	if a.serverID != "" {
		return a.serverID
	}
	var env struct {
		Data struct {
			PublicKey string `json:"public_key"`
		} `json:"data"`
	}
	if json.Unmarshal(a.exec(ctx, Request{Cmd: "project.key"}), &env) == nil {
		a.serverID = env.Data.PublicKey
	}
	return a.serverID
}

// dangerous tells whether the request changes the server and so must be signed once an owner key is registered.
// Deploys and rollbacks stay open: CI does them with a restricted key, and they only move between versions.
func (a *Agent) dangerous(ctx context.Context, req Request) bool {
	switch {
	case readOnly[req.Cmd], req.Cmd == "deploy", req.Cmd == "rollback":
		return false
	case req.Cmd == "plugin.run" && len(req.Args) >= 2:
		if m := a.pluginConfirm(ctx); m != nil {
			if confirm, known := m[req.Args[0]+"/"+req.Args[1]]; known {
				return confirm
			}
		}
	}
	return true
}

// guardOwner returns an error text if the request needs the owner's signature and does not have a valid one.
func (a *Agent) guardOwner(ctx context.Context, r *http.Request, req Request, body []byte) string {
	keys := a.ownerKeys()
	if len(keys) == 0 || !a.dangerous(ctx, req) {
		return ""
	}
	sid := a.ServerID(ctx)
	if sid == "" {
		return "the server could not be identified to check your confirmation"
	}
	if err := a.verifier.Verify(keys, sid, r.Method, r.URL.Path, body, r.Header.Get("X-Lite-Sig"), a.now()); err != nil {
		return err.Error()
	}
	return ""
}

// readOnly are the commands that change nothing; they do not count against the quota.
var readOnly = map[string]bool{"project.export": true, "secret.list": true, "status": true, "info": true, "verify": true, "ingress": true, "project.key": true,
	"project.preview": true, "firewall.status": true, "update.check": true}

// overQuota records a changing action and reports whether the hourly quota is used up.
func (a *Agent) overQuota(cmd string) bool {
	if a.ApplyPerHour <= 0 || readOnly[cmd] {
		return false
	}
	a.qmu.Lock()
	defer a.qmu.Unlock()
	now := a.now()
	keep := a.changes[:0]
	for _, t := range a.changes {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	a.changes = keep
	if len(a.changes) >= a.ApplyPerHour {
		return true
	}
	a.changes = append(a.changes, now)
	return false
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

// exec sends a request to the executor and returns its answer (a JSON envelope).
func (a *Agent) exec(ctx context.Context, req Request) []byte {
	if a.overQuota(req.Cmd) {
		return ErrorJSON(req.Cmd, "quota_exceeded", fmt.Sprintf("too many changes to the server in the last hour (the limit is %d): wait a while, or raise quotas.apply_per_hour in /etc/lite-deploy/config.json", a.ApplyPerHour))
	}
	to := a.Timeout
	if to == 0 {
		to = 25 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	conn, err := a.ExecDial(ctx)
	if err != nil {
		return ErrorJSON(req.Cmd, "precondition_failed", "the executor is unavailable: "+err.Error())
	}
	defer func() { _ = conn.Close() }()
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
		return ErrorJSON(req.Cmd, "io_error", "the executor returned no answer")
	}
	return out
}

// readBundle reads the whole request body (a JSON bundle) with the MaxBody limit.
func readBundle(r *http.Request) (string, bool) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil || len(b) == 0 || len(b) > MaxBody || !json.Valid(b) || b[0] != '{' {
		return "", false
	}
	return string(b), true
}

func readJSON(r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return false
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096+2*MaxPluginStdin))
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

// Handler is the HTTP handler of the /v1 API.
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
				status, body = 403, ErrorJSON("api", "permission_denied", "no access: the user must be a system administrator (group sudo or wheel)")
			case r.Method != method:
				status, body = 405, ErrorJSON("api", "usage", "method not allowed")
			default:
				var in []byte
				if r.Method == http.MethodPost && r.Body != nil {
					in, _ = io.ReadAll(io.LimitReader(r.Body, 2*MaxBody+MaxRequest))
					r.Body = io.NopCloser(bytes.NewReader(in))
				}
				req, valid := build(r)
				if !valid {
					status, body = 400, ErrorJSON("api", "usage", "invalid request")
				} else if why := a.guardOwner(r.Context(), r, req, in); why != "" {
					status, body = 403, ErrorJSON(req.Cmd, "owner_confirmation_required", why)
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
	route(http.MethodGet, "/v1/status", func(r *http.Request) (Request, bool) {
		if p := r.URL.Query().Get("project"); p != "" {
			if !projectRe.MatchString(p) {
				return Request{}, false
			}
			return Request{Cmd: "status", Args: []string{p}}, true
		}
		return Request{Cmd: "status"}, true
	})
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
	route(http.MethodGet, "/v1/project/key", func(*http.Request) (Request, bool) { return Request{Cmd: "project.key"}, true })
	for _, cmd := range []string{"preview", "create"} {
		cmd := cmd
		route(http.MethodPost, "/v1/project/"+cmd, func(r *http.Request) (Request, bool) {
			body, ok := readBundle(r)
			if !ok {
				return Request{}, false
			}
			return Request{Cmd: "project." + cmd, Body: body}, true
		})
	}
	route(http.MethodGet, "/v1/plugins", func(*http.Request) (Request, bool) { return Request{Cmd: "info"}, true })
	route(http.MethodPost, "/v1/fix", func(r *http.Request) (Request, bool) {
		var in struct{}
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "fix"}, true
	})
	route(http.MethodGet, "/v1/update", func(*http.Request) (Request, bool) { return Request{Cmd: "update.check"}, true })
	route(http.MethodPost, "/v1/update/apply", func(r *http.Request) (Request, bool) {
		var in struct{}
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "update.apply"}, true
	})
	route(http.MethodGet, "/v1/ingress", func(*http.Request) (Request, bool) { return Request{Cmd: "ingress"}, true })
	route(http.MethodPost, "/v1/publish", func(r *http.Request) (Request, bool) {
		var in struct{ Project, Site, Mode string }
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "publish", Args: []string{in.Project, in.Site, in.Mode}}, true
	})
	route(http.MethodPost, "/v1/owner/add", func(r *http.Request) (Request, bool) {
		var in struct{ Pub, Label string }
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "owner.add", Args: []string{in.Pub, in.Label}}, true
	})
	route(http.MethodPost, "/v1/owner/remove", func(r *http.Request) (Request, bool) {
		var in struct{ ID string }
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "owner.remove", Args: []string{in.ID}}, true
	})
	route(http.MethodGet, "/v1/owner", func(*http.Request) (Request, bool) { return Request{Cmd: "owner.list"}, true })
	route(http.MethodGet, "/v1/project/export", func(r *http.Request) (Request, bool) {
		p := r.URL.Query().Get("project")
		if !projectRe.MatchString(p) {
			return Request{}, false
		}
		return Request{Cmd: "project.export", Args: []string{p}}, true
	})
	route(http.MethodGet, "/v1/secrets", func(r *http.Request) (Request, bool) {
		p := r.URL.Query().Get("project")
		if !projectRe.MatchString(p) {
			return Request{}, false
		}
		return Request{Cmd: "secret.list", Args: []string{p}}, true
	})
	route(http.MethodPost, "/v1/secret", func(r *http.Request) (Request, bool) {
		var in struct{ Project, Name, Ciphertext string }
		if !readJSON(r, &in) || len(in.Ciphertext) == 0 || len(in.Ciphertext) > MaxPluginStdin {
			return Request{}, false
		}
		return Request{Cmd: "secret.put", Args: []string{in.Project, in.Name}, Body: in.Ciphertext}, true
	})
	route(http.MethodPost, "/v1/secret/remove", func(r *http.Request) (Request, bool) {
		var in struct{ Project, Name string }
		if !readJSON(r, &in) {
			return Request{}, false
		}
		return Request{Cmd: "secret.remove", Args: []string{in.Project, in.Name}}, true
	})
	route(http.MethodGet, "/v1/verify", func(*http.Request) (Request, bool) { return Request{Cmd: "verify"}, true })
	route(http.MethodPost, "/v1/plugin", func(r *http.Request) (Request, bool) {
		var in struct {
			Plugin  string   `json:"plugin"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Stdin   string   `json:"stdin,omitempty"`
		}
		if !readJSON(r, &in) || len(in.Stdin) > MaxPluginStdin {
			return Request{}, false
		}
		return Request{Cmd: "plugin.run", Args: append([]string{in.Plugin, in.Command}, in.Args...), Body: in.Stdin}, true
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
			_, _ = w.Write(ErrorJSON("api", "permission_denied", "no access"))
			return
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "api_version": "v1", "command": "info", "data": map[string]string{"version": a.Version}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append(b, '\n'))
	})
	return mux
}

// Serve handles connections until ctx is canceled. The caller uid is determined by the kernel (SO_PEERCRED).
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

// GroupAllowed allows users who belong to the group (primary or supplementary), per /etc/passwd and /etc/group.
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

// Call sends a request to the agent over a unix socket and returns the response body.
func Call(ctx context.Context, sock, method, path string, body []byte) ([]byte, error) {
	return CallSigned(ctx, sock, method, path, body, "")
}

// CallSigned is Call with the owner's signature (the value of the X-Lite-Sig header; empty: none).
func CallSigned(ctx context.Context, sock, method, path string, body []byte, sig string) ([]byte, error) {
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
	if sig != "" {
		req.Header.Set("X-Lite-Sig", sig)
	}
	resp, err := (&http.Client{Transport: tr, Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("the agent is unavailable (%s): %w", sock, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}
