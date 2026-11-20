package builtin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// The files managed by the security plugin. The sshd file is named so that it is read before cloud-init (50-cloud-init.conf):
// sshd takes the first value of a parameter, and an earlier file wins.
const (
	sshdDir        = "/etc/ssh/sshd_config.d"
	SSHSecurity    = sshdDir + "/01-lite-security.conf"
	sysctlFile     = "/etc/sysctl.d/60-lite-security.conf"
	jailFile       = "/etc/fail2ban/jail.d/lite-deploy.local"
	aptAutoFile    = "/etc/apt/apt.conf.d/52lite-deploy-auto-upgrades"
	dnfAutoFile    = "/etc/dnf/automatic.conf"
	securityMark   = "# managed-by: lite-deploy (security): do not edit by hand, lite security rewrites this file"
	rootProhibitPw = "prohibit-password"
)

const sysctlBody = securityMark + `
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv6.conf.all.accept_redirects = 0
net.ipv6.conf.default.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.default.send_redirects = 0
net.ipv4.conf.all.accept_source_route = 0
net.ipv6.conf.all.accept_source_route = 0
net.ipv4.conf.all.log_martians = 1
net.ipv4.icmp_echo_ignore_broadcasts = 1
net.ipv4.tcp_syncookies = 1
kernel.dmesg_restrict = 1
kernel.kptr_restrict = 2
fs.protected_hardlinks = 1
fs.protected_symlinks = 1
`

// sysctlChecks are the kernel parameters that verify checks.
var sysctlChecks = [][2]string{{"net.ipv4.tcp_syncookies", "1"}, {"net.ipv4.conf.all.rp_filter", "1"}, {"net.ipv4.conf.all.accept_redirects", "0"}, {"net.ipv4.conf.all.accept_source_route", "0"}}

// Security is the `security` plugin: server protection (R-23).
type Security struct{}

func (Security) Name() string       { return "security" }
func (Security) Kind() string       { return "builtin" }
func (Security) Requires() []string { return nil }
func (Security) Description() string {
	return "server protection: SSH, fail2ban, automatic security updates, kernel parameters, login journal"
}
func (Security) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "status", Root: true, Help: "protection state (the same as the security checks in lite verify)"},
		{Name: "enable", Help: "enable everything: fail2ban, automatic updates, kernel parameters, then no SSH passwords and no root password login; no-root forbids root login entirely", Args: "[no-root]", Confirm: true},
		{Name: "ssh", Help: "SSH: key-only login for everyone, root by key only (no-root: root cannot log in at all)", Args: "[no-root]", Confirm: true},
		{Name: "allow-users", Help: "SSH: allow only the listed users (any: remove the restriction); project users are added automatically", Args: "NAME…|any", Confirm: true},
		{Name: "logins", Root: true, Help: "login journal: recent logins and failed attempts", Args: "[NUMBER]"},
		{Name: "banned", Root: true, Help: "who fail2ban has banned right now"},
		{Name: "unban", Help: "remove a fail2ban ban from an address", Args: "ADDRESS", Confirm: true},
	}
}

type sec struct{ tools }

func newSec(ctx context.Context, env plugin.Env) *sec { return &sec{tools{ctx, env}} }

// sshdEffective is the effective sshd settings (`sshd -T`), names in lower case.
func (s *sec) sshdEffective() (map[string]string, error) {
	res, err := s.run("sshd", "-T")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("sshd -T: %s", strings.TrimSpace(string(res.Stderr)))
	}
	m := map[string]string{}
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), " ")
		if k != "" {
			if _, dup := m[k]; !dup {
				m[k] = strings.TrimSpace(v)
			}
		}
	}
	// in newer OpenSSH versions sshd -T prints prohibit-password as without-password: it is one value
	if m["permitrootlogin"] == "without-password" {
		m["permitrootlogin"] = "prohibit-password"
	}
	return m, nil
}

func (s *sec) sshPort() int {
	if m, err := s.sshdEffective(); err == nil {
		if p, err := strconv.Atoi(m["port"]); err == nil {
			return p
		}
	}
	return 22
}

