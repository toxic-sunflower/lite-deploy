// Package render builds file texts (Quadlet, Caddy, sudoers, authorized_keys, env) from validated parameters.
// The executor receives typed parameters, not file texts (D-42), so all the templates live here.
package render

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
)

// BinaryPath is the path of the binary on the server; its owner is root (D-39).
const BinaryPath = "/usr/local/bin/lite-deploy"

// UnitName is the unit and container name: <project>-<service>-<slot>.
func UnitName(project, service, slot string) string {
	return project + "-" + service + "-" + slot
}

// SiteFileName is the name of the site and upstream file: <project>-<site>.caddy.
func SiteFileName(project, site string) string { return project + "-" + site + ".caddy" }

// UpstreamPath is the path of the site upstream file.
func UpstreamPath(project, site string) string {
	return "/etc/caddy/upstreams/" + SiteFileName(project, site)
}

// Quadlet is the unit text of one service slot (SPEC-v0.md, 4.3, install_quadlet).
func Quadlet(a actions.InstallQuadlet) string {
	var b strings.Builder
	name := UnitName(a.Project, a.Service, a.Slot)
	fmt.Fprintf(&b, "[Unit]\nDescription=%s %s (slot %s)\nAfter=network-online.target\nWants=network-online.target\n\n", a.Project, a.Service, a.Slot)
	fmt.Fprintf(&b, "[Container]\nContainerName=%s\nImage=localhost/%s-%s:%s\n", name, a.Project, a.Service, a.Slot)
	fmt.Fprintf(&b, "PublishPort=127.0.0.1:%d:%d\n", a.HostPort, a.ContainerPort)
	fmt.Fprintf(&b, "EnvironmentFile=%%h/.config/lite-deploy/env/%s.env\n", a.Service)
	if a.DatabaseSocket {
		b.WriteString("Volume=/var/run/postgresql:/var/run/postgresql\n")
	}
	for _, d := range a.Data {
		fmt.Fprintf(&b, "Volume=%%h/%s:/data/%s:U\n", DataDir(d), d)
	}
	b.WriteString("ReadOnly=true\nTmpfs=/tmp\nNoNewPrivileges=true\nDropCapability=ALL\n")
	fmt.Fprintf(&b, "PidsLimit=%d\nPodmanArgs=--memory=%s\n\n", a.Pids, a.Memory)
	b.WriteString("[Service]\nRestart=always\nRestartSec=5\nTimeoutStartSec=120\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

// DataDir is the persistent data directory of a component inside the project user's home directory.
func DataDir(name string) string { return ".local/share/lite-deploy/data/" + name }

// AppDir is the release directory of a slot inside the project user's home directory (for kind unit).
func AppDir(service, slot string) string {
	return ".local/share/lite-deploy/app/" + service + "/" + slot
}

// unitArg is one argument for ExecStart: in double quotes, with quotes, backslash, % and $ escaped.
func unitArg(s string) string { return unitArgWith("", s) }

// unitArgWith is like unitArg, but prefix (for example %h) is inserted as is so that systemd expands it.
func unitArgWith(prefix, s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + prefix + r.Replace(s) + `"`
}

func systemdSize(s string) string { // 768m → 768M
	return strings.ToUpper(s)
}

// Unit is the text of the user systemd unit of one slot of a kind unit component.
func Unit(a actions.InstallUnit) string {
	var b strings.Builder
	dir := "%h/" + AppDir(a.Service, a.Slot)
	fmt.Fprintf(&b, "[Unit]\nDescription=%s %s (slot %s)\nAfter=network-online.target\nWants=network-online.target\nConditionPathExists=%s/.release\n\n", a.Project, a.Service, a.Slot, dir)
	prog := unitArg(a.Exec[0])
	if !strings.HasPrefix(a.Exec[0], "/") {
		prog = unitArgWith("%h/"+AppDir(a.Service, a.Slot)+"/", strings.TrimPrefix(a.Exec[0], "./"))
	}
	args := []string{prog}
	for _, x := range a.Exec[1:] {
		args = append(args, unitArg(x))
	}
	fmt.Fprintf(&b, "[Service]\nWorkingDirectory=%s\nEnvironmentFile=%%h/.config/lite-deploy/env/%s.env\nEnvironment=PORT=%d\n", dir, a.Service, a.HostPort)
	for _, d := range a.Data {
		fmt.Fprintf(&b, "Environment=LITE_DATA_%s=%%h/%s\n", strings.ToUpper(strings.ReplaceAll(d, "-", "_")), DataDir(d))
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(args, " "))
	if len(a.Stop) > 0 {
		stop := unitArg(a.Stop[0])
		if !strings.HasPrefix(a.Stop[0], "/") {
			stop = unitArgWith("%h/"+AppDir(a.Service, a.Slot)+"/", strings.TrimPrefix(a.Stop[0], "./"))
		}
		sargs := []string{stop}
		for _, x := range a.Stop[1:] {
			sargs = append(sargs, unitArg(x))
		}
		fmt.Fprintf(&b, "ExecStop=%s\n", strings.Join(sargs, " "))
	}
	fmt.Fprintf(&b, "Restart=always\nRestartSec=5\nTimeoutStartSec=120\n")
	fmt.Fprintf(&b, "NoNewPrivileges=true\nPrivateTmp=true\nMemoryMax=%s\nTasksMax=%d\n\n[Install]\nWantedBy=default.target\n", systemdSize(a.Memory), a.Pids)
	return b.String()
}

// CaddySite is the text of a site file (SPEC-v0.md, 4.3, install_caddy_site).
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

// UpstreamNotDeployed is the initial upstream content before the first deploy.
const UpstreamNotDeployed = "respond \"not deployed\" 503\n"

// Upstream is the upstream content after a deploy: traffic to the slot port (SPEC-v0.md, 5.2, step 8).
func Upstream(port int, trustedProxy string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "reverse_proxy 127.0.0.1:%d {\n", port)
	if trustedProxy == "cloudflare" {
		b.WriteString("\theader_up X-Forwarded-For {header.CF-Connecting-IP}\n")
	}
	b.WriteString("\theader_up X-Forwarded-Proto https\n}\n")
	return b.String()
}

// Sudoers is the rule that lets a project user only reload Caddy.
func Sudoers(project string) string {
	return fmt.Sprintf("%s ALL=(root) NOPASSWD: /usr/bin/systemctl reload caddy\n", project)
}

// AuthorizedKey is a CI key line with a forced gatekeeper command.
// The original key comment is dropped: the mark lite-deploy:<project> is needed to replace exactly this line later.
func AuthorizedKey(a actions.InstallDeployKey) string {
	f := strings.Fields(a.PublicKey)
	return fmt.Sprintf("command=\"%s gate --project %s\",restrict %s %s lite-deploy:%s\n", BinaryPath, a.Project, f[0], f[1], a.Project)
}

// KeyMarker is the mark of a line in authorized_keys.
func KeyMarker(project string) string { return "lite-deploy:" + project }

// EnvLine is one line of an env file.
type EnvLine struct{ Name, Value string }

// EnvFile is the EnvironmentFile format: NAME=value without quotes. Values with a line break are rejected.
func EnvFile(lines []EnvLine) (string, error) {
	sorted := append([]EnvLine(nil), lines...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	for _, l := range sorted {
		if strings.ContainsAny(l.Value, "\r\n\x00") {
			return "", fmt.Errorf("the value of variable %s contains a line break or NUL", l.Name)
		}
		b.WriteString(l.Name + "=" + l.Value + "\n")
	}
	return b.String(), nil
}

// DatabaseURL is the connection string for the database over the PostgreSQL unix socket (SPEC-v0.md, 2.1).
func DatabaseURL(format, role, db, password string) (string, error) {
	switch format {
	case "npgsql":
		return fmt.Sprintf("Host=/var/run/postgresql;Database=%s;Username=%s;Password=%s", db, role, password), nil
	case "url":
		return fmt.Sprintf("postgresql://%s:%s@/%s?host=/var/run/postgresql", role, password, db), nil
	case "libpq":
		return fmt.Sprintf("host=/var/run/postgresql dbname=%s user=%s password=%s", db, role, password), nil
	}
	return "", fmt.Errorf("unknown connection string format %q", format)
}

// RemoteDatabaseURL is a connection string for PostgreSQL on another server: over the network and only encrypted (sslmode=require).
func RemoteDatabaseURL(format, host string, port int, role, db, password string) (string, error) {
	if port == 0 {
		port = 5432
	}
	switch format {
	case "npgsql":
		return fmt.Sprintf("Host=%s;Port=%d;Database=%s;Username=%s;Password=%s;SSL Mode=Require", host, port, db, role, npgsqlValue(password)), nil
	case "url":
		u := url.URL{Scheme: "postgresql", User: url.UserPassword(role, password), Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/" + db, RawQuery: "sslmode=require"}
		return u.String(), nil
	case "libpq":
		return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=require", host, port, db, role, libpqValue(password)), nil
	}
	return "", fmt.Errorf("unknown connection string format %q", format)
}

func npgsqlValue(s string) string {
	if strings.ContainsAny(s, ";\"' ") || s == "" {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

func libpqValue(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
