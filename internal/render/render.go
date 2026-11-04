// Package render строит тексты файлов (Quadlet, Caddy, sudoers, authorized_keys, env) из проверенных параметров.
// Исполнитель получает типизированные параметры, а не тексты файлов (D-42), поэтому все шаблоны живут здесь.
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
)

// BinaryPath путь к бинарнику на сервере; его владелец root (D-39).
const BinaryPath = "/usr/local/bin/lite-deploy"

// UnitName имя юнита и контейнера: <проект>-<сервис>-<слот>.
func UnitName(project, service, slot string) string {
	return project + "-" + service + "-" + slot
}

// SiteFileName имя файла сайта и upstream: <проект>-<сайт>.caddy.
func SiteFileName(project, site string) string { return project + "-" + site + ".caddy" }

// UpstreamPath путь к файлу upstream сайта.
func UpstreamPath(project, site string) string {
	return "/etc/caddy/upstreams/" + SiteFileName(project, site)
}

// Quadlet текст юнита одного слота сервиса (SPEC-v0.md, 4.3, install_quadlet).
func Quadlet(a actions.InstallQuadlet) string {
	var b strings.Builder
	name := UnitName(a.Project, a.Service, a.Slot)
	fmt.Fprintf(&b, "[Unit]\nDescription=%s %s (слот %s)\nAfter=network-online.target\nWants=network-online.target\n\n", a.Project, a.Service, a.Slot)
	fmt.Fprintf(&b, "[Container]\nContainerName=%s\nImage=localhost/%s-%s:%s\n", name, a.Project, a.Service, a.Slot)
	fmt.Fprintf(&b, "PublishPort=127.0.0.1:%d:%d\n", a.HostPort, a.ContainerPort)
	fmt.Fprintf(&b, "EnvironmentFile=%%h/.config/lite-deploy/env/%s.env\n", a.Service)
	if a.DatabaseSocket {
		b.WriteString("Volume=/var/run/postgresql:/var/run/postgresql\n")
	}
	b.WriteString("ReadOnly=true\nTmpfs=/tmp\nNoNewPrivileges=true\nDropCapability=ALL\n")
	fmt.Fprintf(&b, "PidsLimit=%d\nMemory=%s\n\n", a.Pids, a.Memory)
	b.WriteString("[Service]\nRestart=always\nRestartSec=5\nTimeoutStartSec=120\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

// CaddySite текст файла сайта (SPEC-v0.md, 4.3, install_caddy_site).
func CaddySite(a actions.InstallCaddySite) string {
	var b strings.Builder
	up := UpstreamPath(a.Project, a.Site)
	fmt.Fprintf(&b, "http://%s:%d {\n\tbind 127.0.0.1\n\n\tencode zstd gzip\n\n", a.Host, a.ListenPort)
	b.WriteString("\theader {\n\t\tStrict-Transport-Security \"max-age=31536000\"\n\t\tX-Content-Type-Options nosniff\n\t\tReferrer-Policy strict-origin-when-cross-origin\n\t\t-Server\n\t}\n\n")
	if len(a.Routes) == 0 {
		fmt.Fprintf(&b, "\trequest_body {\n\t\tmax_size %s\n\t}\n\timport %s\n", a.MaxBody, up)
	} else {
		for i, r := range a.Routes {
			fmt.Fprintf(&b, "\t@route%d path %s\n\thandle @route%d {\n\t\trequest_body {\n\t\t\tmax_size %s\n\t\t}\n\t\timport %s\n\t}\n\n", i+1, r.Path, i+1, r.MaxBody, up)
		}
		fmt.Fprintf(&b, "\thandle {\n\t\trequest_body {\n\t\t\tmax_size %s\n\t\t}\n\t\timport %s\n\t}\n", a.MaxBody, up)
	}
	fmt.Fprintf(&b, "\n\tlog {\n\t\toutput file /var/log/caddy/%s.log {\n\t\t\troll_size 50MiB\n\t\t\troll_keep 5\n\t\t}\n\t}\n}\n", a.Project+"-"+a.Site)
	return b.String()
}

// UpstreamNotDeployed начальное содержимое upstream до первой выкатки.
const UpstreamNotDeployed = "respond \"not deployed\" 503\n"

// Upstream содержимое upstream после выкатки: трафик на порт слота (SPEC-v0.md, 5.2, шаг 8).
func Upstream(port int, trustedProxy string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "reverse_proxy 127.0.0.1:%d {\n", port)
	if trustedProxy == "cloudflare" {
		b.WriteString("\theader_up X-Forwarded-For {header.CF-Connecting-IP}\n")
	}
	b.WriteString("\theader_up X-Forwarded-Proto https\n}\n")
	return b.String()
}

// Sudoers правило, разрешающее проектному пользователю только reload Caddy.
func Sudoers(project string) string {
	return fmt.Sprintf("%s ALL=(root) NOPASSWD: /usr/bin/systemctl reload caddy\n", project)
}

// AuthorizedKey строка ключа CI с принудительной командой-привратником.
// Исходный комментарий ключа отбрасывается: метка lite-deploy:<проект> нужна, чтобы позже заменить именно эту строку.
func AuthorizedKey(a actions.InstallDeployKey) string {
	f := strings.Fields(a.PublicKey)
	return fmt.Sprintf("command=\"%s gate --project %s\",restrict %s %s lite-deploy:%s\n", BinaryPath, a.Project, f[0], f[1], a.Project)
}

// KeyMarker метка строки в authorized_keys.
func KeyMarker(project string) string { return "lite-deploy:" + project }

// EnvLine одна строка env-файла.
type EnvLine struct{ Name, Value string }

// EnvFile формат EnvironmentFile: NAME=value без кавычек. Значения с переводом строки отклоняются.
func EnvFile(lines []EnvLine) (string, error) {
	sorted := append([]EnvLine(nil), lines...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	for _, l := range sorted {
		if strings.ContainsAny(l.Value, "\r\n\x00") {
			return "", fmt.Errorf("значение переменной %s содержит перевод строки или NUL", l.Name)
		}
		b.WriteString(l.Name + "=" + l.Value + "\n")
	}
	return b.String(), nil
}

// DatabaseURL строка подключения к базе по unix-сокету PostgreSQL (SPEC-v0.md, 2.1).
func DatabaseURL(format, role, db, password string) (string, error) {
	switch format {
	case "npgsql":
		return fmt.Sprintf("Host=/var/run/postgresql;Database=%s;Username=%s;Password=%s", db, role, password), nil
	case "url":
		return fmt.Sprintf("postgresql://%s:%s@/%s?host=/var/run/postgresql", role, password, db), nil
	case "libpq":
		return fmt.Sprintf("host=/var/run/postgresql dbname=%s user=%s password=%s", db, role, password), nil
	}
	return "", fmt.Errorf("неизвестный формат строки подключения %q", format)
}
