package cfaccess

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestStoreIsPrivate(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "x", "cf.json")}
	if err := s.Put("acme", Saved{Token: "tok", Account: acc}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(s.Path); st.Mode().Perm() != 0o600 {
		t.Errorf("the file must be private: %v", st.Mode())
	}
	if l := s.Labels(); len(l) != 1 || l[0] != "acme" {
		t.Errorf("labels: %v", l)
	}
	if v, ok := s.Get("acme"); !ok || v.Token != "tok" {
		t.Errorf("get: %+v", v)
	}
	_ = s.Delete("acme")
	if _, ok := s.Get("acme"); ok {
		t.Error("deleted")
	}
}