// account is an /etc/passwd entry.
type account struct {
	Name, Home, Shell string
	UID               int
}

func (s *sec) accounts() []account {
	b, err := s.env.FS.ReadFile("/etc/passwd")
	if err != nil {
		return nil
	}
	var out []account
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		out = append(out, account{Name: f[0], UID: uid, Home: f[5], Shell: f[6]})
	}
	return out
}

// admins are the users of the sudo and wheel groups.
func (s *sec) admins() []string {
	b, err := s.env.FS.ReadFile("/etc/group")
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) >= 4 && (f[0] == "sudo" || f[0] == "wheel") {
			for _, u := range strings.Split(f[3], ",") {
				if u != "" {
					set[u] = true
				}
			}
		}
	}
	var out []string
	for u := range set {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

func (s *sec) hasKey(user string) bool {
	for _, a := range s.accounts() {
		if a.Name != user {
			continue
		}
		b, err := s.env.FS.ReadFile(a.Home + "/.ssh/authorized_keys")
		if err != nil {
			return false
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				return true
			}
		}
	}
	return false
}

// sshConf is what the security plugin keeps in the sshd file.
type sshConf struct {
	Root       string // prohibit-password or no
	AllowUsers []string
}

func (s *sec) readSSHConf() sshConf {
	c := sshConf{Root: rootProhibitPw}
	b, err := s.env.FS.ReadFile(SSHSecurity)
	if err != nil {
		return c
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "PermitRootLogin" {
			c.Root = f[1]
		}
		if len(f) >= 2 && f[0] == "AllowUsers" {
			c.AllowUsers = f[1:]
		}
	}
	return c
}

func (c sshConf) body() string {
	var sb strings.Builder
	sb.WriteString(securityMark + "\n")
	fmt.Fprintf(&sb, "PermitRootLogin %s\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPubkeyAuthentication yes\nPermitEmptyPasswords no\nMaxAuthTries 4\nLoginGraceTime 30\nX11Forwarding no\n", c.Root)
	if len(c.AllowUsers) > 0 {
		fmt.Fprintf(&sb, "AllowUsers %s\n", strings.Join(c.AllowUsers, " "))
	}
	return sb.String()
}

// guardSSH keeps the server from being locked out: an administrator with a key must remain, and the caller must have a key.
func (s *sec) guardSSH(c sshConf) error {
	var keyed []string
	for _, a := range s.admins() {
		if s.hasKey(a) {
			keyed = append(keyed, a)
		}
	}
	if len(keyed) == 0 {
		e := plugin.Errorf("precondition_failed", "there is no administrator (sudo) with an SSH key: after disabling passwords you would lose access")
		e.Hint = "first: lite init --user NAME (creates the user and asks for a key)"
		return e
	}
	if u := s.env.User; u != "" && !s.hasKey(u) {
		e := plugin.Errorf("precondition_failed", "user %s has no SSH key: after disabling passwords they would lose access", u)
		e.Hint = "add the key to ~/.ssh/authorized_keys or log in as another administrator"
		return e
	}
	if len(c.AllowUsers) > 0 {
		ok := false
		for _, u := range c.AllowUsers {
			for _, k := range keyed {
				ok = ok || u == k
			}
		}
		if !ok {
			return plugin.Errorf("precondition_failed", "AllowUsers would contain no administrator with a key (%s): access would be lost", strings.Join(keyed, ", "))
		}
		if s.env.User != "" {
			in := false
			for _, u := range c.AllowUsers {
				in = in || u == s.env.User
			}
			if !in {
				return plugin.Errorf("precondition_failed", "you (%s) are not in the AllowUsers list: after applying this you would lose access", s.env.User)
			}
		}
	}
	return nil
}

