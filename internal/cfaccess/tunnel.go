package cfaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Tunnel is a Cloudflare Tunnel of an account.
type Tunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Zone is a domain of the account.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ZoneFor finds the zone (the domain in the account) that holds a host: it tries the host itself, then the names with the
// leftmost label removed, down to the last two labels.
func (c *Client) ZoneFor(ctx context.Context, host string) (Zone, error) {
	if !hostRe.MatchString(host) {
		return Zone{}, errors.New("invalid domain")
	}
	labels := strings.Split(host, ".")
	for i := 0; i+1 < len(labels); i++ {
		name := strings.Join(labels[i:], ".")
		raw, err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(name), nil)
		if err != nil {
			return Zone{}, err
		}
		var zs []Zone
		if json.Unmarshal(raw, &zs) == nil && len(zs) > 0 {
			return zs[0], nil
		}
	}
	return Zone{}, errors.New("the domain " + host + " is not in this Cloudflare account (or the token cannot see it)")
}

// Tunnels lists the tunnels of an account that are not deleted.
func (c *Client) Tunnels(ctx context.Context, account string) ([]Tunnel, error) {
	if !idRe.MatchString(account) {
		return nil, errors.New("invalid account id")
	}
	raw, err := c.do(ctx, http.MethodGet, "/accounts/"+account+"/cfd_tunnel?is_deleted=false&per_page=100", nil)
	if err != nil {
		return nil, err
	}
	var out []Tunnel
	return out, json.Unmarshal(raw, &out)
}

// CreateTunnel makes a tunnel whose settings (the routes) are kept at Cloudflare, so that it needs only its token to run.
func (c *Client) CreateTunnel(ctx context.Context, account, name string) (Tunnel, error) {
	if !idRe.MatchString(account) {
		return Tunnel{}, errors.New("invalid account id")
	}
	raw, err := c.do(ctx, http.MethodPost, "/accounts/"+account+"/cfd_tunnel", map[string]any{"name": name, "config_src": "cloudflare"})
	if err != nil {
		return Tunnel{}, err
	}
	var t Tunnel
	return t, json.Unmarshal(raw, &t)
}

// TunnelToken is the token the connector program on the server runs with.
func (c *Client) TunnelToken(ctx context.Context, account, id string) (string, error) {
	if !idRe.MatchString(account) || !idRe.MatchString(id) {
		return "", errors.New("invalid id")
	}
	raw, err := c.do(ctx, http.MethodGet, "/accounts/"+account+"/cfd_tunnel/"+id+"/token", nil)
	if err != nil {
		return "", err
	}
	var tok string
	if json.Unmarshal(raw, &tok) != nil || tok == "" {
		return "", errors.New("Cloudflare did not give a token") //nolint:staticcheck // a proper name
	}
	return tok, nil
}

type ingressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
	Path     string `json:"path,omitempty"`
}

// AddRoute makes the tunnel carry host to service (for example http://127.0.0.1:18080): the rules already there stay, a rule
// for the same host is replaced, and the closing "nothing else" rule is kept last.
func (c *Client) AddRoute(ctx context.Context, account, id, host, service string) error {
	if !idRe.MatchString(account) || !idRe.MatchString(id) || !hostRe.MatchString(host) || !strings.HasPrefix(service, "http://127.0.0.1:") {
		return errors.New("invalid route")
	}
	path := "/accounts/" + account + "/cfd_tunnel/" + id + "/configurations"
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	var cur struct {
		Config struct {
			Ingress []ingressRule `json:"ingress"`
		} `json:"config"`
	}
	_ = json.Unmarshal(raw, &cur)
	var rules []ingressRule
	for _, r := range cur.Config.Ingress {
		if r.Hostname == "" || r.Hostname == host {
			continue // the closing rule is added again below; an old rule of this host is replaced
		}
		rules = append(rules, r)
	}
	rules = append(rules, ingressRule{Hostname: host, Service: service}, ingressRule{Service: "http_status:404"})
	_, err = c.do(ctx, http.MethodPut, path, map[string]any{"config": map[string]any{"ingress": rules}})
	return err
}

// SetDNS points a host at the tunnel with a CNAME record (made again if it exists).
func (c *Client) SetDNS(ctx context.Context, zone, host, tunnelID string) error {
	if !idRe.MatchString(zone) || !hostRe.MatchString(host) || !idRe.MatchString(tunnelID) {
		return errors.New("invalid DNS record")
	}
	content := tunnelID + ".cfargotunnel.com"
	rec := map[string]any{"type": "CNAME", "name": host, "content": content, "proxied": true, "ttl": 1}
	raw, err := c.do(ctx, http.MethodGet, "/zones/"+zone+"/dns_records?type=CNAME&name="+url.QueryEscape(host), nil)
	if err != nil {
		return err
	}
	var have []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &have) == nil && len(have) > 0 {
		_, err = c.do(ctx, http.MethodPut, "/zones/"+zone+"/dns_records/"+have[0].ID, rec)
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", rec)
	return err
}
