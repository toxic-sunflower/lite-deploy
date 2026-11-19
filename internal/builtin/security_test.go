package builtin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

func init() { waitStep = func(time.Duration) {} }

const pubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake anna@laptop"

// server притворяется сервером: файлы в памяти, а sshd -T отражает записанные файлы.
type server struct {
	fs        *host.MemFS
	calls     []string
	sshdTfail bool   // sshd -t сообщает об ошибке
	override  bool   // другой файл sshd переопределяет наши настройки
	listen    string // вывод ss
	ufw       string // вывод ufw status
	f2b       bool   // fail2ban работает
	installed map[string]bool
	sysctl    map[string]string
}

func newServer() *server {
	m := host.NewMemFS()
	m.Files["/etc/passwd"] = []byte("root:x:0:0::/root:/bin/bash\nanna:x:1000:1000::/home/anna:/bin/bash\nbob:x:1001:1001::/home/bob:/bin/bash\nshop:x:1002:1002::/home/shop:/bin/bash\n")
	m.Files["/etc/group"] = []byte("sudo:x:27:anna,bob\n")
	m.Files["/home/anna/.ssh/authorized_keys"] = []byte(pubKey + "\n")
	m.Files["/etc/ssh/sshd_config"] = []byte("Include /etc/ssh/sshd_config.d/*.conf\n")
	return &server{fs: m, installed: map[string]bool{}, sysctl: map[string]string{}, ufw: "Status: inactive\n", listen: "tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\n"}
}

func (s *server) sshdT() string {
	cfg := map[string]string{"permitrootlogin": "yes", "passwordauthentication": "yes", "port": "22"}
	if b, err := s.fs.ReadFile(SSHSecurity); err == nil && !s.override {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) >= 2 && !strings.HasPrefix(l, "#") {
				cfg[strings.ToLower(f[0])] = strings.Join(f[1:], " ")
			}
		}
	}
	var sb strings.Builder
	for k, v := range cfg {
		sb.WriteString(k + " " + v + "\n")
	}
	return sb.String()
}

func (s *server) run(c host.Cmd) (host.Result, error) {
	line := c.Name + " " + strings.Join(c.Args, " ")
	s.calls = append(s.calls, line)
	ok := host.Result{}
	switch c.Name {
	case "apt-get":
		if c.Args[0] == "--version" {
			return ok, nil
		}
		if c.Args[0] == "install" {
			for _, a := range c.Args[1:] {
				if !strings.HasPrefix(a, "-") {
					s.installed[a] = true
					if a == "fail2ban" {
						s.f2b = true
					}
				}
			}
		}
		if c.Args[0] == "-s" {
			return host.Result{Stdout: []byte("Inst openssl [3.0] (3.1 Ubuntu:24.04/noble-security)\nInst vim [1] (2 noble-updates)\n")}, nil
		}
	case "dpkg-query":
		if s.installed[c.Args[len(c.Args)-1]] {
			return host.Result{Stdout: []byte("install ok installed|1.0")}, nil
		}
		return host.Result{ExitCode: 1}, nil
	case "sshd":
		if c.Args[0] == "-t" && s.sshdTfail {
			return host.Result{ExitCode: 255, Stderr: []byte("плохая настройка")}, nil
		}
		if c.Args[0] == "-T" {
			return host.Result{Stdout: []byte(s.sshdT())}, nil
		}
	case "fail2ban-client":
		if !s.f2b {
			return host.Result{ExitCode: 1}, nil
		}
		return host.Result{Stdout: []byte("Status for the jail: sshd\n")}, nil
	case "apt-config":
		if _, err := s.fs.ReadFile(aptAutoFile); err == nil {
			return host.Result{Stdout: []byte("APT::Periodic::Unattended-Upgrade \"1\";\n")}, nil
		}
	case "sysctl":
		if c.Args[0] == "-n" {
			if v, ok := s.sysctl[c.Args[1]]; ok {
				return host.Result{Stdout: []byte(v + "\n")}, nil
			}
			return host.Result{Stdout: []byte("0\n")}, nil
		}
		for _, l := range strings.Split(sysctlBody, "\n") {
			if k, v, ok := strings.Cut(l, " = "); ok {
				s.sysctl[k] = v
			}
		}
	case "ss":
		return host.Result{Stdout: []byte(s.listen)}, nil
	case "ufw":
		return host.Result{Stdout: []byte(s.ufw)}, nil
	}
	return ok, nil
}