// applySSH writes the sshd file, checks the configuration and the effective values, reloads sshd.
// On any failure the previous state is restored.
func (s *sec) applySSH(c sshConf) error {
	if err := s.guardSSH(c); err != nil {
		return err
	}
	fs := s.env.FS
	if main, err := fs.ReadFile("/etc/ssh/sshd_config"); err == nil && !strings.Contains(string(main), "sshd_config.d") {
		return plugin.Errorf("precondition_failed", "/etc/ssh/sshd_config has no Include /etc/ssh/sshd_config.d/*.conf line: SSH settings were not changed")
	}
	old, oldErr := fs.ReadFile(SSHSecurity)
	body := c.body()
	if oldErr == nil && string(old) == body {
		return nil
	}
	if err := fs.MkdirAll(sshdDir, 0o755); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := fs.WriteFile(SSHSecurity, []byte(body), 0o644); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	restore := func() {
		if oldErr == nil {
			_ = fs.WriteFile(SSHSecurity, old, 0o644)
		} else {
			_ = fs.Remove(SSHSecurity)
		}
	}
	// the privilege separation directory is created by the ssh service at start; with socket activation it may not exist yet
	_ = fs.MkdirAll("/run/sshd", 0o755)
	if err := s.must("sshd", "-t"); err != nil {
		restore()
		return plugin.Errorf("step_failed", "the new sshd configuration failed validation, the previous one was restored: %v", err)
	}
	eff, err := s.sshdEffective()
	if err != nil {
		restore()
		return plugin.Errorf("step_failed", "could not read the effective sshd settings, the previous ones were restored: %v", err)
	}
	if eff["passwordauthentication"] != "no" || eff["permitrootlogin"] != c.Root || (len(c.AllowUsers) > 0 && eff["allowusers"] == "") {
		restore()
		e := plugin.Errorf("step_failed", "another sshd file overrides the settings (in effect: passwordauthentication=%s, permitrootlogin=%s); the previous settings were restored", eff["passwordauthentication"], eff["permitrootlogin"])
		e.Hint = "check sshd -T and the files in " + sshdDir + " and /etc/ssh/sshd_config"
		return e
	}
	// the service is called ssh (Debian, Ubuntu) or sshd; if neither is running (socket activation) there is nothing to reload:
	// the settings take effect at start
	for _, unit := range []string{"ssh", "sshd"} {
		if !s.ok("systemctl", "is-active", "--quiet", unit) {
			continue
		}
		if err := s.must("systemctl", "reload", unit); err != nil {
			restore()
			return plugin.Errorf("step_failed", "could not reload the sshd settings, the previous ones were restored: %v", err)
		}
		break
	}
	return nil
}

// waitStep is the pause between repeats of steps that wait for a service; replaced in tests.
var waitStep = time.Sleep

func (s *sec) fail2ban() error {
	if err := s.install("fail2ban"); err != nil {
		return err
	}
	jail := securityMark + "\n[DEFAULT]\nbantime = 1h\nfindtime = 10m\nmaxretry = 5\nignoreip = 127.0.0.1/8 ::1\n\n[sshd]\nenabled = true\nport = " + strconv.Itoa(s.sshPort()) + "\n"
	if err := s.env.FS.MkdirAll("/etc/fail2ban/jail.d", 0o755); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := s.env.FS.WriteFile(jailFile, []byte(jail), 0o644); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := s.must("systemctl", "enable", "fail2ban"); err != nil {
		return err
	}
	if err := s.must("systemctl", "restart", "fail2ban"); err != nil {
		return err
	}
	// after restart fail2ban brings up its socket not at once: wait up to 20 seconds
	var jailErr error
	for i := 0; i < 20; i++ {
		if jailErr = s.must("fail2ban-client", "status", "sshd"); jailErr == nil {
			break
		}
		waitStep(time.Second)
	}
	if err := jailErr; err != nil {
		return plugin.Errorf("step_failed", "fail2ban is running, but SSH protection (jail sshd) is not working: %v", err)
	}
	return nil
}

