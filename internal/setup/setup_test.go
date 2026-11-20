package setup

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const caddyMark = "# managed-by: lite-deploy"

const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake me@laptop"

type world struct {
	fs      *host.MemFS
	calls   []string
	out     bytes.Buffer
	noPass  map[string]bool // who has no password
	failOn  string
	noUfw   bool
	created []string
	pkgs    map[string]bool // installed packages
	journal string          // what journalctl prints (the SSH log)
}

func newWorld() *world {
	m := host.NewMemFS()
	m.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\nanna:x:1000:1000::/home/anna:/bin/bash\nshop:x:1001:1001::/home/shop:/bin/bash\nwww:x:33:33::/var/www:/usr/sbin/nologin\nsvc:x:1002:1002::/home/svc:/bin/false\n")
	m.Files["/etc/ssh/sshd_config"] = []byte("Include /etc/ssh/sshd_config.d/*.conf\n")
	return &world{fs: m, noPass: map[string]bool{}, pkgs: map[string]bool{}}
}

func (w *world) env(input string) Env {
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		w.calls = append(w.calls, line)
		if w.failOn != "" && strings.HasPrefix(line, w.failOn) {
			return host.Result{ExitCode: 1, Stderr: []byte("a failure")}, nil
		}
		switch {
		case c.Name == "apt-get" && len(c.Args) > 0 && c.Args[0] == "install":
			for _, a := range c.Args[1:] {
				if !strings.HasPrefix(a, "-") {
					w.pkgs[a] = true
				}
			}
		case c.Name == "dpkg-query":
			if w.pkgs[c.Args[len(c.Args)-1]] {
				return host.Result{Stdout: []byte("install ok installed|1.0")}, nil
			}
			return host.Result{ExitCode: 1}, nil
		case c.Name == "ufw" && w.noUfw && line == "ufw version":
			return host.Result{}, context.DeadlineExceeded
		case c.Name == "getent":
			if strings.HasSuffix(line, " sudo") {
				return host.Result{}, nil
			}
			return host.Result{ExitCode: 2}, nil
		case line == "sshd -T":
			return host.Result{Stdout: []byte("port 2222\n")}, nil
		case c.Name == "journalctl":
			return host.Result{Stdout: []byte(w.journal)}, nil
		case c.Name == "gpg":
			w.fs.Files[c.Args[len(c.Args)-1]] = c.Stdin // the real gpg creates the key file
		case c.Name == "useradd":
			name := c.Args[len(c.Args)-1]
			w.created = append(w.created, name)
			w.fs.Files["/etc/passwd"] = append(w.fs.Files["/etc/passwd"], []byte(name+":x:1100:1100::/home/"+name+":/bin/bash\n")...)
			w.noPass[name] = true
		case c.Name == "passwd" && len(c.Args) == 2 && c.Args[0] == "-S":
			if w.noPass[c.Args[1]] {
				return host.Result{Stdout: []byte(c.Args[1] + " L 2026-01-01\n")}, nil
			}
			return host.Result{Stdout: []byte(c.Args[1] + " P 2026-01-01\n")}, nil
		case c.Name == "passwd" && c.Interactive:
			w.noPass[c.Args[0]] = false
		}
		return host.Result{}, nil
	}}
	return Env{Runner: r, FS: w.fs, In: bufio.NewReader(strings.NewReader(input)), Out: &w.out, Projects: []string{"shop"}}
}

func (w *world) has(t *testing.T, want ...string) {
	t.Helper()
	joined := strings.Join(w.calls, "\n")
	for _, x := range want {
		if !strings.Contains(joined, x) {
			t.Errorf("no call %q in\n%s", x, joined)
		}
	}
}

