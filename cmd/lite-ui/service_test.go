package main

import (
	"os"
	"strings"
	"testing"
)

func TestServiceFiles(t *testing.T) {
	u := systemdUnit("/home/a b/.local/bin/lite-ui")
	for _, want := range []string{`ExecStart="/home/a b/.local/bin/lite-ui" --service --no-open`, "Restart=always", "WantedBy=default.target", "LITE_UI_SERVICE=1"} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
	p := launchdPlist("/usr/local/bin/lite-ui", "/tmp/x.log")
	for _, want := range []string{"<string>--service</string>", "<key>KeepAlive</key><true/>", "<key>RunAtLoad</key><true/>", launchdLabel} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
}

func TestTokenIsKept(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, err := loadToken(false); err == nil {
		t.Fatal("no token yet")
	}
	a, err := loadToken(true)
	if err != nil || len(a) != 32 {
		t.Fatalf("token: %q %v", a, err)
	}
	if b, _ := loadToken(false); b != a {
		t.Fatalf("the token changed: %q vs %q", b, a)
	}
	if st, _ := os.Stat(tokenPath()); st.Mode().Perm() != 0o600 {
		t.Errorf("the token file must be private: %v", st.Mode())
	}
}