// setINI sets key = value in section of an ini-format file (the section and the key are created if missing).
func setINI(text, section, key, value string) string {
	lines := strings.Split(text, "\n")
	in, secAt := false, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			in = t == "["+section+"]"
			if in {
				secAt = i
			}
			continue
		}
		if k, _, ok := strings.Cut(t, "="); in && ok && strings.TrimSpace(k) == key {
			lines[i] = key + " = " + value
			return strings.Join(lines, "\n")
		}
	}
	if secAt >= 0 {
		out := append([]string{}, lines[:secAt+1]...)
		out = append(out, key+" = "+value)
		return strings.Join(append(out, lines[secAt+1:]...), "\n")
	}
	return strings.TrimRight(text, "\n") + "\n[" + section + "]\n" + key + " = " + value + "\n"
}

// updates turns on automatic installation of security updates; it returns an explanation if the package manager is not supported.
func (s *sec) updates() (string, error) {
	switch s.manager() {
	case "apt":
		if err := s.install("unattended-upgrades"); err != nil {
			return "", err
		}
		body := securityMark + "\nAPT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\nUnattended-Upgrade::Automatic-Reboot \"false\";\n"
		if err := s.env.FS.WriteFile(aptAutoFile, []byte(body), 0o644); err != nil {
			return "", plugin.Errorf("io_error", "%v", err)
		}
		return "", s.must("systemctl", "enable", "--now", "apt-daily.timer", "apt-daily-upgrade.timer")
	case "dnf":
		if err := s.install("dnf-automatic"); err != nil {
			return "", err
		}
		b, _ := s.env.FS.ReadFile(dnfAutoFile)
		t := setINI(string(b), "commands", "upgrade_type", "security")
		t = setINI(t, "commands", "apply_updates", "yes")
		if err := s.env.FS.WriteFile(dnfAutoFile, []byte(t), 0o644); err != nil {
			return "", plugin.Errorf("io_error", "%v", err)
		}
		return "", s.must("systemctl", "enable", "--now", "dnf-automatic-install.timer")
	}
	return "automatic updates are supported on apt and dnf; skipped on this system", nil
}

func (s *sec) sysctl() error {
	if err := s.env.FS.MkdirAll("/etc/sysctl.d", 0o755); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := s.env.FS.WriteFile(sysctlFile, []byte(sysctlBody), 0o644); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	return s.must("sysctl", "-e", "-p", sysctlFile)
}

