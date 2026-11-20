package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Agent names, paths and groups.
const (
	ServiceUser  = "lite-deploy"
	DefaultGroup = "sudo" // the system administrator group; on RHEL and Fedora it is wheel
	APISocket    = "/run/lite-deploy/api.sock"
	ExecSocket   = "/run/lite-deploy/exec.sock"
	SwitchSocket = "/run/lite-deploy/switch.sock"
	UnitDir      = "/etc/systemd/system"
	auditFileRel = "audit.log"
)

// rootHardening are the limits for the services that run as root. They are the ones that cannot get in the way of what the
// services do (installing packages, writing /etc, firewall, users, sysctl). ProtectKernelTunables, ProtectKernelModules,
// ProtectSystem and RestrictSUIDSGID are left out on purpose: the executor writes sysctl settings, ufw loads kernel modules,
// and packages write /etc and install setuid files.
const rootHardening = `NoNewPrivileges=yes
PrivateTmp=yes
ProtectKernelLogs=yes
ProtectClock=yes
LockPersonality=yes
RestrictRealtime=yes
SystemCallArchitectures=native
`

// Units returns the systemd unit files (name → content) for the binary bin.
func Units(bin, group string) map[string]string {
	return map[string]string{
		"lite-deploy-agent.socket": `[Unit]
Description=lite-deploy: agent API

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
Description=lite-deploy: agent (unprivileged)
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
Description=lite-deploy: executor socket (agent only)

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
Description=lite-deploy: executor (root, on request)

[Service]
User=root
ExecStart=` + bin + ` exec
StandardInput=socket
StandardOutput=socket
StandardError=journal
RuntimeMaxSec=1800
` + rootHardening,
		"lite-deploy-switch.socket": `[Unit]
Description=lite-deploy: traffic switch helper socket (project users)

[Socket]
ListenStream=` + SwitchSocket + `
SocketUser=root
SocketGroup=root
SocketMode=0666
DirectoryMode=0755
Accept=yes

[Install]
WantedBy=sockets.target
`,
		"lite-deploy-switch@.service": `[Unit]
Description=lite-deploy: traffic switch helper (root, on request)

[Service]
User=root
ExecStart=` + bin + ` switch-serve
StandardInput=socket
StandardOutput=socket
StandardError=journal
RuntimeMaxSec=300
` + rootHardening,
	}
}

// AdminGroup finds the system administrator group: sudo (Debian, Ubuntu) or wheel (RHEL, Fedora, Arch).
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
	return "", fmt.Errorf("the system has no administrator group (sudo or wheel)")
}

// Install creates the agent service user, writes the unit files and enables the sockets.
// Members of the system administrator group (sudo or wheel) are admitted to the agent.
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
	if err := must("systemctl", "enable", "--now", "lite-deploy-agent.socket", "lite-deploy-exec.socket", "lite-deploy-switch.socket"); err != nil {
		return "", err
	}
	// the agent process is long-lived and keeps running the old binary after an upgrade: restart it if it is running
	// (the executor starts per request, so it always uses the new binary)
	if err := must("systemctl", "try-restart", "lite-deploy-agent.service"); err != nil {
		return "", err
	}
	return group, nil
}

// Uninstall stops and removes the agent and the executor: units, sockets and the service user.
// Project data, secrets, settings and backups are not touched.
func Uninstall(ctx context.Context, r host.Runner, fsys host.FS) error {
	run := func(name string, args ...string) (host.Result, error) {
		return r.Run(ctx, host.Cmd{Name: name, Args: args})
	}
	// disable errors are ignored: the units may be missing already (a repeated uninstall must succeed)
	_, _ = run("systemctl", "disable", "--now", "lite-deploy-agent.socket", "lite-deploy-exec.socket", "lite-deploy-switch.socket", "lite-deploy-agent.service")
	for name := range Units("", DefaultGroup) {
		if err := fsys.Remove(UnitDir + "/" + name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if res, err := run("systemctl", "daemon-reload"); err != nil {
		return err
	} else if res.ExitCode != 0 {
		return fmt.Errorf("systemctl daemon-reload: %s", strings.TrimSpace(string(res.Stderr)))
	}
	if res, err := run("id", "-u", ServiceUser); err == nil && res.ExitCode == 0 {
		if res, err := run("userdel", ServiceUser); err != nil {
			return err
		} else if res.ExitCode != 0 {
			return fmt.Errorf("userdel %s: %s", ServiceUser, strings.TrimSpace(string(res.Stderr)))
		}
	}
	return nil
}

// AuditPath is the path of the agent journal in the service state directory.
func AuditPath(stateDir string) string {
	if stateDir == "" {
		stateDir = "/var/lib/lite-deploy-agent"
	}
	return stateDir + "/" + auditFileRel
}
