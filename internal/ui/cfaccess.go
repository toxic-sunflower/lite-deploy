package ui

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/toxic-sunflower/lite-deploy/internal/cfaccess"
)

// cfAccessRequest is the one request of the "close with Cloudflare Access" form. The token comes from the form or from the
// ones the person chose to keep on this workstation; it is sent to Cloudflare only, never to a server.
type cfAccessRequest struct {
	Action   string   `json:"action"`
	Label    string   `json:"label"`
	Token    string   `json:"token"`
	Remember bool     `json:"remember"`
	Account  string   `json:"account"`
	Host     string   `json:"host"`
	Path     string   `json:"path"`
	Who      []string `json:"who"`
	AppID    string   `json:"app_id"`
}

func (s *Server) cfClient(token string) *cfaccess.Client {
	c := cfaccess.New(token)
	if s.CFBase != "" {
		c.Base = s.CFBase
	}
	return c
}

func (s *Server) cfAccess(ctx context.Context, in cfAccessRequest) ([]byte, int) {
	if in.Label == "" {
		in.Label = "default"
	}
	if !cfaccess.ValidLabel(in.Label) || len(in.Token) > 200 {
		return errBody("usage", "invalid request"), 400
	}
	switch in.Action {
	case "labels":
		labels := []string{}
		if s.CF != nil {
			labels = s.CF.Labels()
		}
		return okBody(map[string]any{"labels": labels}), 200
	case "forget":
		if s.CF != nil {
			_ = s.CF.Delete(in.Label)
		}
		return okBody(map[string]any{}), 200
	case "list", "protect", "remove":
	default:
		return errBody("usage", "invalid request"), 400
	}
	token, account := in.Token, in.Account
	if token == "" && s.CF != nil {
		if v, ok := s.CF.Get(in.Label); ok {
			token = v.Token
			if account == "" {
				account = v.Account
			}
		}
	}
	if token == "" {
		return errBody("usage", "paste the Cloudflare API token first"), 400
	}
	c := s.cfClient(token)
	if account == "" {
		accs, err := c.Accounts(ctx)
		if err != nil {
			return errBody("cloudflare", err.Error()), 502
		}
		if len(accs) != 1 {
			return okBody(map[string]any{"need_account": true, "accounts": accs}), 200
		}
		account = accs[0].ID
	}
	remember := func() {
		if in.Remember && s.CF != nil && in.Token != "" {
			_ = s.CF.Put(in.Label, cfaccess.Saved{Token: in.Token, Account: account})
		}
	}
	switch in.Action {
	case "list":
		apps, err := c.Apps(ctx, account)
		if err != nil {
			return errBody("cloudflare", err.Error()), 502
		}
		remember()
		return okBody(map[string]any{"account": account, "apps": apps}), 200
	case "protect":
		addr, err := cfaccess.Address(in.Host, in.Path)
		if err != nil {
			return errBody("usage", err.Error()), 400
		}
		app, err := c.Protect(ctx, account, addr, in.Who)
		if err != nil {
			return errBody("cloudflare", err.Error()), 502
		}
		remember()
		return okBody(map[string]any{"account": account, "app": app}), 200
	default: // remove
		if err := c.Remove(ctx, account, in.AppID); err != nil {
			return errBody("cloudflare", err.Error()), 502
		}
		remember()
		return okBody(map[string]any{"account": account}), 200
	}
}

func (s *Server) registerCFAccess(mux *http.ServeMux) {
	mux.HandleFunc("/api/cf-access", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in cfAccessRequest
		dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8192))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return errBody("usage", "invalid request"), 400
		}
		return s.cfAccess(r.Context(), in)
	}))
}