func (Security) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	s := newSec(ctx, env)
	noRoot := func() (string, error) {
		switch {
		case len(args) == 0:
			return rootProhibitPw, nil
		case len(args) == 1 && args[0] == "no-root":
			return "no", nil
		}
		return "", plugin.Errorf("usage", "usage: lite security %s [no-root]", cmd)
	}
	switch cmd {
	case "status":
		fs := s.findings()
		return findingsOutput(fs), nil
	case "enable":
		root, err := noRoot()
		if err != nil {
			return plugin.Output{}, err
		}
		var done []string
		step := func(name string, f func() error) error {
			if err := f(); err != nil {
				var pe *plugin.Error
				if errors.As(err, &pe) {
					pe.Message = fmt.Sprintf("step \"%s\": %s (already done: %s)", name, pe.Message, strings.Join(append(done, "—"), ", "))
				}
				return err
			}
			done = append(done, name)
			return nil
		}
		for _, st := range []struct {
			n string
			f func() error
		}{
			{"fail2ban", s.fail2ban},
			{"automatic updates", func() error { _, err := s.updates(); return err }},
			{"kernel parameters", s.sysctl},
			{"SSH", func() error { c := s.readSSHConf(); c.Root = root; return s.applySSH(c) }},
		} {
			if err := step(st.n, st.f); err != nil {
				return plugin.Output{}, err
			}
		}
		return plugin.Output{Text: "Protection enabled: " + strings.Join(done, ", ") + ".\nNext: check that you can still log in with your key from a new terminal, then run: lite verify\n", Data: map[string]any{"steps": done}}, nil
	case "ssh":
		root, err := noRoot()
		if err != nil {
			return plugin.Output{}, err
		}
		c := s.readSSHConf()
		c.Root = root
		if err := s.applySSH(c); err != nil {
			return plugin.Output{}, err
		}
		return plugin.Output{Text: "SSH: passwords are disabled for everyone; root: " + root + ".\nKeep this session open, open a new one and check that key login works.\n", Data: map[string]any{"root": root}}, nil
	case "allow-users":
		if len(args) == 0 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite security allow-users NAME… | any")
		}
		c := s.readSSHConf()
		if len(args) == 1 && args[0] == "any" {
			c.AllowUsers = nil
		} else {
			known := map[string]bool{}
			for _, a := range s.accounts() {
				known[a.Name] = true
			}
			set := map[string]bool{}
			for _, u := range append(append([]string{}, args...), s.env.Projects...) {
				if !known[u] {
					if contains(args, u) {
						return plugin.Output{}, plugin.Errorf("usage", "no user %q", u)
					}
					continue
				}
				set[u] = true
			}
			c.AllowUsers = nil
			for u := range set {
				c.AllowUsers = append(c.AllowUsers, u)
			}
			sort.Strings(c.AllowUsers)
		}
		if err := s.applySSH(c); err != nil {
			return plugin.Output{}, err
		}
		text := "SSH: user restriction removed.\n"
		if len(c.AllowUsers) > 0 {
			text = "SSH: only these users may log in: " + strings.Join(c.AllowUsers, ", ") + " (project users were added automatically).\n"
		}
		return plugin.Output{Text: text, Data: map[string]any{"allow_users": c.AllowUsers}}, nil
	case "logins":
		return s.logins(args)
	case "banned":
		res, err := s.run("fail2ban-client", "status", "sshd")
		if err != nil || res.ExitCode != 0 {
			return plugin.Output{}, plugin.Errorf("precondition_failed", "fail2ban is not working: lite security fail2ban")
		}
		return plugin.Output{Text: string(res.Stdout), Data: map[string]any{"output": strings.TrimSpace(string(res.Stdout))}}, nil
	case "unban":
		if len(args) != 1 || net.ParseIP(args[0]) == nil {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite security unban ADDRESS")
		}
		if err := s.must("fail2ban-client", "set", "sshd", "unbanip", args[0]); err != nil {
			return plugin.Output{}, err
		}
		return plugin.Output{Text: "Lock released: " + args[0] + "\n", Data: map[string]any{"ip": args[0]}}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin security has no command %q", cmd)
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// logins: recent successful logins and failed attempts from the sshd journal.
func (s *sec) logins(args []string) (plugin.Output, error) {
	n := 20
	if len(args) > 1 {
		return plugin.Output{}, plugin.Errorf("usage", "usage: lite security logins [COUNT]")
	}
	if len(args) == 1 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 || v > 500 {
			return plugin.Output{}, plugin.Errorf("usage", "the number of lines is from 1 to 500")
		}
		n = v
	}
	var sb strings.Builder
	sb.WriteString("Recent logins:\n")
	ok := []string{}
	if res, err := s.run("last", "-n", strconv.Itoa(n), "-a", "-w"); err == nil && res.ExitCode == 0 {
		for _, l := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "wtmp begins") {
				ok = append(ok, l)
				sb.WriteString("  " + l + "\n")
			}
		}
	}
	sb.WriteString("Failed attempts (sshd journal):\n")
	bad := []string{}
	for _, unit := range []string{"ssh", "sshd"} {
		res, err := s.run("journalctl", "-u", unit, "--no-pager", "-n", "2000", "-o", "short-iso")
		if err != nil || res.ExitCode != 0 {
			continue
		}
		for _, l := range strings.Split(string(res.Stdout), "\n") {
			if strings.Contains(l, "Failed password") || strings.Contains(l, "Invalid user") || strings.Contains(l, "authentication failure") {
				bad = append(bad, strings.TrimSpace(l))
			}
		}
	}
	if len(bad) > n {
		bad = bad[len(bad)-n:]
	}
	for _, l := range bad {
		sb.WriteString("  " + l + "\n")
	}
	if len(bad) == 0 {
		sb.WriteString("  none\n")
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"logins": ok, "failed": bad}}, nil
}

