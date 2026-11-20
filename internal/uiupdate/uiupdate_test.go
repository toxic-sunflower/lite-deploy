package uiupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.12.0", "v0.12.1", true}, {"v0.12.1", "v0.12.1", false}, {"v0.12.1", "v0.9.9", false},
		{"v0.9.0", "v0.10.0", true}, {"0.0.0-dev", "v9.9.9", false}, {"v1.0.0", "weird", false},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q,%q)=%v", c.cur, c.latest, got)
		}
	}
}

func fake(t *testing.T, bin []byte, sumOK bool) *Updater {
	t.Helper()
	sum := sha256.Sum256(bin)
	h := hex.EncodeToString(sum[:])
	if !sumOK {
		h = hex.EncodeToString(make([]byte, 32))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"tag_name":"v0.2.0"}`)) })
	mux.HandleFunc("/dl/v0.2.0/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(h + "  lite-ui-linux-amd64\n")) })
	mux.HandleFunc("/dl/v0.2.0/lite-ui-linux-amd64", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(bin) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	exe := filepath.Join(t.TempDir(), "lite-ui")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil { //nolint:gosec // test program
		t.Fatal(err)
	}
	return &Updater{Current: "v0.1.0", APIURL: srv.URL + "/latest", DownloadBase: srv.URL + "/dl", GOOS: "linux", GOARCH: "amd64", Exe: exe, Client: srv.Client()}
}

func TestApply(t *testing.T) {
	u := fake(t, []byte("new program"), true)
	info, err := u.Check(context.Background())
	if err != nil || !info.Available || info.Latest != "v0.2.0" {
		t.Fatalf("check: %+v %v", info, err)
	}
	tag, err := u.Apply(context.Background())
	if err != nil || tag != "v0.2.0" {
		t.Fatalf("apply: %q %v", tag, err)
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "new program" {
		t.Fatalf("program not replaced: %q", b)
	}
}

func TestApplyRefusesWrongChecksum(t *testing.T) {
	u := fake(t, []byte("new program"), false)
	if _, err := u.Apply(context.Background()); err == nil {
		t.Fatal("a wrong checksum must stop the update")
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "old" {
		t.Fatalf("the program was changed: %q", b)
	}
}
