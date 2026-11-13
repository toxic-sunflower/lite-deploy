package agent

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Имена, пути и группы агента.
const (
	ServiceUser  = "lite-deploy"
	DefaultGroup = "sudo" // группа администраторов системы; на RHEL и Fedora это wheel
	APISocket    = "/run/lite-deploy/api.sock"
	ExecSocket   = "/run/lite-deploy/exec.sock"
	UnitDir      = "/etc/systemd/system"
	auditFileRel = "audit.log"
)

// Units возвращает файлы unit systemd (имя → содержимое) для бинарника bin.
func Units(bin, group string) map[string]string {
	return map[string]string{
		"lite-deploy-agent.socket": `[Unit]
Description=lite-deploy: API агента

[Socket]
ListenStream=` + APISocket + `
SocketUser=` + ServiceUser + `
SocketGroup=` + group + `
SocketMode=0660
DirectoryMode=0755

[Install]
WantedBy=sockets.target
`,
		"lite-deploy-agent.service": `[Unit]
Description=lite-deploy: агент (без прав)
Requires=lite-deploy-agent.socket
After=lite-deploy-agent.socket

[Service]
User=` + ServiceUser + `
Group=` + ServiceUser + `
ExecStart=` + bin + ` agent serve --group ` + group + `
StateDirectory=lite-deploy-agent
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictAddressFamilies=AF_UNIX
CapabilityBoundingSet=
`,
		"lite-deploy-exec.socket": `[Unit]
Description=lite-deploy: сокет исполнителя (только для агента)

[Socket]
ListenStream=` + ExecSocket + `
SocketUser=` + ServiceUser + `
SocketGroup=` + ServiceUser + `
SocketMode=0600
DirectoryMode=0755
Accept=yes

[Install]
WantedBy=sockets.target
`,
		"lite-deploy-exec@.service": `[Unit]
Description=lite-deploy: исполнитель (root, по запросу)

[Service]
User=root
ExecStart=` + bin + ` exec
StandardInput=socket
StandardOutput=socket
StandardError=journal
RuntimeMaxSec=1800
NoNewPrivileges=yes
PrivateTmp=yes
`,
	}
}

// AdminGroup определяет группу администраторов системы: sudo (Debian, Ubuntu) или wheel (RHEL, Fedora, Arch).
func AdminGroup(ctx context.Context, r host.Runner) (string, error) {
	for _, g := range []string{"sudo", "wheel"} {
		res, err := r.Run(ctx, host.Cmd{Name: "getent", Args: []string{"group", g}})
		if err != nil {
			return "", err
		}
		if res.ExitCode == 0 {
			return g, nil
		}
	}
	return "", fmt.Errorf("в системе нет группы администраторов (sudo или wheel)")
}

// Install создаёт пользователя службы агента, записывает unit-файлы и включает сокеты.
// К агенту допускаются члены группы администраторов системы (sudo или wheel).
func Install(ctx context.Context, r host.Runner, fsys host.FS, bin string) (group string, err error) {
	group, err = AdminGroup(ctx, r)
	if err != nil {
		return "", err
	}
	run := func(name string, args ...string) (host.Result, error) {
		return r.Run(ctx, host.Cmd{Name: name, Args: args})
	}
	must := func(name string, args ...string) error {
		res, err := run(name, args...)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(res.Stderr)))
		}
		return nil
	}
	if res, err := run("id", "-u", ServiceUser); err != nil {
		return "", err
	} else if res.ExitCode != 0 {
		if err := must("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", ServiceUser); err != nil {
			return "", err
		}
	}
	for name, body := range Units(bin, group) {
		if err := fsys.WriteFile(UnitDir+"/"+name, []byte(body), fs.FileMode(0o644)); err != nil {
			return "", err
		}
	}
	if err := must("systemctl", "daemon-reload"); err != nil {
		return "", err
	}
	if err := must("systemctl", "enable", "--now", "lite-deploy-agent.socket", "lite-deploy-exec.socket"); err != nil {
		return "", err
	}
	return group, nil
}

// AuditPath путь журнала агента в каталоге состояния службы.
func AuditPath(stateDir string) string {
	if stateDir == "" {
		stateDir = "/var/lib/lite-deploy-agent"
	}
	return stateDir + "/" + auditFileRel
}