func findingsOutput(fs []plugin.Finding) plugin.Output {
	var sb strings.Builder
	for _, f := range fs {
		fmt.Fprintf(&sb, "[%s] %s: %s\n", f.Level, f.Check, f.Message)
		if f.Fix != "" && f.Level != plugin.LevelOK {
			fmt.Fprintf(&sb, "      fix: %s\n", f.Fix)
		}
	}
	return plugin.Output{Text: sb.String(), Data: map[string]any{"findings": fs}}
}

func (s *sec) finding(check, level, msg, fix string) plugin.Finding {
	return plugin.Finding{Plugin: "security", Check: check, Level: level, Message: msg, Fix: fix}
}

func (s *sec) findings() []plugin.Finding {
	var out []plugin.Finding
	add := func(check, level, msg, fix string) { out = append(out, s.finding(check, level, msg, fix)) }

	// SSH
	if eff, err := s.sshdEffective(); err != nil {
		add("ssh", plugin.LevelWarn, "could not read the sshd settings: "+err.Error(), "")
	} else {
		switch eff["permitrootlogin"] {
		case "yes":
			add("ssh-root", plugin.LevelFail, "root can log in over SSH with a password", "lite security enable")
		case "no":
			add("ssh-root", plugin.LevelOK, "root cannot log in over SSH", "")
		default:
			add("ssh-root", plugin.LevelOK, "root can log in over SSH with a key only", "")
		}
		if eff["passwordauthentication"] == "yes" {
			add("ssh-password", plugin.LevelFail, "SSH password login is allowed", "lite security enable")
		} else {
			add("ssh-password", plugin.LevelOK, "SSH password login is disabled", "")
		}
		if eff["permitemptypasswords"] == "yes" {
			add("ssh-empty", plugin.LevelFail, "login with an empty password is allowed", "lite security enable")
		}
		if au := eff["allowusers"]; au != "" {
			add("ssh-allow", plugin.LevelOK, "SSH only for: "+au, "")
		} else {
			add("ssh-allow", plugin.LevelWarn, "the SSH user list is not restricted", "lite security allow-users NAME…")
		}
	}

	// fail2ban
	if res, err := s.run("fail2ban-client", "status", "sshd"); err != nil || res.ExitCode != 0 {
		add("fail2ban", plugin.LevelFail, "fail2ban does not protect SSH (not installed, not running, or no sshd jail)", "lite security enable")
	} else {
		add("fail2ban", plugin.LevelOK, "fail2ban protects SSH", "")
	}

	// automatic updates and pending security updates
	switch s.manager() {
	case "apt":
		if res, err := s.run("apt-config", "dump"); err == nil && strings.Contains(string(res.Stdout), `APT::Periodic::Unattended-Upgrade "1"`) {
			add("auto-updates", plugin.LevelOK, "security updates are installed automatically", "")
		} else {
			add("auto-updates", plugin.LevelWarn, "automatic security updates are off", "lite security enable")
		}
		if res, err := s.run("apt-get", "-s", "upgrade"); err == nil && res.ExitCode == 0 {
			n := 0
			for _, l := range strings.Split(string(res.Stdout), "\n") {
				if strings.HasPrefix(l, "Inst ") && strings.Contains(strings.ToLower(l), "security") {
					n++
				}
			}
			if n > 0 {
				add("pending-updates", plugin.LevelWarn, fmt.Sprintf("security updates waiting to be installed: %d", n), "sudo apt-get upgrade")
			} else {
				add("pending-updates", plugin.LevelOK, "no security updates are waiting", "")
			}
		}
	case "dnf":
		if _, err := s.env.FS.ReadFile(dnfAutoFile); err == nil {
			if res, err := s.run("systemctl", "is-enabled", "dnf-automatic-install.timer"); err == nil && res.ExitCode == 0 {
				add("auto-updates", plugin.LevelOK, "security updates are installed automatically", "")
				break
			}
		}
		add("auto-updates", plugin.LevelWarn, "automatic security updates are off", "lite security enable")
	}

	// kernel parameters
	var bad []string
	for _, kv := range sysctlChecks {
		if res, err := s.run("sysctl", "-n", kv[0]); err == nil && res.ExitCode == 0 && strings.TrimSpace(string(res.Stdout)) != kv[1] {
			bad = append(bad, kv[0])
		}
	}
	if len(bad) > 0 {
		add("sysctl", plugin.LevelWarn, "protective kernel parameters are not applied: "+strings.Join(bad, ", "), "lite security enable")
	} else {
		add("sysctl", plugin.LevelOK, "protective kernel parameters are applied", "")
	}

	// administrators
	if adm := s.admins(); len(adm) == 0 {
		add("admins", plugin.LevelWarn, "the sudo and wheel groups are empty", "lite init --user NAME")
	} else {
		var nokey []string
		for _, a := range adm {
			if !s.hasKey(a) {
				nokey = append(nokey, a)
			}
		}
		if len(nokey) > 0 {
			add("admins", plugin.LevelWarn, "administrators (sudo): "+strings.Join(adm, ", ")+"; without an SSH key: "+strings.Join(nokey, ", "), "")
		} else {
			add("admins", plugin.LevelOK, "administrators (sudo): "+strings.Join(adm, ", "), "")
		}
	}

	// ports that listen to the outside
	out = append(out, s.portFindings()...)
	return out
}

