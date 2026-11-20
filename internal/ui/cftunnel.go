package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/toxic-sunflower/lite-deploy/internal/cfaccess"
)

// cfTunnelRequest asks to carry a domain to a site of this server through a Cloudflare Tunnel, doing everything: the
// tunnel (made once per Cloudflare account), the route, the DNS record, the connector on the server and the site's settings.
type cfTunnelRequest struct {
	cfAccessRequest
	Port    int    `json:"port"`
	Project string `json:"project"`
	Site    string `json:"site"`
}

// envOK reports whether an answer of the agent is a success, and its message if not.
func envOK(raw []byte) (bool, string, json.RawMessage) {
	var env struct {
		OK    bool                      `json:"ok"`
		Data  json.RawMessage           `json:"data"`
		Error *struct{ Message string } `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return false, "the answer of the server is not JSON", nil
	}
	if !env.OK {
		if env.Error != nil {
			return false, env.Error.Message, nil
		}
		return false, "the request failed", nil
	}
	return true, "", env.Data
}

func (s *Server) cfTunnel(ctx context.Context, in cfTunnelRequest) ([]byte, int) {
	if in.Label == "" {
		in.Label = "default"
	}
	if !cfaccess.ValidLabel(in.Label) || len(in.Token) > 200 || in.Port < 18000 || in.Port > 18999 ||
		(in.Project != "" && !projectRe.MatchString(in.Project)) || (in.Site != "" && !siteRe.MatchString(in.Site)) {
		return errBody("usage", "invalid request"), 400
	}
	host, err := cfaccess.Address(in.Host, "")
	if err != nil {
		return errBody("usage", err.Error()), 400
	}
	c, account, remember, early := s.cfSession(ctx, in.cfAccessRequest)
	if early != nil {
		return early, 200
	}
	var steps []string
	step := func(s string) { steps = append(steps, s) }
	fail := func(what, msg string) ([]byte, int) {
		return errBody("cloudflare", what+": "+msg+" (done before this: "+join(steps)+")"), 502
	}

	// 1. the tunnel of this Cloudflare account (one per account, reused for every domain)
	remote := "lite-" + in.Label
	tunnels, err := c.Tunnels(ctx, account)
	if err != nil {
		return fail("listing the tunnels", err.Error())
	}
	var tunnel cfaccess.Tunnel
	for _, t := range tunnels {
		if t.Name == remote {
			tunnel = t
		}
	}
	created := false
	if tunnel.ID == "" {
		if tunnel, err = c.CreateTunnel(ctx, account, remote); err != nil {
			return fail("creating the tunnel", err.Error())
		}
		created = true
		step("tunnel " + remote + " created")
	} else {
		step("tunnel " + remote + " reused")
	}
	// 2. the route and the DNS record
	zone, err := c.ZoneFor(ctx, host)
	if err != nil {
		return fail("finding the domain", err.Error())
	}
	if err := c.AddRoute(ctx, account, tunnel.ID, host, "http://127.0.0.1:"+strconv.Itoa(in.Port)); err != nil {
		return fail("adding the route", err.Error())
	}
	step(host + " routed to port " + strconv.Itoa(in.Port))
	if err := c.SetDNS(ctx, zone.ID, host, tunnel.ID); err != nil {
		return fail("making the DNS record", err.Error())
	}
	step("DNS record of " + host)
	remember()

	// 3. the connector on the server, unless this tunnel already runs there
	name := in.Label
	statusArgs := []string{}
	if name != "default" {
		statusArgs = []string{"--name", name}
	}
	connected := false
	if ok, _, data := envOK(s.call(ctx, "POST", "/v1/plugin", map[string]any{"plugin": "cloudflare", "command": "status", "args": statusArgs})); ok {
		var st struct {
			Tunnels []struct{ Name string } `json:"tunnels"`
		}
		_ = json.Unmarshal(data, &st)
		for _, t := range st.Tunnels {
			if t.Name == name {
				connected = true
			}
		}
	}
	if !connected || created {
		token, err := c.TunnelToken(ctx, account, tunnel.ID)
		if err != nil {
			return fail("getting the tunnel token", err.Error())
		}
		if ok, msg, _ := envOK(s.call(ctx, "POST", "/v1/plugin", map[string]any{"plugin": "cloudflare", "command": "install", "args": []string{}})); !ok {
			return fail("installing cloudflared on the server", msg)
		}
		args := []string{"--token-stdin"}
		if name != "default" {
			args = append(args, "--name", name)
		}
		// the token of the tunnel goes to the server as standard input, never as an argument
		if ok, msg, _ := envOK(s.call(ctx, "POST", "/v1/plugin", map[string]any{"plugin": "cloudflare", "command": "setup", "args": args, "stdin": token + "\n"})); !ok {
			return fail("connecting the tunnel on the server", msg)
		}
		step("tunnel connected on the server")
	}
	// 4. the site of the project goes through this tunnel
	if in.Project != "" && in.Site != "" {
		body := map[string]string{"project": in.Project, "site": in.Site, "mode": "tunnel", "tunnel": ""}
		if name != "default" {
			body["tunnel"] = name
		}
		if ok, msg, _ := envOK(s.call(ctx, "POST", "/v1/publish", body)); !ok {
			return fail("setting up the site", msg)
		}
		step("site " + in.Project + "/" + in.Site + " goes through the tunnel")
	}
	return okBody(map[string]any{"steps": steps, "tunnel": remote, "zone": zone.Name, "host": host}), 200
}

func join(l []string) string {
	if len(l) == 0 {
		return "nothing"
	}
	out := l[0]
	for _, x := range l[1:] {
		out += ", " + x
	}
	return out
}

func (s *Server) registerCFTunnel(mux *http.ServeMux) {
	mux.HandleFunc("/api/cf-tunnel", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in cfTunnelRequest
		dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8192))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return errBody("usage", "invalid request"), 400
		}
		return s.cfTunnel(r.Context(), in)
	}))
}

// CloudflareTunnel is the same action as the page's, for a terminal: it returns the answer envelope. The port of the site
// is looked up on the server when it is not given.
func (s *Server) CloudflareTunnel(ctx context.Context, target, label, token string, remember bool, account, host string, port int, project, site string) []byte {
	ctx = context.WithValue(ctx, ctxKey{}, target)
	if port == 0 && project != "" && site != "" {
		if ok, _, data := envOK(s.call(ctx, "GET", "/v1/ingress", nil)); ok {
			var ing struct {
				Sites []struct {
					Project, Site string
					Port          int
				} `json:"sites"`
			}
			if json.Unmarshal(data, &ing) == nil {
				for _, x := range ing.Sites {
					if x.Project == project && x.Site == site {
						port = x.Port
					}
				}
			}
		}
		if port == 0 {
			return errBody("usage", "the site "+project+"/"+site+" has no port: is the project created on that server?")
		}
	}
	raw, _ := s.cfTunnel(ctx, cfTunnelRequest{
		cfAccessRequest: cfAccessRequest{Label: label, Token: token, Remember: remember, Account: account, Host: host},
		Port:            port, Project: project, Site: site,
	})
	return raw
}
