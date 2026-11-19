package render

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
)

var update = flag.Bool("update", false, "overwrite the golden files")

const golden = "../../testdata/shop/golden/"
const legacy = "../../testdata/shop/legacy/"

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

// settings returns the file lines without empty ones and without comments, trimmed.
func settings(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// The settings lines of the previous the example project units (SPEC-v0.md, section 11) must be in the generated ones,
// except those that changed on purpose: environment variables moved to the env file.
func TestQuadletMatchesLegacy(t *testing.T) {
	moved := regexp.MustCompile(`^(Environment|EnvironmentFile)=`)
	for _, c := range []struct {
		file string
		gen  actions.InstallQuadlet
	}{
		{"shop-api-blue.container", apiQuadlet("blue", 18081)},
		{"shop-api-green.container", apiQuadlet("green", 18082)},
		{"shop-admin-blue.container", adminQuadlet("blue", 18091)},
		{"shop-admin-green.container", adminQuadlet("green", 18092)},
	} {
		old, err := os.ReadFile(filepath.Join(legacy, "quadlet", c.file))
		if err != nil {
			t.Fatal(err)
		}
		gen := settings(Quadlet(c.gen))
		for _, l := range settings(string(old)) {
			if moved.MatchString(l) || strings.HasPrefix(l, "Description=") {
				continue
			}
			// Memory= exists only in Quadlet of podman 5; PodmanArgs=--memory works in podman 4.x too (Ubuntu 24.04)
			if m, ok := strings.CutPrefix(l, "Memory="); ok {
				l = "PodmanArgs=--memory=" + m
			}
			if !contains(gen, l) {
				t.Errorf("%s: the line %q is not in the generated unit", c.file, l)
			}
		}
	}
}

func TestCaddyMatchesLegacy(t *testing.T) {
	for _, c := range []struct {
		file string
		gen  actions.InstallCaddySite
		name string
	}{
		{"shop.caddy", mainSite(), "shop-main"},
		{"shop-admin.caddy", adminSite(), "shop-admin"},
	} {
		old, err := os.ReadFile(filepath.Join(legacy, "caddy", c.file))
		if err != nil {
			t.Fatal(err)
		}
		gen := settings(CaddySite(c.gen))
		oldBase := strings.TrimSuffix(c.file, ".caddy")
		for _, l := range settings(string(old)) {
			// the file names changed: <project>-<site>.caddy
			l = strings.ReplaceAll(l, oldBase+".caddy", c.name+".caddy")
			l = strings.ReplaceAll(l, oldBase+".log", c.name+".log")
			if strings.HasPrefix(l, "@upload") {
				l = strings.Replace(l, "@upload", "@route1", 1)
			}
			if l == "handle @upload {" {
				l = "handle @route1 {"
			}
			if !contains(gen, l) {
				t.Errorf("%s: the line %q is not in the generated file", c.file, l)
			}
		}
	}
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
