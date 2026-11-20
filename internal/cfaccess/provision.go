package cfaccess

import (
	"context"
	"strconv"
)

// Provisioned is the result of making the Cloudflare side of a domain.
type Provisioned struct {
	Tunnel  Tunnel
	Zone    Zone
	Token   string // the token the connector on the server runs with
	Created bool   // the tunnel was made now (it did not exist yet)
	Steps   []string
}

// Provision makes everything at Cloudflare that a domain needs to reach a site on 127.0.0.1:port of the server: the tunnel of
// this account (named lite-LABEL, made once and reused for every domain), the route, the DNS record, and it fetches the tunnel
// token. On an error it returns what was done before it in Steps.
func (c *Client) Provision(ctx context.Context, account, label, host string, port int) (Provisioned, error) {
	var p Provisioned
	remote := "lite-" + label
	tunnels, err := c.Tunnels(ctx, account)
	if err != nil {
		return p, wrap("listing the tunnels", err)
	}
	for _, t := range tunnels {
		if t.Name == remote {
			p.Tunnel = t
		}
	}
	if p.Tunnel.ID == "" {
		if p.Tunnel, err = c.CreateTunnel(ctx, account, remote); err != nil {
			return p, wrap("creating the tunnel", err)
		}
		p.Created = true
		p.Steps = append(p.Steps, "tunnel "+remote+" created")
	} else {
		p.Steps = append(p.Steps, "tunnel "+remote+" reused")
	}
	if p.Zone, err = c.ZoneFor(ctx, host); err != nil {
		return p, wrap("finding the domain", err)
	}
	if err := c.AddRoute(ctx, account, p.Tunnel.ID, host, "http://127.0.0.1:"+strconv.Itoa(port)); err != nil {
		return p, wrap("adding the route", err)
	}
	p.Steps = append(p.Steps, host+" routed to port "+strconv.Itoa(port))
	if err := c.SetDNS(ctx, p.Zone.ID, host, p.Tunnel.ID); err != nil {
		return p, wrap("making the DNS record", err)
	}
	p.Steps = append(p.Steps, "DNS record of "+host)
	if p.Token, err = c.TunnelToken(ctx, account, p.Tunnel.ID); err != nil {
		return p, wrap("getting the tunnel token", err)
	}
	return p, nil
}

type stepError struct {
	what string
	err  error
}

func (e *stepError) Error() string { return e.what + ": " + e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }

func wrap(what string, err error) error { return &stepError{what, err} }
