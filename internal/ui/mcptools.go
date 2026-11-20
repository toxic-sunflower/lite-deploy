package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/mcp"
)

// MCPTools are the tools of the MCP mode of lite-ui. They read the state and do the things that are safe to leave to an
// assistant (deploy, roll back). They never sign a request with the owner's key, so with that protection on, dangerous
// changes cannot be made through them.
func (s *Server) MCPTools() []mcp.Tool {
	serverArg := map[string]any{"type": "string", "description": "The server as user@address, one of list_servers. Optional when you have only one."}
	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	target := func(args map[string]any) (string, error) {
		if v, _ := args["server"].(string); v != "" {
			if s.Servers == nil || !s.Servers.Has(v) {
				return "", fmt.Errorf("the server %q is not in the list (see list_servers)", v)
			}
			return v, nil
		}
		if s.Servers != nil {
			if l := s.Servers.List(); len(l) > 0 {
				return l[0], nil
			}
		}
		if s.Host != "" {
			return s.Host, nil
		}
		return "", errors.New("no server is set up: add one in lite-ui first")
	}
	get := func(ctx context.Context, args map[string]any, method, path string, body any) (string, error) {
		t, err := target(args)
		if err != nil {
			return "", err
		}
		raw := s.Request(ctx, t, method, path, body)
		var env struct {
			OK    bool            `json:"ok"`
			Data  json.RawMessage `json:"data"`
			Error *struct {
				Code, Message, Hint string
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) != nil {
			return "", errors.New("the server returned something that is not JSON")
		}
		if !env.OK {
			if env.Error == nil {
				return "", errors.New("the request failed")
			}
			msg := env.Error.Message
			if env.Error.Hint != "" {
				msg += "\n" + env.Error.Hint
			}
			return "", errors.New(msg)
		}
		var pretty strings.Builder
		var v any
		if json.Unmarshal(env.Data, &v) == nil {
			b, _ := json.MarshalIndent(v, "", "  ")
			pretty.Write(b)
		}
		return pretty.String(), nil
	}
	// api runs one request of the page in this same process, with the same checks as in the page, and shows the answer.
	api := func(ctx context.Context, args map[string]any, method, path string, body any) (string, error) {
		t, err := target(args)
		if err != nil {
			return "", err
		}
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		ctx = context.WithValue(ctx, noSignKey{}, true) // an assistant never signs with the owner's key
		req, err := http.NewRequestWithContext(ctx, method, "http://127.0.0.1"+path, rd)
		if err != nil {
			return "", err
		}
		req.Header.Set("X-Token", s.Token)
		req.Header.Set("X-Server", t)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		w := &memWriter{h: http.Header{}}
		s.Handler().ServeHTTP(w, req)
		return renderEnvelope(w.buf.Bytes())
	}
	post := func(path string) func(context.Context, map[string]any) (string, error) {
		return func(ctx context.Context, a map[string]any) (string, error) {
			body := map[string]any{}
			for k, v := range a {
				if k != "server" {
					body[k] = v
				}
			}
			return api(ctx, a, "POST", path, body)
		}
	}
	list := func(name string) func(context.Context, map[string]any) (string, error) {
		return func(ctx context.Context, a map[string]any) (string, error) {
			p, _ := a["project"].(string)
			if !projectRe.MatchString(p) {
				return "", errors.New("a valid project name is required")
			}
			return api(ctx, a, "GET", "/api/"+name+"?project="+p, nil)
		}
	}
	arr := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	extra := []mcp.Tool{
		{Name: "firewall_status", ReadOnly: true, Description: "The firewall of the server: is it on, and which ports are open.", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return api(ctx, a, "GET", "/api/firewall", nil)
			}},
		{Name: "secrets_list", ReadOnly: true, Description: "The names of the secrets of a project and whether each is set (values are never shown).", Schema: obj(map[string]any{"server": serverArg, "project": str("The project name.")}, "project"),
			Run: list("secrets")},
		{Name: "project_export", ReadOnly: true, Description: "The description of a project as it is kept on the server, without secret values.", Schema: obj(map[string]any{"server": serverArg, "project": str("The project name.")}, "project"),
			Run: list("project/export")},
		{Name: "server_config", ReadOnly: true, Description: "The configuration of the server as a file (users and keys, firewall, programs): it can set up another server the same way.", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return api(ctx, a, "GET", "/api/server/config", nil)
			}},
		{Name: "update_server", Description: "Install the newest release of lite on the server (the agent restarts for about a minute). Ask the person first.", Schema: obj(map[string]any{"server": serverArg}),
			Run: post("/api/update/apply")},
		{Name: "fix", Description: "Fix what the server check can fix by itself (safe things only). Ask the person first.", Schema: obj(map[string]any{"server": serverArg}),
			Run: post("/api/fix")},
		{Name: "run_plugin", Description: "Run a command of a plugin of the server (see plugins for the list): for example firewall allow/remove/mode, security enable/allow-users/unban/web-auth, backup run/schedule/status, pg commands, caddy install, cloudflare remove. Changes the server: ask the person first.",
			Schema: obj(map[string]any{"server": serverArg, "plugin": str("The plugin name."), "command": str("The command."), "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Arguments of the command."}}, "plugin", "command"),
			Run:    post("/api/plugin")},
		{Name: "publish", Description: "Choose how a site reaches the internet: tunnel (through a Cloudflare Tunnel; tunnel names the account's tunnel) or direct (Caddy gets its own certificate). Ask the person first.",
			Schema: obj(map[string]any{"server": serverArg, "project": str("The project."), "site": str("The site name."), "mode": map[string]any{"type": "string", "enum": []string{"tunnel", "direct"}}, "tunnel": str("Optional name of the Cloudflare tunnel, with mode tunnel.")}, "project", "site", "mode"),
			Run:    post("/api/publish")},
		{Name: "fallback_password", Description: "Set or remove the password asked for when a site is served directly (action set needs user and a password of 12+ characters and optional paths such as /admin*; remove-user; off). The password passes through this chat: prefer typing it in the page.",
			Schema: obj(map[string]any{"server": serverArg, "project": str("The project."), "site": str("The site."), "action": map[string]any{"type": "string", "enum": []string{"set", "remove-user", "off"}}, "user": str("The login."), "password": str("The password (set only)."), "paths": arr}, "project", "site", "action"),
			Run:    post("/api/fallback-auth")},
		{Name: "project_preview", ReadOnly: true, Description: "Show what creating or changing a project would do, without changing anything. project is the project description (see project_export for an example); secrets maps secret names to values (they are encrypted on this workstation).",
			Schema: obj(map[string]any{"server": serverArg, "project": map[string]any{"type": "object", "description": "The project description."}, "secrets": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}, "project"),
			Run:    post("/api/project/preview")},
		{Name: "project_create", Description: "Create or change a project from its description (run project_preview first and show the person the steps). Secret values go encrypted to the server. Ask the person first.",
			Schema: obj(map[string]any{"server": serverArg, "project": map[string]any{"type": "object", "description": "The project description."}, "secrets": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "expect_project_hash": str("project_hash from the preview.")}, "project"),
			Run:    post("/api/project/create")},
		{Name: "secret_set", Description: "Set a secret of a project (project shared: a secret shared by projects). The value passes through this chat: prefer typing it in the page. Ask the person first.",
			Schema: obj(map[string]any{"server": serverArg, "project": str("The project, or shared."), "name": str("The secret name (lowercase letters, digits, _)."), "value": str("The value.")}, "project", "name", "value"),
			Run:    post("/api/secret/set")},
		{Name: "secret_remove", Description: "Delete a secret of a project. Ask the person first.", Schema: obj(map[string]any{"server": serverArg, "project": str("The project."), "name": str("The secret name.")}, "project", "name"),
			Run: post("/api/secret/remove")},
		{Name: "connect_cloudflare_tunnel", Description: "Connect a Cloudflare Tunnel with its token (name: another tunnel for another Cloudflare account). The token passes through this chat: prefer typing it in the page.",
			Schema: obj(map[string]any{"server": serverArg, "token": str("The tunnel token from the Cloudflare dashboard."), "name": str("Optional tunnel name.")}, "token"),
			Run:    post("/api/cloudflare/setup")},
		{Name: "cloudflare_access", Description: "Close a domain (or a path) with Cloudflare Access, list what is closed, or open it again. action: list, protect (host, optional path, who: e-mails or @domain) or remove (app_id). label names the Cloudflare account; the API token is used on this workstation only and goes to Cloudflare only.",
			Schema: obj(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"labels", "list", "protect", "remove", "forget"}}, "label": str("The account name; default: default."), "token": str("The Cloudflare API token (not needed if saved)."), "remember": map[string]any{"type": "boolean"}, "account": str("Cloudflare account id, if the token sees several."), "host": str("The domain."), "path": str("Optional path, e.g. /admin*."), "who": arr, "app_id": str("The application to remove.")}, "action"),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				b, _ := json.Marshal(a)
				var in cfAccessRequest
				if err := json.Unmarshal(b, &in); err != nil {
					return "", errors.New("invalid arguments")
				}
				return renderEnvelope(func() []byte { raw, _ := s.cfAccess(ctx, in); return raw }())
			}},
		{Name: "add_server", Description: "Add a server (user@address) to the list of lite-ui on this workstation. A login with the SSH key must already work.", Schema: obj(map[string]any{"server": str("The server as user@address.")}, "server"),
			Run: func(_ context.Context, a map[string]any) (string, error) {
				v, _ := a["server"].(string)
				if s.Servers == nil {
					return "", errors.New("this workstation has no list of servers")
				}
				if err := s.Servers.Add(strings.TrimSpace(v)); err != nil {
					return "", err
				}
				return "Added " + v + ".", nil
			}},
		{Name: "remove_server", Description: "Remove a server from the list of lite-ui on this workstation (the server itself is not touched). Ask the person first.", Schema: obj(map[string]any{"server": str("The server as user@address.")}, "server"),
			Run: func(_ context.Context, a map[string]any) (string, error) {
				v, _ := a["server"].(string)
				if s.Servers == nil {
					return "", errors.New("this workstation has no list of servers")
				}
				if err := s.Servers.Remove(v); err != nil {
					return "", err
				}
				return "Removed " + v + " from the list.", nil
			}},
	}
	return append([]mcp.Tool{
		{Name: "list_servers", ReadOnly: true, Description: "The servers you can manage (user@address). Start here.", Schema: obj(map[string]any{}),
			Run: func(context.Context, map[string]any) (string, error) {
				var l []string
				if s.Servers != nil {
					l = s.Servers.List()
				} else if s.Host != "" {
					l = []string{s.Host}
				}
				b, _ := json.Marshal(l)
				return string(b), nil
			}},
		{Name: "verify", ReadOnly: true, Description: "Check a server (read only). Returns findings with a level: ok, warn or fail, and the counts.", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return get(ctx, a, "GET", "/v1/verify", nil)
			}},
		{Name: "project_status", ReadOnly: true, Description: "The projects on a server: which slot is active, whether each service answers, the version, the history of deploys.", Schema: obj(map[string]any{"server": serverArg, "project": str("One project; empty for all.")}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				p := "/v1/status"
				if v, _ := a["project"].(string); v != "" {
					if !projectRe.MatchString(v) {
						return "", errors.New("invalid project name")
					}
					p += "?project=" + v
				}
				return get(ctx, a, "GET", p, nil)
			}},
		{Name: "ingress", ReadOnly: true, Description: "How visitors reach the server: Caddy, the web ports, and every domain with the way it is published (tunnel or direct).", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return get(ctx, a, "GET", "/v1/ingress", nil)
			}},
		{Name: "plugins", ReadOnly: true, Description: "The plugins of the server, whether each is installed, and their commands.", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return get(ctx, a, "GET", "/v1/plugins", nil)
			}},
		{Name: "update_check", ReadOnly: true, Description: "Is a newer release of lite available for the server (it does not install it).", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return get(ctx, a, "GET", "/v1/update", nil)
			}},
		{Name: "deploy", Description: "Deploy a version of a project with no downtime. The version is the full 40-character commit hash whose image or build already exists. If the new version does not become healthy, traffic stays on the old one. Ask the person before deploying to production.",
			Schema: obj(map[string]any{"server": serverArg, "project": str("The project name."), "sha": str("The full 40-character commit hash.")}, "project", "sha"),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				p, _ := a["project"].(string)
				h, _ := a["sha"].(string)
				if !projectRe.MatchString(p) || !shaRe.MatchString(h) {
					return "", errors.New("a valid project name and the full 40-character commit hash (0-9, a-f) are required")
				}
				return get(ctx, a, "POST", "/v1/deploy", map[string]string{"project": p, "sha": h})
			}},
		{Name: "rollback", Description: "Switch a project back to its previous version. Ask the person before doing it.",
			Schema: obj(map[string]any{"server": serverArg, "project": str("The project name.")}, "project"),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				p, _ := a["project"].(string)
				if !projectRe.MatchString(p) {
					return "", errors.New("a valid project name is required")
				}
				return get(ctx, a, "POST", "/v1/rollback", map[string]string{"project": p})
			}},
	}, extra...)
}

// memWriter collects the answer of the page handler.
type memWriter struct {
	h    http.Header
	buf  bytes.Buffer
	code int
}

func (w *memWriter) Header() http.Header         { return w.h }
func (w *memWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *memWriter) WriteHeader(c int)           { w.code = c }

// renderEnvelope turns the answer envelope ({ok, data | error}) into the text the assistant sees.
func renderEnvelope(raw []byte) (string, error) {
	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code, Message, Hint string
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return "", errors.New("the answer is not JSON")
	}
	if !env.OK {
		if env.Error == nil {
			return "", errors.New("the request failed")
		}
		msg := env.Error.Message
		if env.Error.Hint != "" {
			msg += "\n" + env.Error.Hint
		}
		return "", errors.New(msg)
	}
	var v any
	if json.Unmarshal(env.Data, &v) != nil {
		return "Done.", nil
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b), nil
}

// Request sends one request to the agent of a server on behalf of an assistant: it is never signed with the owner's key.
func (s *Server) Request(ctx context.Context, target, method, path string, body any) []byte {
	ctx = context.WithValue(ctx, ctxKey{}, target)
	ctx = context.WithValue(ctx, noSignKey{}, true)
	return s.call(ctx, method, path, body)
}