func (w *world) index(prefix string) int {
	for i, c := range w.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func TestNewUserFullFlow(t *testing.T) {
	w := newWorld()
	err := w.env("y\n"+key+"\n").Apply(context.Background(), Options{Users: []string{"serafim"}, Firewall: "max"})
	if err != nil {
		t.Fatal(err, w.out.String())
	}
	w.has(t, "useradd --create-home --shell /bin/bash serafim", "passwd serafim", "usermod -aG sudo serafim",
		"ufw --force reset", "ufw limit 2222/tcp", "ufw --force enable", "sshd -t", "systemctl reload ssh")
	if strings.Contains(strings.Join(w.calls, "\n"), "ufw allow 80/tcp") {
		t.Error("in mode max the web ports are not opened")
	}
	if got := string(w.fs.Files["/home/serafim/.ssh/authorized_keys"]); got != key+"\n" {
		t.Fatalf("authorized_keys: %q", got)
	}
	if w.fs.Perms["/home/serafim/.ssh/authorized_keys"] != 0o600 || w.fs.Owners["/home/serafim/.ssh/authorized_keys"][0] != "serafim" {
		t.Errorf("key permissions: %v %v", w.fs.Perms, w.fs.Owners)
	}
	drop := string(w.fs.Files[SSHDropIn])
	if !strings.Contains(drop, "Match User serafim\n") || !strings.Contains(drop, "PasswordAuthentication no") {
		t.Fatalf("sshd: %q", drop)
	}
	// order: the SSH rule before enabling the firewall; the password ban after the key and the firewall
	if w.index("ufw limit") >= w.index("ufw --force enable") || w.index("ufw --force enable") >= w.index("sshd -t") {
		t.Fatalf("order: %v", w.calls)
	}
}

func TestExistingUserKeepsKeysAndPassword(t *testing.T) {
	w := newWorld()
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	if err := w.env("y\n\n").Apply(context.Background(), Options{Current: "anna", Firewall: "http"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(w.calls, "\n")
	if strings.Contains(joined, "useradd") || strings.Contains(joined, "passwd anna\n") {
		t.Fatalf("an existing user with a password is not touched: %s", joined)
	}
	w.has(t, "usermod -aG sudo anna", "ufw allow 80/tcp", "ufw allow 443/tcp", "ufw allow 443/udp")
	if string(w.fs.Files["/home/anna/.ssh/authorized_keys"]) != key+"\n" {
		t.Fatal("the keys changed")
	}
}

func TestKeyIsMandatory(t *testing.T) {
	w := newWorld()
	err := w.env("y\nnot a key\nneither is this\nssh-rsa short\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "max"})
	if err == nil {
		t.Fatal("without a key init must stop")
	}
	if _, ok := w.fs.Files[SSHDropIn]; ok || w.index("ufw --force") >= 0 {
		t.Fatalf("without a key neither the firewall nor sshd is changed: %v", w.calls)
	}
	w = newWorld()
	if err := w.env("y\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "max"}); err == nil {
		t.Fatal("no input and no keys: a refusal")
	}
}

func TestRootNeedsUser(t *testing.T) {
	for _, cur := range []string{"", "root"} {
		w := newWorld()
		if err := w.env("y\n").Apply(context.Background(), Options{Current: cur, Firewall: "max"}); err == nil || !strings.Contains(err.Error(), "--user") {
			t.Fatalf("%q: %v", cur, err)
		}
		if len(w.calls) != 0 {
			t.Fatalf("nothing may run: %v", w.calls)
		}
	}
}

func TestAllUsers(t *testing.T) {
	w := newWorld()
	got, err := w.env("").ResolveUsers(Options{AllUsers: true})
	if err != nil || strings.Join(got, ",") != "anna" {
		t.Fatalf("only people with a shell, no projects and no system users: %v %v", got, err)
	}
}

func TestRejections(t *testing.T) {
	w := newWorld()
	e := w.env("")
	for _, o := range []Options{{Users: []string{"shop"}}, {Users: []string{"root"}}, {Users: []string{"Bad Name"}}, {Users: []string{"a;b"}}} {
		if _, err := e.ResolveUsers(o); err == nil {
			t.Errorf("%v must be rejected", o.Users)
		}
	}
	if err := w.env("y\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "wide"}); err == nil {
		t.Error("unknown firewall mode")
	}
	if err := w.env("y\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "max", Plugins: []string{"mysql"}}); err == nil {
		t.Error("unknown plugin")
	}
	if len(w.calls) != 0 {
		t.Fatalf("on wrong parameters nothing is executed: %v", w.calls)
	}
}

func TestDeclinedPlanChangesNothing(t *testing.T) {
	w := newWorld()
	if err := w.env("n\n").Apply(context.Background(), Options{Users: []string{"serafim"}, Firewall: "max"}); err == nil {
		t.Fatal("a refusal must stop")
	}
	if len(w.calls) != 0 || len(w.created) != 0 {
		t.Fatalf("after a refusal nothing changes: %v", w.calls)
	}
	if !strings.Contains(w.out.String(), "will be created") {
		t.Fatalf("the plan must be shown: %s", w.out.String())
	}
}

func TestSSHDFailureRestores(t *testing.T) {
	w := newWorld()
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	w.failOn = "sshd -t"
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true}); err == nil {
		t.Fatal("an sshd validation error must stop")
	}
	if _, ok := w.fs.Files[SSHDropIn]; ok {
		t.Fatal("an unchecked sshd file must be removed")
	}
	if w.index("systemctl reload") >= 0 {
		t.Fatal("sshd is not reloaded when the check fails")
	}
}

func TestRerunKeepsEarlierUsers(t *testing.T) {
	w := newWorld()
	w.fs.Files[SSHDropIn] = []byte(dropInMark + "\nMatch User bob\n    PasswordAuthentication no\n    KbdInteractiveAuthentication no\n")
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(w.fs.Files[SSHDropIn]), "Match User anna,bob\n") {
		t.Fatalf("%q", w.fs.Files[SSHDropIn])
	}
}

func TestFirewallOffAndInstall(t *testing.T) {
	w := newWorld()
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	w.noUfw = true
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true}); err != nil {
		t.Fatal(err)
	}
	if w.index("apt-get install") >= 0 || w.index("ufw disable") >= 0 {
		t.Fatalf("there is no ufw and the mode is off: nothing to install or touch: %v", w.calls)
	}
	w = newWorld()
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	w.noUfw = true
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "max", Plugins: []string{"podman"}, Yes: true}); err != nil {
		t.Fatal(err)
	}
	w.has(t, "apt-get install -y -qq ufw", "apt-get install -y -qq podman uidmap")
}

