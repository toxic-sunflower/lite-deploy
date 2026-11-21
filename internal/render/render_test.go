package render

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
)

var update = flag.Bool("update", false, "overwrite the golden files")

const golden = "../../testdata/shop/golden/"

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := golden + name
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/render -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func apiQuadlet(slot string, port int) actions.InstallQuadlet {
	return actions.InstallQuadlet{Project: "shop", Service: "api", Slot: slot, HostPort: port, ContainerPort: 8080, Memory: "768m", Pids: 512, DatabaseSocket: true}
}

func adminQuadlet(slot string, port int) actions.InstallQuadlet {
	return actions.InstallQuadlet{Project: "shop", Service: "admin", Slot: slot, HostPort: port, ContainerPort: 8080, Memory: "384m", Pids: 256, DatabaseSocket: true}
}

func mainSite() actions.InstallCaddySite {
	return actions.InstallCaddySite{Project: "shop", Site: "main", Host: "shop.example.com", ListenPort: 18080,
		UpstreamInitial: "not_deployed", TrustedProxy: "cloudflare", MaxBody: "5MB",
		Routes: []actions.SiteRoute{{Path: "/api/vault/upload", MaxBody: "100MB"}}}
}

func adminSite() actions.InstallCaddySite {
	return actions.InstallCaddySite{Project: "shop", Site: "admin", Host: "admin.shop.example.com", ListenPort: 18090,
		UpstreamInitial: "not_deployed", TrustedProxy: "cloudflare", MaxBody: "5MB"}
}