func (s *server) env(user string, projects ...string) plugin.Env {
	return plugin.Env{Runner: &host.FakeRunner{Handler: s.run}, FS: s.fs, User: user, Projects: projects,
		Now: func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }}
}

func (s *server) did(prefix string) int {
	for i, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func runSec(s *server, user string, cmd string, args ...string) (plugin.Output, error) {
	return Security{}.Run(context.Background(), s.env(user, "shop"), cmd, args)
}

func code(err error) string {
	var pe *plugin.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func TestEnableFull(t *testing.T) {
	s := newServer()
	out, err := runSec(s, "anna", "enable")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "fail2ban") {
		t.Fatalf("%s", out.Text)
	}
	body, _ := s.fs.ReadFile(SSHSecurity)
	for _, want := range []string{"PermitRootLogin prohibit-password", "PasswordAuthentication no", "KbdInteractiveAuthentication no", "MaxAuthTries 4"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("в файле sshd нет %q:\n%s", want, body)
		}
	}
	if j, _ := s.fs.ReadFile(jailFile); !strings.Contains(string(j), "[sshd]") || !strings.Contains(string(j), "port = 22") {
		t.Errorf("jail: %s", j)
	}
	if _, err := s.fs.ReadFile(aptAutoFile); err != nil {
		t.Error("нет файла автообновлений")
	}
	if _, err := s.fs.ReadFile(sysctlFile); err != nil {
		t.Error("нет файла sysctl")
	}
	// порядок: SSH в самом конце, после остальных защит (чтобы сбой раньше не запер сервер)
	if s.did("systemctl restart fail2ban") >= s.did("sysctl -e") || s.did("sysctl -e") >= s.did("sshd -t") || s.did("sshd -t") >= s.did("systemctl reload ssh") {
		t.Errorf("порядок шагов:\n%s", strings.Join(s.calls, "\n"))
	}
	// всё применено: проверки зелёные
	for _, f := range (Security{}).Check(context.Background(), s.env("anna", "shop")) {
		if f.Level == plugin.LevelFail || (f.Level == plugin.LevelWarn && f.Check != "ssh-allow" && f.Check != "admins" && f.Check != "pending-updates") {
			t.Errorf("после enable: %+v", f)
		}
	}
}

func TestSSHGuards(t *testing.T) {
	s := newServer()
	delete(s.fs.Files, "/home/anna/.ssh/authorized_keys")
	if _, err := runSec(s, "anna", "ssh"); code(err) != "precondition_failed" {
		t.Fatalf("без ключей у администраторов: %v", err)
	}
	if _, err := s.fs.ReadFile(SSHSecurity); err == nil {
		t.Fatal("файл не должен появляться")
	}
	s = newServer() // у bob нет ключа, вызывает bob
	if _, err := runSec(s, "bob", "ssh"); code(err) != "precondition_failed" || !strings.Contains(err.Error(), "bob") {
		t.Fatalf("у вызывающего нет ключа: %v", err)
	}
	if s.did("sshd") >= 0 {
		t.Fatal("до проверки sshd дело не должно доходить")
	}
}

