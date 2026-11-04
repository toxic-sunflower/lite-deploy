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

var update = flag.Bool("update", false, "перезаписать golden-файлы")

const golden = "../../testdata/fanvue-games/golden/"
const legacy = "../../projects/fanvue-games/"

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
		t.Fatalf("%v (запустите go test ./internal/render -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s отличается от golden:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func apiQuadlet(slot string, port int) actions.InstallQuadlet {
	return actions.InstallQuadlet{Project: "fanvue-games", Service: "api", Slot: slot, HostPort: port, ContainerPort: 8080, Memory: "768m", Pids: 512, DatabaseSocket: true}
}

func adminQuadlet(slot string, port int) actions.InstallQuadlet {
	return actions.InstallQuadlet{Project: "fanvue-games", Service: "admin", Slot: slot, HostPort: port, ContainerPort: 8080, Memory: "384m", Pids: 256, DatabaseSocket: true}
}

func mainSite() actions.InstallCaddySite {
	return actions.InstallCaddySite{Project: "fanvue-games", Site: "main", Host: "fanvue-games.prozorov.site", ListenPort: 18080,
		UpstreamInitial: "not_deployed", TrustedProxy: "cloudflare", MaxBody: "5MB",
		Routes: []actions.SiteRoute{{Path: "/api/vault/upload", MaxBody: "100MB"}}}
}

func adminSite() actions.InstallCaddySite {
	return actions.InstallCaddySite{Project: "fanvue-games", Site: "admin", Host: "admin.fanvue-games.prozorov.site", ListenPort: 18090,
		UpstreamInitial: "not_deployed", TrustedProxy: "cloudflare", MaxBody: "5MB"}
}

func TestGoldenFiles(t *testing.T) {
	checkGolden(t, "fanvue-games-api-blue.container", Quadlet(apiQuadlet("blue", 18081)))
	checkGolden(t, "fanvue-games-api-green.container", Quadlet(apiQuadlet("green", 18082)))
	checkGolden(t, "fanvue-games-admin-blue.container", Quadlet(adminQuadlet("blue", 18091)))
	checkGolden(t, "fanvue-games-admin-green.container", Quadlet(adminQuadlet("green", 18092)))
	checkGolden(t, "fanvue-games-main.caddy", CaddySite(mainSite()))
	checkGolden(t, "fanvue-games-admin.caddy", CaddySite(adminSite()))
	checkGolden(t, "upstream-main.caddy", Upstream(18081, "cloudflare"))
	checkGolden(t, "upstream-plain.caddy", Upstream(18081, "none"))
	checkGolden(t, "sudoers", Sudoers("fanvue-games"))
	checkGolden(t, "authorized_keys", AuthorizedKey(actions.InstallDeployKey{Project: "fanvue-games",
		PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDtqJ7zOtqQtYqOo0CpvDXNlMhV3HeJDpjrASKGLWdop github-actions"}))
}

// settings возвращает строки файла без пустых и без комментариев, с обрезанными краями.
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

// Строки настроек прежних юнитов FanVue Games (SPEC-v0.md, раздел 11) должны быть в сгенерированных,
// кроме тех, что осознанно изменились: переменные окружения переехали в env-файл.
func TestQuadletMatchesLegacy(t *testing.T) {
	moved := regexp.MustCompile(`^(Environment|EnvironmentFile)=`)
	for _, c := range []struct {
		file string
		gen  actions.InstallQuadlet
	}{
		{"fanvue-games-api-blue.container", apiQuadlet("blue", 18081)},
		{"fanvue-games-api-green.container", apiQuadlet("green", 18082)},
		{"fanvue-games-admin-blue.container", adminQuadlet("blue", 18091)},
		{"fanvue-games-admin-green.container", adminQuadlet("green", 18092)},
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
			if !contains(gen, l) {
				t.Errorf("%s: строки %q нет в сгенерированном юните", c.file, l)
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
		{"fanvue-games.caddy", mainSite(), "fanvue-games-main"},
		{"fanvue-games-admin.caddy", adminSite(), "fanvue-games-admin"},
	} {
		old, err := os.ReadFile(filepath.Join(legacy, "caddy", c.file))
		if err != nil {
			t.Fatal(err)
		}
		gen := settings(CaddySite(c.gen))
		oldBase := strings.TrimSuffix(c.file, ".caddy")
		for _, l := range settings(string(old)) {
			// имена файлов изменились: <проект>-<сайт>.caddy
			l = strings.ReplaceAll(l, oldBase+".caddy", c.name+".caddy")
			l = strings.ReplaceAll(l, oldBase+".log", c.name+".log")
			if strings.HasPrefix(l, "@upload") {
				l = strings.Replace(l, "@upload", "@route1", 1)
			}
			if l == "handle @upload {" {
				l = "handle @route1 {"
			}
			if !contains(gen, l) {
				t.Errorf("%s: строки %q нет в сгенерированном файле", c.file, l)
			}
		}
	}
}

func TestQuadletWithoutDatabaseHasNoSocket(t *testing.T) {
	a := apiQuadlet("blue", 18081)
	a.DatabaseSocket = false
	if strings.Contains(Quadlet(a), "postgresql") {
		t.Error("без database_socket том не нужен")
	}
}

func TestQuadletHardening(t *testing.T) {
	got := Quadlet(apiQuadlet("blue", 18081))
	for _, must := range []string{"ReadOnly=true", "NoNewPrivileges=true", "DropCapability=ALL", "Tmpfs=/tmp", "PublishPort=127.0.0.1:18081:8080"} {
		if !strings.Contains(got, must) {
			t.Errorf("нет %s", must)
		}
	}
	for _, bad := range []string{"Privileged", "User=root", "Network=host", "AddCapability", "PublishPort=0.0.0.0"} {
		if strings.Contains(got, bad) {
			t.Errorf("не должно быть %s", bad)
		}
	}
}

func TestAuthorizedKey(t *testing.T) {
	line := AuthorizedKey(actions.InstallDeployKey{Project: "p1", PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDtqJ7zOtqQtYqOo0CpvDXNlMhV3HeJDpjrASKGLWdop my laptop"})
	if !strings.HasPrefix(line, `command="/usr/local/bin/lite-deploy gate --project p1",restrict ssh-ed25519 `) || !strings.HasSuffix(line, " lite-deploy:p1\n") {
		t.Errorf("строка: %q", line)
	}
	if strings.Contains(line, "laptop") || strings.Count(line, "\n") != 1 {
		t.Errorf("комментарий ключа должен быть заменён меткой: %q", line)
	}
}

func TestEnvFile(t *testing.T) {
	got, err := EnvFile([]EnvLine{{"B", "2"}, {"A", "x y"}})
	if err != nil || got != "A=x y\nB=2\n" {
		t.Fatalf("%q %v", got, err)
	}
	for _, v := range []string{"a\nb", "a\rb", "a\x00b"} {
		if _, err := EnvFile([]EnvLine{{"A", v}}); err == nil {
			t.Errorf("%q: ожидали отказ", v)
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
		t.Error("неизвестный формат")
	}
}
