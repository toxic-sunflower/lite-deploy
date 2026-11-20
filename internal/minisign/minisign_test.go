package minisign

import (
	"context"
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

// The real minisign program makes the signatures here, so the format is checked against the original, both forms.
func TestAgainstTheRealProgram(t *testing.T) {
	if _, err := runCmd("minisign", "-v"); err != nil {
		t.Skip("minisign is not installed")
	}
	dir := t.TempDir()
	key, pub := filepath.Join(dir, "k.key"), filepath.Join(dir, "k.pub")
	if out, err := runCmd("minisign", "-G", "-W", "-f", "-s", key, "-p", pub); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	msg := filepath.Join(dir, "SHA256SUMS")
	body := []byte("abc  lite-ui-linux-amd64\n")
	if err := os.WriteFile(msg, body, 0o600); err != nil {
		t.Fatal(err)
	}
	pubLines, _ := os.ReadFile(pub)
	pk, err := ParsePublicKey(strings.Split(strings.TrimSpace(string(pubLines)), "\n")[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		args := []string{"-S", "-s", key, "-m", msg}
		if legacy {
			args = append(args, "-l")
		}
		if out, err := runCmd("minisign", args...); err != nil {
			t.Fatalf("%v %s", err, out)
		}
		sig, _ := os.ReadFile(msg + ".minisig")
		if err := pk.Verify(body, sig); err != nil {
			t.Errorf("legacy=%v: %v", legacy, err)
		}
		if err := pk.Verify(append(body, 'x'), sig); err == nil {
			t.Errorf("legacy=%v: a changed file must fail", legacy)
		}
		changed := strings.Replace(string(sig), "\ntrusted comment: ", "\ntrusted comment: x", 1)
		if err := pk.Verify(body, []byte(changed)); err == nil {
			t.Errorf("legacy=%v: a changed comment must fail", legacy)
		}
	}
}

func TestTheKeyOfTheRepository(t *testing.T) {
	b, err := os.ReadFile("../../minisign.pub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePublicKey(strings.Split(strings.TrimSpace(string(b)), "\n")[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePublicKey("nonsense"); err == nil {
		t.Fatal("garbage must be refused")
	}
}