func TestSSHRollbacks(t *testing.T) {
	s := newServer()
	s.fs.Files[SSHSecurity] = []byte("# прежний\nPermitRootLogin no\n")
	s.sshdTfail = true
	if _, err := runSec(s, "anna", "ssh"); code(err) != "step_failed" {
		t.Fatalf("%v", err)
	}
	if b, _ := s.fs.ReadFile(SSHSecurity); string(b) != "# прежний\nPermitRootLogin no\n" {
		t.Fatalf("прежний файл должен вернуться: %q", b)
	}
	if s.did("systemctl reload") >= 0 {
		t.Fatal("sshd не должен перечитываться")
	}
	s = newServer()
	s.override = true // cloud-init или другой файл побеждает
	if _, err := runSec(s, "anna", "ssh"); code(err) != "step_failed" || !strings.Contains(err.Error(), "overrides") {
		t.Fatalf("%v", err)
	}
	if _, err := s.fs.ReadFile(SSHSecurity); err == nil {
		t.Fatal("нового файла быть не должно")
	}
	s = newServer()
	s.fs.Files["/etc/ssh/sshd_config"] = []byte("Port 22\n")
	if _, err := runSec(s, "anna", "ssh"); code(err) != "precondition_failed" {
		t.Fatalf("нет Include: %v", err)
	}
}

func TestNoRootAndAllowUsers(t *testing.T) {
	s := newServer()
	if _, err := runSec(s, "anna", "ssh", "no-root"); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.fs.ReadFile(SSHSecurity); !strings.Contains(string(b), "PermitRootLogin no") {
		t.Fatalf("%s", b)
	}
	if _, err := runSec(s, "anna", "ssh", "extra"); code(err) != "usage" {
		t.Fatalf("%v", err)
	}
	// allow-users сохраняет настройку root и добавляет пользователей проектов
	if _, err := runSec(s, "anna", "allow-users", "anna"); err != nil {
		t.Fatal(err)
	}
	b, _ := s.fs.ReadFile(SSHSecurity)
	if !strings.Contains(string(b), "AllowUsers anna shop") || !strings.Contains(string(b), "PermitRootLogin no") {
		t.Fatalf("%s", b)
	}
	// нельзя исключить себя или всех администраторов с ключом
	if _, err := runSec(s, "anna", "allow-users", "bob"); code(err) != "precondition_failed" {
		t.Fatalf("%v", err)
	}
	if _, err := runSec(s, "anna", "allow-users", "ghost"); code(err) != "usage" {
		t.Fatalf("несуществующий пользователь: %v", err)
	}
	if _, err := runSec(s, "anna", "allow-users", "any"); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.fs.ReadFile(SSHSecurity); strings.Contains(string(b), "AllowUsers") {
		t.Fatalf("ограничение должно сняться: %s", b)
	}
}

func TestVerifyFindsProblems(t *testing.T) {
	s := newServer()
	s.listen = "tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\ntcp LISTEN 0 128 0.0.0.0:5432 0.0.0.0:*\ntcp LISTEN 0 128 127.0.0.1:6379 0.0.0.0:*\nudp UNCONN 0 0 0.0.0.0:68 0.0.0.0:*\n"
	got := map[string]plugin.Finding{}
	for _, f := range (Security{}).Check(context.Background(), s.env("anna")) {
		got[f.Check] = f
	}
	for check, level := range map[string]string{"ssh-root": plugin.LevelFail, "ssh-password": plugin.LevelFail, "fail2ban": plugin.LevelFail, "auto-updates": plugin.LevelWarn,
		"pending-updates": plugin.LevelWarn, "sysctl": plugin.LevelWarn, "ports": plugin.LevelFail, "ssh-allow": plugin.LevelWarn} {
		if got[check].Level != level {
			t.Errorf("%s: %+v, ожидался уровень %s", check, got[check], level)
		}
	}
	if !strings.Contains(got["ports"].Message, "5432/tcp") || strings.Contains(got["ports"].Message, "6379") || strings.Contains(got["ports"].Message, "68") {
		t.Errorf("порты: %s", got["ports"].Message)
	}
	if !strings.Contains(got["pending-updates"].Message, "1") {
		t.Errorf("обновления: %s", got["pending-updates"].Message)
	}
	// файервол включён и порт 5432 в нём открыт: предупреждение; не открыт: порядок
	s.ufw = "Status: active\n\nTo                         Action      From\n--                         ------      ----\n22/tcp                     ALLOW       Anywhere\n5432/tcp                   ALLOW       Anywhere\n"
	for _, f := range (Security{}).Check(context.Background(), s.env("anna")) {
		if f.Check == "ports" && f.Level != plugin.LevelWarn {
			t.Errorf("порт открыт в файерволе: %+v", f)
		}
	}
	s.ufw = "Status: active\n\nTo                         Action      From\n--                         ------      ----\n22/tcp                     ALLOW       Anywhere\n"
	for _, f := range (Security{}).Check(context.Background(), s.env("anna")) {
		if f.Check == "ports" && f.Level != plugin.LevelOK {
			t.Errorf("порт закрыт файерволом: %+v", f)
		}
	}
}

