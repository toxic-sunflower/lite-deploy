package setup

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake me@laptop"

type world struct {
	fs      *host.MemFS
	calls   []string
	out     bytes.Buffer
	noPass  map[string]bool // у кого нет пароля
	failOn  string
	noUfw   bool
	created []string
}

func newWorld() *world {
	m := host.NewMemFS()
	m.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\nanna:x:1000:1000::/home/anna:/bin/bash\nshop:x:1001:1001::/home/shop:/bin/bash\nwww:x:33:33::/var/www:/usr/sbin/nologin\nsvc:x:1002:1002::/home/svc:/bin/false\n")
	m.Files["/etc/ssh/sshd_config"] = []byte("Include /etc/ssh/sshd_config.d/*.conf\n")
	return &world{fs: m, noPass: map[string]bool{}}
}

func (w *world) env(input string) Env {
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		w.calls = append(w.calls, line)
		if w.failOn != "" && strings.HasPrefix(line, w.failOn) {
			return host.Result{ExitCode: 1, Stderr: []byte("сбой")}, nil
		}
		switch {
		case c.Name == "ufw" && w.noUfw && line == "ufw version":
			return host.Result{}, context.DeadlineExceeded
		case c.Name == "getent":
			if strings.HasSuffix(line, " sudo") {
				return host.Result{}, nil
			}
			return host.Result{ExitCode: 2}, nil
		case line == "sshd -T":
			return host.Result{Stdout: []byte("port 2222\n")}, nil
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
			t.Errorf("нет вызова %q в\n%s", x, joined)
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
		t.Error("в режиме max веб-порты не открываются")
	}
	if got := string(w.fs.Files["/home/serafim/.ssh/authorized_keys"]); got != key+"\n" {
		t.Fatalf("authorized_keys: %q", got)
	}
	if w.fs.Perms["/home/serafim/.ssh/authorized_keys"] != 0o600 || w.fs.Owners["/home/serafim/.ssh/authorized_keys"][0] != "serafim" {
		t.Errorf("права ключа: %v %v", w.fs.Perms, w.fs.Owners)
	}
	drop := string(w.fs.Files[SSHDropIn])
	if !strings.Contains(drop, "Match User serafim\n") || !strings.Contains(drop, "PasswordAuthentication no") {
		t.Fatalf("sshd: %q", drop)
	}
	// порядок: правило SSH до включения файервола; запрет пароля после ключа и файервола
	if w.index("ufw limit") >= w.index("ufw --force enable") || w.index("ufw --force enable") >= w.index("sshd -t") {
		t.Fatalf("порядок: %v", w.calls)
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
		t.Fatalf("существующего пользователя с паролем не трогаем: %s", joined)
	}
	w.has(t, "usermod -aG sudo anna", "ufw allow 80/tcp", "ufw allow 443/tcp", "ufw allow 443/udp")
	if string(w.fs.Files["/home/anna/.ssh/authorized_keys"]) != key+"\n" {
		t.Fatal("ключи изменились")
	}
}

func TestKeyIsMandatory(t *testing.T) {
	w := newWorld()
	err := w.env("y\nне ключ\nтоже нет\nssh-rsa коротко\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "max"})
	if err == nil {
		t.Fatal("без ключа init должен остановиться")
	}
	if _, ok := w.fs.Files[SSHDropIn]; ok || w.index("ufw --force") >= 0 {
		t.Fatalf("без ключа ни файервол, ни sshd не меняются: %v", w.calls)
	}
	w = newWorld()
	if err := w.env("y\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "max"}); err == nil {
		t.Fatal("нет ввода и нет ключей: отказ")
	}
}

func TestRootNeedsUser(t *testing.T) {
	for _, cur := range []string{"", "root"} {
		w := newWorld()
		if err := w.env("y\n").Apply(context.Background(), Options{Current: cur, Firewall: "max"}); err == nil || !strings.Contains(err.Error(), "--user") {
			t.Fatalf("%q: %v", cur, err)
		}
		if len(w.calls) != 0 {
			t.Fatalf("ничего не должно выполняться: %v", w.calls)
		}
	}
}

func TestAllUsers(t *testing.T) {
	w := newWorld()
	got, err := w.env("").ResolveUsers(Options{AllUsers: true})
	if err != nil || strings.Join(got, ",") != "anna" {
		t.Fatalf("только люди с оболочкой, без проектов и служебных: %v %v", got, err)
	}
}

func TestRejections(t *testing.T) {
	w := newWorld()
	e := w.env("")
	for _, o := range []Options{{Users: []string{"shop"}}, {Users: []string{"root"}}, {Users: []string{"Bad Name"}}, {Users: []string{"a;b"}}} {
		if _, err := e.ResolveUsers(o); err == nil {
			t.Errorf("%v должен отклоняться", o.Users)
		}
	}
	if err := w.env("y\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "wide"}); err == nil {
		t.Error("неизвестный режим файервола")
	}
	if err := w.env("y\n").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "max", Plugins: []string{"mysql"}}); err == nil {
		t.Error("неизвестный плагин")
	}
	if len(w.calls) != 0 {
		t.Fatalf("при неверных параметрах ничего не выполняется: %v", w.calls)
	}
}

func TestDeclinedPlanChangesNothing(t *testing.T) {
	w := newWorld()
	if err := w.env("n\n").Apply(context.Background(), Options{Users: []string{"serafim"}, Firewall: "max"}); err == nil {
		t.Fatal("отказ должен останавливать")
	}
	if len(w.calls) != 0 || len(w.created) != 0 {
		t.Fatalf("после отказа ничего не меняется: %v", w.calls)
	}
	if !strings.Contains(w.out.String(), "будет создан") {
		t.Fatalf("план должен быть показан: %s", w.out.String())
	}
}

func TestSSHDFailureRestores(t *testing.T) {
	w := newWorld()
	w.fs.Files["/home/anna/.ssh/authorized_keys"] = []byte(key + "\n")
	w.failOn = "sshd -t"
	if err := w.env("").Apply(context.Background(), Options{Users: []string{"anna"}, Firewall: "off", Yes: true}); err == nil {
		t.Fatal("ошибка проверки sshd должна останавливать")
	}
	if _, ok := w.fs.Files[SSHDropIn]; ok {
		t.Fatal("непроверенный файл sshd должен быть убран")
	}
	if w.index("systemctl reload") >= 0 {
		t.Fatal("sshd не перечитывается при ошибке проверки")
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
		t.Fatalf("ufw нет и режим off: ставить и трогать нечего: %v", w.calls)
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
		t.Fatal("без Include настройка не подействует: нужно сказать об этом")
	}
}