// portFindings are ports that listen on all interfaces, except SSH and the web ports.
func (s *sec) portFindings() []plugin.Finding {
	res, err := s.run("ss", "-Htuln")
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	ssh := s.sshPort()
	skipUDP := map[int]bool{68: true, 123: true, 323: true, 443: true, 5353: true}
	seen := map[string]bool{}
	var open []string
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		addr := f[4]
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			continue
		}
		hostPart, port := addr[:i], addr[i+1:]
		if hostPart != "0.0.0.0" && hostPart != "*" && hostPart != "[::]" && hostPart != "::" {
			continue
		}
		n, err := strconv.Atoi(port)
		if err != nil || n == ssh || n == 80 || (n == 443 && f[0] == "tcp") {
			continue
		}
		proto := f[0]
		if proto == "udp" && (skipUDP[n] || n >= 32768) { // 32768 and above: ephemeral client ports
			continue
		}
		k := port + "/" + proto
		if !seen[k] {
			seen[k] = true
			open = append(open, k)
		}
	}
	if len(open) == 0 {
		return []plugin.Finding{s.finding("ports", plugin.LevelOK, "only SSH and web ports listen to the outside", "")}
	}
	sort.Strings(open)
	st, _ := (&firewall.Manager{Runner: s.env.Runner}).Status(s.ctx)
	if st == nil || !st.Active {
		return []plugin.Finding{s.finding("ports", plugin.LevelFail, "listening on all interfaces and not closed by the firewall: "+strings.Join(open, ", "), "lite firewall enable (or mode max in lite init --ufw max)")}
	}
	var exposed []string
	for _, k := range open {
		p, proto, _ := strings.Cut(k, "/")
		n, _ := strconv.Atoi(p)
		for _, r := range st.Rules {
			if r.Port == n && (r.Proto == "" || r.Proto == proto) && r.Action == "allow" {
				exposed = append(exposed, k)
				break
			}
		}
	}
	if len(exposed) > 0 {
		return []plugin.Finding{s.finding("ports", plugin.LevelWarn, "extra ports are open to the outside: "+strings.Join(exposed, ", "), "lite firewall remove PORT --yes")}
	}
	return []plugin.Finding{s.finding("ports", plugin.LevelOK, "extra ports are listening but the firewall closes them: "+strings.Join(open, ", "), "")}
}

func (Security) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	return newSec(ctx, env).findings()
}
