package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	return []mcp.Tool{
		{Name: "list_servers", Description: "The servers you can manage (user@address). Start here.", Schema: obj(map[string]any{}),
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
		{Name: "verify", Description: "Check a server (read only). Returns findings with a level: ok, warn or fail, and the counts.", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return get(ctx, a, "GET", "/v1/verify", nil)
			}},
		{Name: "project_status", Description: "The projects on a server: which slot is active, whether each service answers, the version, the history of deploys.", Schema: obj(map[string]any{"server": serverArg, "project": str("One project; empty for all.")}),
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
		{Name: "ingress", Description: "How visitors reach the server: Caddy, the web ports, and every domain with the way it is published (tunnel or direct).", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return get(ctx, a, "GET", "/v1/ingress", nil)
			}},
		{Name: "plugins", Description: "The plugins of the server, whether each is installed, and their commands.", Schema: obj(map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, a map[string]any) (string, error) {
				return get(ctx, a, "GET", "/v1/plugins", nil)
			}},
		{Name: "update_check", Description: "Is a newer release of lite available for the server (it does not install it).", Schema: obj(map[string]any{"server": serverArg}),
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
	}
}

// Request sends one request to the agent of a server on behalf of an assistant: it is never signed with the owner's key.
func (s *Server) Request(ctx context.Context, target, method, path string, body any) []byte {
	ctx = context.WithValue(ctx, ctxKey{}, target)
	ctx = context.WithValue(ctx, noSignKey{}, true)
	return s.call(ctx, method, path, body)
}
