// Package cfaccess closes a domain (or a path of it) with Cloudflare Access: a login page of Cloudflare in front of the
// site. It talks to the Cloudflare API with a token that lives on the workstation only (never on the server), and it is
// used by lite-ui.
package cfaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultBase is the Cloudflare API.
const DefaultBase = "https://api.cloudflare.com/client/v4"

// Client is one Cloudflare account reached with one token.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// New makes a client for the real API.
func New(token string) *Client {
	return &Client{Base: DefaultBase, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Account is a Cloudflare account the token can see.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// App is an Access application: a protected address.
type App struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach Cloudflare: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("Cloudflare answered %d with something unexpected", resp.StatusCode)
	}
	if !env.Success {
		if len(env.Errors) > 0 {
			msgs := make([]string, 0, len(env.Errors))
			for _, e := range env.Errors {
				msgs = append(msgs, e.Message)
			}
			return nil, errors.New("Cloudflare: " + strings.Join(msgs, "; "))
		}
		return nil, fmt.Errorf("Cloudflare answered %d", resp.StatusCode)
	}
	return env.Result, nil
}

var idRe = regexp.MustCompile(`^[0-9a-f]{32}$|^[0-9a-f-]{36}$`)

// Accounts lists the accounts the token can see.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	raw, err := c.do(ctx, http.MethodGet, "/accounts?per_page=50", nil)
	if err != nil {
		return nil, err
	}
	var out []Account
	return out, json.Unmarshal(raw, &out)
}

// Apps lists the Access applications of an account.
func (c *Client) Apps(ctx context.Context, account string) ([]App, error) {
	if !idRe.MatchString(account) {
		return nil, errors.New("invalid account id")
	}
	raw, err := c.do(ctx, http.MethodGet, "/accounts/"+account+"/access/apps?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	var out []App
	return out, json.Unmarshal(raw, &out)
}

var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
var domainRe = regexp.MustCompile(`^@([A-Za-z0-9-]+\.)+[A-Za-z]{2,}$`)

// Include turns the list of people into the rules of the policy: an address is one person, "@company.com" is everyone of
// that mail domain.
func Include(who []string) ([]map[string]any, error) {
	if len(who) == 0 || len(who) > 50 {
		return nil, errors.New("give at least one e-mail address (or @domain), at most 50")
	}
	var out []map[string]any
	for _, w := range who {
		w = strings.TrimSpace(w)
		switch {
		case emailRe.MatchString(w):
			out = append(out, map[string]any{"email": map[string]string{"email": w}})
		case domainRe.MatchString(w):
			out = append(out, map[string]any{"email_domain": map[string]string{"domain": strings.TrimPrefix(w, "@")}})
		default:
			return nil, fmt.Errorf("%q is not an e-mail address (use name@example.com, or @example.com for a whole domain)", w)
		}
	}
	return out, nil
}

var hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
var pathRe = regexp.MustCompile(`^/[^\s{}?#]*$`)

// Address joins a host and an optional path into the address of an application.
func Address(host, path string) (string, error) {
	if !hostRe.MatchString(host) {
		return "", errors.New("invalid domain")
	}
	if path == "" {
		return host, nil
	}
	if !pathRe.MatchString(path) || len(path) > 200 {
		return "", errors.New("a path starts with / and has no spaces")
	}
	return host + path, nil
}

// Protect creates an application for the address that lets in only the given people.
func (c *Client) Protect(ctx context.Context, account, address string, who []string) (App, error) {
	if !idRe.MatchString(account) {
		return App{}, errors.New("invalid account id")
	}
	include, err := Include(who)
	if err != nil {
		return App{}, err
	}
	body := map[string]any{
		"name": "Lite Deploy " + address, "domain": address, "type": "self_hosted", "session_duration": "24h",
		"policies": []map[string]any{{"name": "Allowed people", "decision": "allow", "include": include}},
	}
	raw, err := c.do(ctx, http.MethodPost, "/accounts/"+account+"/access/apps", body)
	if err != nil {
		return App{}, err
	}
	var app App
	return app, json.Unmarshal(raw, &app)
}

// Remove deletes an application: the address is open again (to whoever can reach it).
func (c *Client) Remove(ctx context.Context, account, id string) error {
	if !idRe.MatchString(account) || !idRe.MatchString(id) {
		return errors.New("invalid id")
	}
	_, err := c.do(ctx, http.MethodDelete, "/accounts/"+account+"/access/apps/"+url.PathEscape(id), nil)
	return err
}
