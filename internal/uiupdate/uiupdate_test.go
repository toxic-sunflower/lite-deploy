package uiupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

func runCmd(name string, args ...string) (string, error) {
	res, err := host.ExecRunner{}.Run(context.Background(), host.Cmd{Name: name, Args: args})
	if err == nil && res.ExitCode != 0 {
		err = os.ErrInvalid
	}
	return string(res.Stdout) + string(res.Stderr), err
}

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

// signer makes a key pair with the real minisign and signs texts with it; without the program the test is skipped.
type signer struct {
	t        *testing.T
	dir, key string
	pub      string
}

func newSigner(t *testing.T) *signer {
	t.Helper()
	if _, err := runCmd("minisign", "-v"); err != nil {
		t.Skip("minisign is not installed")
	}
	dir := t.TempDir()
	s := &signer{t: t, dir: dir, key: filepath.Join(dir, "k.key")}
	pubFile := filepath.Join(dir, "k.pub")
	if out, err := runCmd("minisign", "-G", "-W", "-f", "-s", s.key, "-p", pubFile); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	b, _ := os.ReadFile(pubFile)
	s.pub = strings.Split(strings.TrimSpace(string(b)), "\n")[1]
	return s
}

func (s *signer) sign(text []byte) []byte {
	s.t.Helper()
	f := filepath.Join(s.dir, "msg")
	_ = os.WriteFile(f, text, 0o600)
	if out, err := runCmd("minisign", "-S", "-s", s.key, "-m", f); err != nil {
		s.t.Fatalf("%v %s", err, out)
	}
	b, _ := os.ReadFile(f + ".minisig")
	return b
}

func fake(t *testing.T, bin []byte, sumOK bool) *Updater {
	t.Helper()
	sum := sha256.Sum256(bin)
	h := hex.EncodeToString(sum[:])
	if !sumOK {
		h = hex.EncodeToString(make([]byte, 32))
	}
	sg := newSigner(t)
	sums := []byte(h + "  lite-ui-linux-amd64\n")
	sig := sg.sign(sums)
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"tag_name":"v0.2.0"}`)) })
	mux.HandleFunc("/dl/v0.2.0/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(sums) })
	mux.HandleFunc("/dl/v0.2.0/SHA256SUMS.minisig", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(sig) })
	mux.HandleFunc("/dl/v0.2.0/lite-ui-linux-amd64", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(bin) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	exe := filepath.Join(t.TempDir(), "lite-ui")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil { //nolint:gosec // test program
		t.Fatal(err)
	}
	return &Updater{Current: "v0.1.0", APIURL: srv.URL + "/latest", DownloadBase: srv.URL + "/dl", GOOS: "linux", GOARCH: "amd64", Exe: exe, Client: srv.Client(), PubKey: sg.pub}
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

// A release signed by another key (or not signed) is never installed, even when the checksum is right.
func TestApplyRefusesAForeignSignature(t *testing.T) {
	u := fake(t, []byte("new program"), true)
	u.PubKey = newSigner(t).pub // another key pair
	if _, err := u.Apply(context.Background()); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a foreign signature must stop the update: %v", err)
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "old" {
		t.Fatalf("the program was changed: %q", b)
	}
	u.PubKey = ""
	if _, err := u.Apply(context.Background()); err == nil {
		t.Fatal("no key means no update")
	}
}
