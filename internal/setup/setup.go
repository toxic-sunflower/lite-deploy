// Package setup prepares the server for work (`lite init`): administrator users, their SSH keys,
// SSH login by key only, the firewall mode, the plugin packages. The program neither sees nor stores passwords:
// they are set by the system passwd command.
package setup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/agent"
	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/pkgmgr"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// SSHDropIn is the sshd settings file managed by lite.
const SSHDropIn = "/etc/ssh/sshd_config.d/50-lite-deploy.conf"

const dropInMark = "# managed-by: lite-deploy (do not edit by hand: lite init rewrites this file)"

var (
	userRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	keyRe  = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/]{32,}={0,3}( [^\n\r]*)?$`)
)

// Options is what to do.
type Options struct {
	Users    []string // names; empty and !AllUsers: the current user
	AllUsers bool
	Current  string   // who started sudo (empty: run directly as root)
	Firewall string   // off, http, max or keep (do not change)
	Plugins  []string // podman, postgres
	Yes      bool     // do not ask for confirmation of the plan
	// WaitLogin: before closing password login, wait until the person has really logged in with the key (the SSH log).
	WaitLogin    bool
	LoginTimeout time.Duration // how long to wait; 5 minutes by default
}

// Env is the environment.
type Env struct {
	Runner host.Runner
	FS     host.FS
	In     *bufio.Reader
	Out    io.Writer
	// Projects are the names of project users: init does not touch them.
	Projects []string
	Sleep    func(time.Duration) // for waiting for the key login
	Now      func() time.Time
}

// Account is an /etc/passwd entry.
type Account struct {
	Name, Home, Shell string
	UID               int
}

func (e Env) accounts() ([]Account, error) {
	b, err := e.FS.ReadFile("/etc/passwd")
	if err != nil {
		return nil, err
	}
	var out []Account
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) < 7 {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		out = append(out, Account{Name: f[0], UID: uid, Home: f[5], Shell: f[6]})
	}
	return out, nil
}

func hasShell(a Account) bool {
	return a.Shell != "" && !strings.HasSuffix(a.Shell, "/nologin") && !strings.HasSuffix(a.Shell, "/false")
}

// ResolveUsers decides whom to configure.
func (e Env) ResolveUsers(o Options) ([]string, error) {
	accs, err := e.accounts()
	if err != nil {
		return nil, err
	}
	project := map[string]bool{}
	for _, p := range e.Projects {
		project[p] = true
	}
	var names []string
	switch {
	case o.AllUsers:
		for _, a := range accs {
			if a.UID >= 1000 && a.UID < 65000 && hasShell(a) && !project[a.Name] {
				names = append(names, a.Name)
			}
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("there are no regular users with a shell: pass --user NAME, it will be created")
		}
	case len(o.Users) > 0:
		names = o.Users
	case o.Current == "" || o.Current == "root":
		return nil, fmt.Errorf("when running as root, name a user: lite init --user NAME (existing or new)")
	default:
		names = []string{o.Current}
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !userRe.MatchString(n) || n == "root" {
			return nil, fmt.Errorf("invalid user name: %q", n)
		}
		if project[n] {
			return nil, fmt.Errorf("%s is a project user and cannot be an administrator", n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

func (e Env) run(ctx context.Context, name string, args ...string) (host.Result, error) {
	return e.Runner.Run(ctx, host.Cmd{Name: name, Args: args})
}

func (e Env) must(ctx context.Context, name string, args ...string) error {
	res, err := e.run(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(res.Stderr)+" "+string(res.Stdout)))
	}
	return nil
}

func (e Env) say(format string, a ...any) { _, _ = fmt.Fprintf(e.Out, format+"\n", a...) }

func (e Env) ask(prompt string) (string, error) {
	_, _ = fmt.Fprint(e.Out, prompt)
	if e.In == nil {
		return "", io.EOF
	}
	l, err := e.In.ReadString('\n')
	if err != nil && l == "" {
		return "", err
	}
	return strings.TrimSpace(l), nil
}

// Plan is a human-readable list of what will be done.
func (e Env) Plan(o Options, users []string) ([]string, error) {
	accs, err := e.accounts()
	if err != nil {
		return nil, err
	}
	exists := map[string]bool{}
	for _, a := range accs {
		exists[a.Name] = true
	}
	var p []string
	for _, u := range users {
		if exists[u] {
			p = append(p, "user "+u+": exists; administrator rights (sudo), SSH key, SSH login by key only")
		} else {
			p = append(p, "user "+u+": will be created; password (passwd will ask), administrator rights (sudo), SSH key, SSH login by key only")
		}
	}
	switch o.Firewall {
	case "keep":
		p = append(p, "firewall: leave unchanged (keep what was set up by hand)")
	case firewall.ModeOff:
		p = append(p, "firewall: turn off (no traffic filtering)")
	case firewall.ModeHTTP:
		p = append(p, "firewall: reset the rules, open only SSH and the web ports (80/tcp, 443/tcp, 443/udp)")
	case firewall.ModeMax:
		p = append(p, "firewall: reset the rules, open SSH only (sites on 80 and 443 are unreachable from outside until the mode is changed to http)")
	default:
		return nil, fmt.Errorf("firewall mode: keep, off, http or max")
	}
	for _, pl := range o.Plugins {
		switch pl {
		case "caddy":
			p = append(p, "plugin caddy: install Caddy, the main Caddyfile (serves the project sites from /etc/caddy/conf.d), start the service")
		case "security":
			p = append(p, "plugin security: server protection: SSH by key only for everyone and root by key only, fail2ban, automatic security updates, protective kernel parameters")
		case "podman", "pg", "postgres", "redis", "backblaze", "s3", "sftp":
			p = append(p, "plugin "+pl+": install packages")
		default:
			return nil, fmt.Errorf("unknown plugin %q (available: %s)", pl, strings.Join(builtin.PluginNames(), ", "))
		}
	}
	return p, nil
}

// Apply performs the setup. The order is chosen so as not to lose access: users and keys first,
// then the firewall (the SSH rule before enabling), and last the ban on password login (with an sshd configuration check).
func (e Env) Apply(ctx context.Context, o Options) error {
	users, err := e.ResolveUsers(o)
	if err != nil {
		return err
	}
	plan, err := e.Plan(o, users)
	if err != nil {
		return err
	}
	e.say("Will do:")
	for _, l := range plan {
		e.say("  - %s", l)
	}
	if !o.Yes {
		a, err := e.ask("Continue? [y/N] ")
		if a = strings.ToLower(a); err != nil || (a != "y" && a != "yes") {
			return fmt.Errorf("canceled, nothing was changed")
		}
	}
	e.snapshot(ctx)
	group, err := agent.AdminGroup(ctx, e.Runner)
	if err != nil {
		return err
	}
	var keyed []string
	for _, u := range users {
		if err := e.user(ctx, u, group); err != nil {
			return fmt.Errorf("user %s: %w", u, err)
		}
		keyed = append(keyed, u)
	}
	protect := true
	if o.WaitLogin {
		protect = e.waitKeyLogin(ctx, keyed, o)
		if !protect {
			var rest []string
			for _, pl := range o.Plugins {
				if pl != "security" {
					rest = append(rest, pl)
				}
			}
			o.Plugins = rest
		}
	}
	for _, pl := range o.Plugins {
		penv := plugin.Env{Runner: e.Runner, FS: e.FS, Projects: e.Projects, User: o.Current}
		if err := builtin.InstallPlugin(ctx, penv, pl, builtin.Say(e.say)); err != nil {
			return fmt.Errorf("plugin %s: %w", pl, err)
		}
	}
	if o.Firewall != "keep" {
		if err := e.firewall(ctx, o.Firewall); err != nil {
			return err
		}
	}
	if protect {
		if err := e.sshKeyOnly(ctx, keyed); err != nil {
			return err
		}
	}
	e.say("")
	if !protect {
		e.say("Done, but the server is NOT protected yet: password login is still on, because no login with your key was seen.")
		e.say("Log in with your key (ssh %s@this-server), then run: lite security enable", users[0])
		return nil
	}
	hardened := false
	for _, pl := range o.Plugins {
		hardened = hardened || pl == "security"
	}
	if hardened {
		e.say("The server is protected: SSH by key only (root too), fail2ban, automatic security updates. Check with: lite verify")
	} else {
		e.say("Root SSH login was not changed: whether to disable it is up to you.")
	}
	return nil
}

// user creates or finishes configuring one user.
func (e Env) user(ctx context.Context, name, group string) error {
	accs, err := e.accounts()
	if err != nil {
		return err
	}
	var acc *Account
	for i := range accs {
		if accs[i].Name == name {
			acc = &accs[i]
		}
	}
	if acc == nil {
		e.say("Creating user %s.", name)
		if err := e.must(ctx, "useradd", "--create-home", "--shell", "/bin/bash", name); err != nil {
			return err
		}
		acc = &Account{Name: name, Home: "/home/" + name}
		if accs2, err := e.accounts(); err == nil {
			for i := range accs2 {
				if accs2[i].Name == name {
					acc = &accs2[i]
				}
			}
		}
	}
	// the password is needed for sudo; the person sets it in the system passwd command
	res, err := e.run(ctx, "passwd", "-S", name)
	if err != nil {
		return err
	}
	f := strings.Fields(string(res.Stdout))
	if len(f) < 2 || (f[1] != "P" && f[1] != "PS") {
		e.say("User %s has no password (sudo needs one). Set it now:", name)
		r, err := e.Runner.Run(ctx, host.Cmd{Name: "passwd", Args: []string{name}, Interactive: true, Timeout: 10 * time.Minute})
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("no password set")
		}
	}
	if err := e.must(ctx, "usermod", "-aG", group, name); err != nil {
		return err
	}
	return e.key(name, acc.Home)
}

// key asks for a public SSH key and adds it to authorized_keys. Without a key init does not continue.
func (e Env) key(name, home string) error {
	dir, file := home+"/.ssh", home+"/.ssh/authorized_keys"
	old, _ := e.FS.ReadFile(file)
	have := 0
	for _, l := range strings.Split(string(old), "\n") {
		if keyRe.MatchString(strings.TrimSpace(l)) {
			have++
		}
	}
	prompt := fmt.Sprintf("Public SSH key for %s (a line like ssh-ed25519 AAAA…): ", name)
	if have > 0 {
		prompt = fmt.Sprintf("Public SSH key for %s (already has %d keys; press Enter to keep as is): ", name, have)
	}
	var key string
	for try := 0; ; try++ {
		k, err := e.ask(prompt)
		if err != nil && have > 0 {
			return nil // there is no input (run without a terminal), keys already exist
		}
		if err != nil {
			return fmt.Errorf("no key entered: password login cannot be disabled without an SSH key")
		}
		if k == "" && have > 0 {
			return nil
		}
		if keyRe.MatchString(k) {
			key = k
			break
		}
		if try >= 2 {
			return fmt.Errorf("that is not a public SSH key; it is on your computer in ~/.ssh/id_ed25519.pub")
		}
		e.say("That does not look like a public SSH key. It is on your computer in ~/.ssh/id_ed25519.pub.")
	}
	body := strings.Fields(key)[1]
	if strings.Contains(string(old), body) {
		return nil
	}
	if err := e.FS.MkdirAll(dir, fs.FileMode(0o700)); err != nil {
		return err
	}
	text := string(old)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := e.FS.WriteFile(file, []byte(text+key+"\n"), fs.FileMode(0o600)); err != nil {
		return err
	}
	for _, p := range []string{dir, file} {
		if err := e.FS.Chown(p, name, ""); err != nil {
			return err
		}
	}
	return e.FS.Chmod(dir, fs.FileMode(0o700))
}

func (e Env) firewall(ctx context.Context, mode string) error {
	m := &firewall.Manager{Runner: e.Runner}
	if _, err := e.run(ctx, "ufw", "version"); err != nil {
		if mode == firewall.ModeOff {
			return nil // there is no ufw, so there is no filtering anyway
		}
		e.say("Installing ufw...")
		if err := (&pkgmgr.Manager{Runner: e.Runner, FS: e.FS}).Install(ctx, "ufw"); err != nil {
			return fmt.Errorf("could not install ufw: %w", err)
		}
	}
	e.say("Firewall: mode %s (SSH port %d).", mode, m.SSHPort(ctx))
	if fe := m.SetMode(ctx, mode); fe != nil {
		return fmt.Errorf("firewall: %s", fe.Message)
	}
	return nil
}

// dropInUsers reads the users from the previous lite file, so that a repeated init does not lose them.
func dropInUsers(old string) []string {
	for _, l := range strings.Split(old, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "Match User "); ok {
			return strings.Split(strings.TrimSpace(rest), ",")
		}
	}
	return nil
}

// sshKeyOnly forbids SSH password login for the listed users (each already has a key).
// The configuration is checked with `sshd -t`; on an error the previous file is restored.
func (e Env) sshKeyOnly(ctx context.Context, users []string) error {
	oldB, oldErr := e.FS.ReadFile(SSHDropIn)
	set := map[string]bool{}
	for _, u := range append(dropInUsers(string(oldB)), users...) {
		if userRe.MatchString(u) {
			set[u] = true
		}
	}
	var all []string
	for u := range set {
		all = append(all, u)
	}
	sort.Strings(all)
	body := dropInMark + "\nMatch User " + strings.Join(all, ",") + "\n    PasswordAuthentication no\n    KbdInteractiveAuthentication no\n"
	if string(oldB) == body {
		return nil
	}
	if main, err := e.FS.ReadFile("/etc/ssh/sshd_config"); err == nil && !strings.Contains(string(main), "sshd_config.d") {
		return fmt.Errorf("/etc/ssh/sshd_config has no Include /etc/ssh/sshd_config.d/*.conf line: password login was not changed")
	}
	if err := e.FS.MkdirAll("/etc/ssh/sshd_config.d", fs.FileMode(0o755)); err != nil {
		return err
	}
	if err := e.FS.WriteFile(SSHDropIn, []byte(body), fs.FileMode(0o644)); err != nil {
		return err
	}
	restore := func() {
		if oldErr == nil {
			_ = e.FS.WriteFile(SSHDropIn, oldB, fs.FileMode(0o644))
		} else {
			_ = e.FS.Remove(SSHDropIn)
		}
	}
	if err := e.must(ctx, "sshd", "-t"); err != nil {
		restore()
		return fmt.Errorf("the new sshd configuration failed validation, the previous one was restored: %w", err)
	}
	for _, unit := range []string{"ssh", "sshd"} {
		if e.must(ctx, "systemctl", "is-active", "--quiet", unit) != nil {
			continue
		}
		if err := e.must(ctx, "systemctl", "reload", unit); err != nil {
			restore()
			return fmt.Errorf("could not reload the sshd settings, the previous one was restored: %w", err)
		}
		break
	}
	e.say("SSH password login is disabled for: %s (key login remains).", strings.Join(users, ", "))
	return nil
}

var acceptedRe = regexp.MustCompile(`Accepted publickey for ([a-z_][a-z0-9_-]*) from`)

// waitKeyLogin waits until one of the users logs in with the key, so that password login is closed only after the key
// is proven to work. It returns false if no login is seen in time; true if one is seen, or if the SSH log cannot be read
// (then nothing can be verified and the earlier behavior is kept).
func (e Env) waitKeyLogin(ctx context.Context, users []string, o Options) bool {
	sleep, now := e.Sleep, e.Now
	if sleep == nil {
		sleep = time.Sleep
	}
	if now == nil {
		now = time.Now
	}
	limit := o.LoginTimeout
	if limit == 0 {
		limit = 5 * time.Minute
	}
	start := now()
	want := map[string]bool{}
	for _, u := range users {
		want[u] = true
	}
	e.say("")
	e.say("Before closing password login, please check that your key works.")
	e.say("Open a NEW terminal on your workstation and log in: ssh %s@this-server", users[0])
	e.say("Waiting for that login (up to %d minutes)...", int(limit.Minutes()))
	for {
		res, err := e.Runner.Run(ctx, host.Cmd{Name: "journalctl", Args: []string{"-q", "--no-pager", "-o", "cat", "-u", "ssh", "-u", "sshd", "--since", "@" + strconv.FormatInt(start.Unix()-1, 10)}, Timeout: 20 * time.Second})
		if err != nil || res.ExitCode != 0 {
			e.say("Could not read the SSH log, so the login cannot be checked automatically: check key login yourself in a second window.")
			return true
		}
		for _, m := range acceptedRe.FindAllStringSubmatch(string(res.Stdout), -1) {
			if want[m[1]] {
				e.say("Key login of %s seen. Closing password login.", m[1])
				return true
			}
		}
		if now().Sub(start) >= limit || ctx.Err() != nil {
			e.say("No login with your key was seen in %d minutes.", int(limit.Minutes()))
			return false
		}
		sleep(3 * time.Second)
	}
}

// OriginalDir holds the state of the server as it was before the first init; liteup uninstall --everything puts it back.
const OriginalDir = "/var/lib/lite-deploy/original"

// snapshot saves the original SSH login settings and the firewall rules, once: a later init sees a server that
// lite has already changed, which is not the original. A failure is not fatal: the uninstall then falls back to defaults.
func (e Env) snapshot(ctx context.Context) {
	if _, err := e.FS.Stat(OriginalDir + "/saved"); err == nil {
		return
	}
	if err := e.FS.MkdirAll(OriginalDir+"/ufw", fs.FileMode(0o700)); err != nil {
		return
	}
	if res, err := e.Runner.Run(ctx, host.Cmd{Name: "sshd", Args: []string{"-T"}, Timeout: 20 * time.Second}); err == nil && res.ExitCode == 0 {
		root, pass := "", ""
		for _, l := range strings.Split(string(res.Stdout), "\n") {
			f := strings.Fields(l)
			if len(f) == 2 && f[0] == "permitrootlogin" {
				root = f[1]
			}
			if len(f) == 2 && f[0] == "passwordauthentication" {
				pass = f[1]
			}
		}
		if root != "" && pass != "" {
			_ = e.FS.WriteFile(OriginalDir+"/ssh", []byte("PermitRootLogin "+root+"\nPasswordAuthentication "+pass+"\n"), fs.FileMode(0o600))
		}
	}
	for _, f := range []string{"ufw.conf", "user.rules", "user6.rules"} {
		if b, err := e.FS.ReadFile("/etc/ufw/" + f); err == nil {
			_ = e.FS.WriteFile(OriginalDir+"/ufw/"+f, b, fs.FileMode(0o600))
		}
	}
	_ = e.FS.WriteFile(OriginalDir+"/saved", []byte("1\n"), fs.FileMode(0o600))
}

// ValidKey tells whether the line is a single public SSH key without options.
func ValidKey(line string) bool { return keyRe.MatchString(strings.TrimSpace(line)) }