func TestNoIncludeInSSHDConfig(t *testing.T) {
	w := newWorld()
	w.fs.Files["/etc/ssh/sshd_config"] = []byte("Port 22\n")
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true}); err == nil {
		t.Fatal("without Include the setting would not take effect: this must be reported")
	}
}

func TestCaddyPluginInstallsAndConfigures(t *testing.T) {
	w := newWorld()
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	w.fs.Files["/etc/caddy/Caddyfile"] = []byte(":80 { respond \"old\" }\n")
	w.failOn = "caddy version"
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Plugins: []string{"caddy"}, Yes: true}); err != nil {
		t.Fatal(err, w.out.String())
	}
	w.has(t, "apt-get install -y -qq debian-keyring", "curl -fsSL --proto =https --tlsv1.2 https://dl.cloudsmith.io/public/caddy/stable/gpg.key",
		"gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg", "apt-get install -y -qq caddy",
		"caddy validate --config /etc/caddy/Caddyfile", "systemctl enable --now caddy", "systemctl reload caddy")
	if got := string(w.fs.Files["/etc/caddy/Caddyfile"]); !strings.Contains(got, caddyMark) || !strings.Contains(got, "import /etc/caddy/conf.d/*.caddy") {
		t.Fatalf("Caddyfile: %q", got)
	}
	backup := false
	for name, body := range w.fs.Files {
		if strings.HasPrefix(name, "/etc/caddy/Caddyfile.bak.") && strings.Contains(string(body), "old") {
			backup = true
		}
	}
	if !backup {
		t.Fatal("the previous Caddyfile must be kept")
	}
	if _, ok := w.fs.Files["/etc/caddy/conf.d/00-placeholder.caddy"]; !ok {
		t.Fatal("no conf.d placeholder")
	}
	if w.index("systemctl enable --now caddy") < w.index("caddy validate") {
		t.Fatal("the service must not be started before the Caddyfile is checked")
	}
}

func TestCaddyAlreadyInstalledKeepsOurCaddyfile(t *testing.T) {
	w := newWorld()
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	own := caddyMark + "\nimport /etc/caddy/conf.d/*.caddy\n"
	w.fs.Files["/etc/caddy/Caddyfile"] = []byte(own)
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Plugins: []string{"caddy"}, Yes: true}); err != nil {
		t.Fatal(err)
	}
	if w.index("curl") >= 0 || w.index("apt-get install") >= 0 {
		t.Fatalf("Caddy is already there: nothing to install: %v", w.calls)
	}
	if string(w.fs.Files["/etc/caddy/Caddyfile"]) != own {
		t.Fatal("a custom Caddyfile is not rewritten")
	}
	for name := range w.fs.Files {
		if strings.Contains(name, "Caddyfile.bak") {
			t.Fatal("no backup is needed")
		}
	}
}

func TestCaddyValidateFailureStopsBeforeStart(t *testing.T) {
	w := newWorld()
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	w.failOn = "caddy validate"
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Plugins: []string{"caddy"}, Yes: true}); err == nil {
		t.Fatal("a Caddyfile validation error must stop")
	}
	if w.index("systemctl enable --now caddy") >= 0 {
		t.Fatal("with a broken Caddyfile the service is not started")
	}
}

