package cfaccess

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const acc = "0123456789abcdef0123456789abcdef"

func fake(t *testing.T) (*Client, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(b))
		if r.Header.Get("Authorization") != "Bearer tok" {
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/accounts":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"` + acc + `","name":"Acme"}]}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/access/apps"):
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","name":"x","domain":"shop.example.com/admin*"}]}`))
		case r.Method == "POST":
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb","name":"n","domain":"shop.example.com/admin*"}}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL, Token: "tok", HTTP: srv.Client()}, &seen
}

func TestProtectSendsPolicyAndAddress(t *testing.T) {
	c, seen := fake(t)
	app, err := c.Protect(context.Background(), acc, "shop.example.com/admin*", []string{"me@example.com", "@team.example.com"})
	if err != nil || app.ID == "" {
		t.Fatalf("%+v %v", app, err)
	}
	last := (*seen)[len(*seen)-1]
	var body struct {
		Domain   string `json:"domain"`
		Type     string `json:"type"`
		Policies []struct {
			Decision string           `json:"decision"`
			Include  []map[string]any `json:"include"`
		} `json:"policies"`
	}
	if err := json.Unmarshal([]byte(last[strings.Index(last, "{"):]), &body); err != nil {
		t.Fatal(err)
	}
	if body.Domain != "shop.example.com/admin*" || body.Type != "self_hosted" || len(body.Policies) != 1 || body.Policies[0].Decision != "allow" || len(body.Policies[0].Include) != 2 {
		t.Fatalf("unexpected request: %s", last)
	}
}

func TestErrorsAreReadable(t *testing.T) {
	c, _ := fake(t)
	c.Token = "wrong"
	if _, err := c.Accounts(context.Background()); err == nil || !strings.Contains(err.Error(), "Authentication error") {
		t.Fatalf("the Cloudflare message must reach the person: %v", err)
	}
}

func TestInputIsChecked(t *testing.T) {
	for _, bad := range [][]string{nil, {"not an address"}, {"a@b"}} {
		if _, err := Include(bad); err == nil {
			t.Errorf("%v must be refused", bad)
		}
	}
	if _, err := Address("shop.example.com", "/a b"); err == nil {
		t.Error("a path with a space must be refused")
	}
	if a, err := Address("shop.example.com", ""); err != nil || a != "shop.example.com" {
		t.Errorf("%q %v", a, err)
	}
	c, _ := fake(t)
	if _, err := c.Apps(context.Background(), "../etc"); err == nil {
		t.Error("a bad account id must be refused")
	}
}

func TestTunnelRouteAndDNS(t *testing.T) {
	var seen []string
	zone := "fedcba9876543210fedcba9876543210"
	tid := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+string(b))
		switch {
		case r.URL.Path == "/zones" && r.URL.Query().Get("name") == "example.com":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"` + zone + `","name":"example.com"}]}`))
		case r.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/cfd_tunnel"):
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"` + tid + `","name":"lite-acme"}}`))
		case strings.HasSuffix(r.URL.Path, "/token"):
			_, _ = w.Write([]byte(`{"success":true,"result":"eyJ-the-token"}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/configurations"):
			_, _ = w.Write([]byte(`{"success":true,"result":{"config":{"ingress":[{"hostname":"old.example.com","service":"http://127.0.0.1:18001"},{"hostname":"app.example.com","service":"http://127.0.0.1:1"},{"service":"http_status:404"}]}}}`))
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/dns_records"):
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
		}
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: "tok", HTTP: srv.Client()}
	ctx := context.Background()
	z, err := c.ZoneFor(ctx, "app.shop.example.com")
	if err != nil || z.ID != zone {
		t.Fatalf("the zone is found by dropping labels: %+v %v", z, err)
	}
	if _, err := c.ZoneFor(ctx, "app.other.org"); err == nil {
		t.Error("a domain of no account must be refused")
	}
	tn, err := c.CreateTunnel(ctx, acc, "lite-acme")
	if err != nil || tn.ID != tid {
		t.Fatalf("%+v %v", tn, err)
	}
	if tok, err := c.TunnelToken(ctx, acc, tid); err != nil || tok != "eyJ-the-token" {
		t.Fatalf("%q %v", tok, err)
	}
	if err := c.AddRoute(ctx, acc, tid, "app.example.com", "http://127.0.0.1:18080"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetDNS(ctx, zone, "app.example.com", tid); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(seen, "\n")
	for _, want := range []string{`"config_src":"cloudflare"`, `"hostname":"old.example.com"`, `"hostname":"app.example.com","service":"http://127.0.0.1:18080"`,
		`"service":"http_status:404"`, `"content":"` + tid + `.cfargotunnel.com"`, `"proxied":true`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %s in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "127.0.0.1:1\"") {
		t.Error("the old rule of the same host must be replaced")
	}
	if err := c.AddRoute(ctx, acc, tid, "app.example.com", "http://evil.example.org"); err == nil {
		t.Error("a route must point at this server only")
	}
}