func TestGoldenFiles(t *testing.T) {
	checkGolden(t, "shop-api-blue.container", Quadlet(apiQuadlet("blue", 18081)))
	checkGolden(t, "shop-api-green.container", Quadlet(apiQuadlet("green", 18082)))
	checkGolden(t, "shop-admin-blue.container", Quadlet(adminQuadlet("blue", 18091)))
	checkGolden(t, "shop-admin-green.container", Quadlet(adminQuadlet("green", 18092)))
	checkGolden(t, "shop-main.caddy", CaddySite(mainSite()))
	checkGolden(t, "shop-admin.caddy", CaddySite(adminSite()))
	checkGolden(t, "upstream-main.caddy", Upstream(18081, "cloudflare"))
	checkGolden(t, "upstream-plain.caddy", Upstream(18081, "none"))
	checkGolden(t, "sudoers", Sudoers("shop"))
	checkGolden(t, "authorized_keys", AuthorizedKey(actions.InstallDeployKey{Project: "shop",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDtqJ7zOtqQtYqOo0CpvDXNlMhV3HeJDpjrASKGLWdop github-actions"}))
}

func TestQuadletWithoutDatabaseHasNoSocket(t *testing.T) {
	a := apiQuadlet("blue", 18081)
	a.DatabaseSocket = false
	if strings.Contains(Quadlet(a), "postgresql") {
		t.Error("without database_socket the volume is not needed")
	}
}

func TestQuadletHardening(t *testing.T) {
	got := Quadlet(apiQuadlet("blue", 18081))
	for _, must := range []string{"ReadOnly=true", "NoNewPrivileges=true", "DropCapability=ALL", "Tmpfs=/tmp", "PublishPort=127.0.0.1:18081:8080"} {
		if !strings.Contains(got, must) {
			t.Errorf("no %s", must)
		}
	}
	for _, bad := range []string{"Privileged", "User=root", "Network=host", "AddCapability", "PublishPort=0.0.0.0"} {
		if strings.Contains(got, bad) {
			t.Errorf("there must be no %s", bad)
		}
	}
}

func TestAuthorizedKey(t *testing.T) {
	line := AuthorizedKey(actions.InstallDeployKey{Project: "p1", PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDtqJ7zOtqQtYqOo0CpvDXNlMhV3HeJDpjrASKGLWdop my laptop"})
	if !strings.HasPrefix(line, `command="/usr/local/bin/lite-deploy gate --project p1",restrict ssh-ed25519 `) || !strings.HasSuffix(line, " lite-deploy:p1\n") {
		t.Errorf("line: %q", line)
	}
	if strings.Contains(line, "laptop") || strings.Count(line, "\n") != 1 {
		t.Errorf("the key comment must be replaced with a mark: %q", line)
	}
}

func TestEnvFile(t *testing.T) {
	got, err := EnvFile([]EnvLine{{"B", "2"}, {"A", "x y"}})
	if err != nil || got != "A=x y\nB=2\n" {
		t.Fatalf("%q %v", got, err)
	}
	for _, v := range []string{"a\nb", "a\rb", "a\x00b"} {
		if _, err := EnvFile([]EnvLine{{"A", v}}); err == nil {
			t.Errorf("%q: expected a refusal", v)
		}
	}
}

func TestDatabaseURL(t *testing.T) {
	cases := map[string]string{
		"npgsql": "Host=/var/run/postgresql;Database=d;Username=r;Password=pw",
		"url":    "postgresql://r:pw@/d?host=/var/run/postgresql",
		"libpq":  "host=/var/run/postgresql dbname=d user=r password=pw",
	}
	for f, want := range cases {
		got, err := DatabaseURL(f, "r", "d", "pw")
		if err != nil || got != want {
			t.Errorf("%s: %q %v", f, got, err)
		}
	}
	if _, err := DatabaseURL("x", "r", "d", "pw"); err == nil {
		t.Error("unknown format")
	}
}

func TestCaddySiteDirect(t *testing.T) {
	a := mainSite()
	a.TrustedProxy = "cloudflare"
	tunnel := CaddySite(a)
	if strings.Contains(tunnel, "request_header") || strings.Count(tunnel, "{\n\tbind") != 1 {
		t.Fatalf("a tunnel site must stay one loopback block:\n%s", tunnel)
	}
	a.Direct = true
	got := CaddySite(a)
	if !strings.HasPrefix(got, tunnel[:len(tunnel)-1]) {
		t.Error("the loopback block must stay unchanged in direct mode")
	}
	pub := got[len(tunnel):]
	if !strings.Contains(pub, "\n"+a.Host+" {\n") || strings.Contains(pub, "bind 127.0.0.1") {
		t.Errorf("the public block must be a bare host without bind:\n%s", pub)
	}
	if !strings.Contains(pub, "request_header CF-Connecting-IP {remote_host}") {
		t.Errorf("a direct site behind cloudflare trust must overwrite a forged CF-Connecting-IP:\n%s", pub)
	}
}

const testHash = "$2a$12$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234"

// The password is the fallback of direct mode: the tunnel block never has it.
func TestCaddySiteFallbackAuth(t *testing.T) {
	a := mainSite()
	a.AuthPaths = []string{"/admin*", "/stats"}
	a.AuthUsers = []actions.AuthUser{{Name: "root-admin", PasswordHash: testHash}}
	if got := CaddySite(a); strings.Contains(got, "basic_auth") {
		t.Fatalf("a tunnel-only site must not ask for a password:\n%s", got)
	}
	a.Direct = true
	got := CaddySite(a)
	i := strings.Index(got, "\nshop.example.com {")
	if i < 0 {
		t.Fatalf("no public block:\n%s", got)
	}
	if strings.Contains(got[:i], "basic_auth") {
		t.Errorf("the loopback (tunnel) block must not ask for a password:\n%s", got[:i])
	}
	pub := got[i:]
	for _, want := range []string{"@protected path /admin* /stats", "basic_auth @protected {", "root-admin " + testHash} {
		if !strings.Contains(pub, want) {
			t.Errorf("public block lacks %q:\n%s", want, pub)
		}
	}
	a.AuthPaths = nil
	if pub := CaddySite(a); !strings.Contains(pub, "\tbasic_auth {\n") || strings.Contains(pub, "@protected") {
		t.Errorf("no paths means the whole site:\n%s", pub)
	}
}

// A container reaches a host socket (for example the one of PostgreSQL) through the setting `sockets`, with no field about
// databases in the core.
func TestQuadletSockets(t *testing.T) {
	a := apiQuadlet("blue", 18081)
	a.DatabaseSocket = false
	a.Sockets = []string{"/var/run/postgresql", "/run/redis"}
	got := Quadlet(a)
	for _, want := range []string{"Volume=/var/run/postgresql:/var/run/postgresql\n", "Volume=/run/redis:/run/redis\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// the older setting and the new one together do not mount the same directory twice
	a.DatabaseSocket = true
	if n := strings.Count(Quadlet(a), "Volume=/var/run/postgresql:/var/run/postgresql"); n != 1 {
		t.Errorf("the socket is mounted %d times", n)
	}
}