func TestEveryInstallablePluginIsAcceptedByPlan(t *testing.T) {
	e := Env{FS: host.NewMemFS(), Runner: &host.FakeRunner{Handler: func(host.Cmd) (host.Result, error) { return host.Result{}, nil }}}
	for _, n := range append(builtin.PluginNames(), "postgres") {
		if _, err := e.Plan(Options{Firewall: "off", Plugins: []string{n}}, nil); err != nil && strings.Contains(err.Error(), "unknown plugin") {
			t.Errorf("the plugin %s from PluginNames was rejected by the plan: %v", n, err)
		}
	}
}

func TestFirewallKeepDoesNotTouchUfw(t *testing.T) {
	var calls []string
	mem := host.NewMemFS()
	mem.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\n")
	e := Env{FS: mem, Runner: &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Name+" "+strings.Join(c.Args, " "))
		return host.Result{}, nil
	}}}
	p, err := e.Plan(Options{Firewall: "keep"}, nil)
	if err != nil || !strings.Contains(strings.Join(p, "\n"), "leave unchanged") {
		t.Fatalf("%v %v", p, err)
	}
	if _, err := e.Plan(Options{Firewall: "bogus"}, nil); err == nil {
		t.Fatal("an unknown mode must be rejected")
	}
}

// clock is a clock that moves one minute on every reading, so that waiting ends without real time passing.
func clock() func() time.Time {
	t := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	return func() time.Time { t = t.Add(time.Minute); return t }
}

func TestWaitsForKeyLoginThenClosesPasswords(t *testing.T) {
	w := newWorld()
	w.noPass["anna"] = false
	w.journal = "Accepted publickey for anna from 203.0.113.5 port 5000 ssh2: ED25519 SHA256:x\n"
	e := w.env(key + "\n")
	e.Sleep, e.Now = func(time.Duration) {}, clock()
	if err := e.Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true, WaitLogin: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.fs.Files[SSHDropIn]; !ok {
		t.Error("after a seen key login the password ban must be written")
	}
}

func TestNoKeyLoginLeavesPasswordsOn(t *testing.T) {
	w := newWorld()
	e := w.env(key + "\n")
	e.Sleep, e.Now = func(time.Duration) {}, clock()
	err := e.Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Plugins: []string{"security"}, Yes: true, WaitLogin: true, LoginTimeout: 3 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.fs.Files[SSHDropIn]; ok {
		t.Error("without a seen key login password login must stay on")
	}
	if w.index("apt-get install -y fail2ban") >= 0 {
		t.Error("the security plugin must wait too")
	}
	if !strings.Contains(w.out.String(), "NOT protected yet") || !strings.Contains(w.out.String(), "lite security enable") {
		t.Errorf("the person must be told what to do: %s", w.out.String())
	}
}

func TestUnreadableSSHLogKeepsOldBehaviour(t *testing.T) {
	w := newWorld()
	w.failOn = "journalctl"
	e := w.env(key + "\n")
	e.Sleep, e.Now = func(time.Duration) {}, clock()
	if err := e.Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true, WaitLogin: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.fs.Files[SSHDropIn]; !ok {
		t.Error("when the log cannot be read nothing can be verified: the old behavior stays")
	}
}

func TestInitSavesTheOriginalStateOnce(t *testing.T) {
	w := newWorld()
	w.fs.Files["/etc/ufw/user.rules"] = []byte("original rules\n")
	if err := w.env(key+"\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true}); err != nil {
		t.Fatal(err)
	}
	if string(w.fs.Files[OriginalDir+"/ufw/user.rules"]) != "original rules\n" {
		t.Error("the original firewall rules must be saved")
	}
	w.fs.Files["/etc/ufw/user.rules"] = []byte("changed by lite\n")
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true}); err != nil {
		t.Fatal(err)
	}
	if string(w.fs.Files[OriginalDir+"/ufw/user.rules"]) != "original rules\n" {
		t.Error("a second init must not overwrite the original state")
	}
}

func TestKeysFromAConfigAreUsedWithoutAsking(t *testing.T) {
	w := newWorld()
	w.noPass["anna"] = false
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true, Keys: map[string][]string{"anna": {key}}}); err != nil {
		t.Fatalf("a configuration with keys needs no question: %v", err)
	}
	if got := string(w.fs.Files["/home/anna/.ssh/authorized_keys"]); !strings.Contains(got, "AAAAC3NzaC1lZDI1NTE5AAAAIFakeFake") {
		t.Errorf("the key from the configuration was not added: %q", got)
	}
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true, Keys: map[string][]string{"anna": {"not a key"}}}); err == nil {
		t.Error("a bad key in the configuration must stop init")
	}
}