func TestLoginsBannedUnban(t *testing.T) {
	s := newServer()
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		switch c.Name {
		case "last":
			return host.Result{Stdout: []byte("anna pts/0 10.0.0.1 Wed Nov 18 12:00 still logged in\n\nwtmp begins Mon Nov 1\n")}, nil
		case "journalctl":
			if c.Args[1] == "ssh" {
				return host.Result{Stdout: []byte("t sshd: Failed password for root from 1.2.3.4\nt sshd: Accepted publickey for anna\nt sshd: Invalid user admin from 5.6.7.8\n")}, nil
			}
		case "fail2ban-client":
			return host.Result{Stdout: []byte("Banned IP list: 1.2.3.4\n")}, nil
		}
		return host.Result{}, nil
	}}
	env := s.env("anna")
	env.Runner = r
	out, err := Security{}.Run(context.Background(), env, "logins", nil)
	if err != nil || !strings.Contains(out.Text, "anna pts/0") || !strings.Contains(out.Text, "Failed password for root") || strings.Contains(out.Text, "Accepted") || strings.Contains(out.Text, "wtmp begins") {
		t.Fatalf("%v\n%s", err, out.Text)
	}
	if _, err := (Security{}).Run(context.Background(), env, "logins", []string{"0"}); code(err) != "usage" {
		t.Fatalf("%v", err)
	}
	if out, err := (Security{}).Run(context.Background(), env, "banned", nil); err != nil || !strings.Contains(out.Text, "1.2.3.4") {
		t.Fatalf("%v %s", err, out.Text)
	}
	if _, err := (Security{}).Run(context.Background(), env, "unban", []string{"1.2.3.4"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"1.2.3.4; id", "-a", "host"} {
		if _, err := (Security{}).Run(context.Background(), env, "unban", []string{bad}); code(err) != "usage" {
			t.Errorf("%q должен отклоняться", bad)
		}
	}
	if len(r.Calls) == 0 || r.Calls[len(r.Calls)-1].Args[2] != "unbanip" {
		t.Fatalf("%+v", r.Calls)
	}
}

func TestSetINI(t *testing.T) {
	got := setINI("[commands]\nupgrade_type = default\n[emitters]\nx = 1", "commands", "upgrade_type", "security")
	if !strings.Contains(got, "upgrade_type = security") || strings.Contains(got, "default") {
		t.Fatal(got)
	}
	got = setINI("[commands]\n", "commands", "apply_updates", "yes")
	if !strings.Contains(got, "[commands]\napply_updates = yes") {
		t.Fatal(got)
	}
	got = setINI("", "commands", "apply_updates", "yes")
	if !strings.Contains(got, "[commands]\napply_updates = yes") {
		t.Fatal(got)
	}
}

func TestSSHDEffectiveNormalizesWithoutPassword(t *testing.T) {
	s := newServer()
	sc := &sec{tools: tools{context.Background(), plugin.Env{FS: s.fs, Runner: fn(func(c host.Cmd) (host.Result, error) {
		if c.Name == "sshd" && len(c.Args) > 0 && c.Args[0] == "-T" {
			return host.Result{Stdout: []byte("permitrootlogin without-password\npasswordauthentication no\n")}, nil
		}
		return host.Result{}, nil
	})}}}
	m, err := sc.sshdEffective()
	if err != nil || m["permitrootlogin"] != "prohibit-password" {
		t.Fatalf("%v %v", m, err)
	}
}
