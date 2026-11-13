package firewall

import (
	"context"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const statusOut = `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     LIMIT       Anywhere                   # ssh
80/tcp                     ALLOW       Anywhere
443/tcp                    ALLOW       Anywhere
8080/tcp                   ALLOW       10.0.0.0/8
5432/tcp                   ALLOW       Anywhere
6000:6007/tcp              ALLOW       Anywhere
22/tcp (v6)                LIMIT       Anywhere (v6)
80/tcp (v6)                ALLOW       Anywhere (v6)
5432/tcp (v6)              ALLOW       Anywhere (v6)
`

type fake struct {
	status string
	calls  []string
	failOn string
	ssh    string
}

func (f *fake) runner() *host.FakeRunner {
	return &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		f.calls = append(f.calls, line)
		if f.failOn != "" && strings.Contains(line, f.failOn) {
			return host.Result{ExitCode: 1, Stderr: []byte("boom")}, nil
		}
		switch {
		case c.Name == "sshd":
			return host.Result{Stdout: []byte(f.ssh)}, nil
		case line == "ufw status":
			return host.Result{Stdout: []byte(f.status)}, nil
		}
		return host.Result{Stdout: []byte("Rule added")}, nil
	}}
}

func TestStatus(t *testing.T) {
	f := &fake{status: statusOut, ssh: "port 22\nx y\n"}
	st, e := (&Manager{Runner: f.runner()}).Status(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !st.Active || st.SSHPort != 22 {
		t.Fatalf("state: %+v", st)
	}
	// IPv6-дубли склеены; диапазон показан как есть, но без порта
	if len(st.Rules) != 6 {
		t.Fatalf("правил %d: %+v", len(st.Rules), st.Rules)
	}
	byPort := map[int]Rule{}
	for _, r := range st.Rules {
		byPort[r.Port] = r
	}
	if !byPort[22].Protected || byPort[22].Action != "limit" || !byPort[80].Protected || !byPort[443].Protected {
		t.Fatalf("защита: %+v", byPort)
	}
	if byPort[8080].Protected || byPort[8080].From != "10.0.0.0/8" || byPort[5432].From != "any" {
		t.Fatalf("правила: %+v", byPort)
	}
	if r := byPort[0]; r.Port != 0 || r.Proto != "tcp" && !strings.Contains(r.Raw, "6000") {
		t.Fatalf("диапазон: %+v", r)
	}
}

func TestStatusNonStandardSSHAndInactive(t *testing.T) {
	f := &fake{status: "Status: inactive\n", ssh: "port 2222\n"}
	st, e := (&Manager{Runner: f.runner()}).Status(context.Background())
	if e != nil || st.Active || st.SSHPort != 2222 || len(st.Rules) != 0 {
		t.Fatalf("%+v %v", st, e)
	}
	f = &fake{status: "Status: inactive\n", ssh: ""}
	st, _ = (&Manager{Runner: f.runner()}).Status(context.Background())
	if st.SSHPort != 22 {
		t.Fatalf("порт по умолчанию: %d", st.SSHPort)
	}
}

func TestAllow(t *testing.T) {
	f := &fake{status: statusOut, ssh: "port 22\n"}
	m := &Manager{Runner: f.runner()}
	if _, e := m.Allow(context.Background(), 9000, "tcp", ""); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Allow(context.Background(), 9001, "udp", "192.168.1.5"); e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"ufw allow 9000/tcp", "ufw allow proto udp from 192.168.1.5 to any port 9001"} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет вызова %q в\n%s", want, joined)
		}
	}
}

func TestAllowValidation(t *testing.T) {
	m := &Manager{Runner: (&fake{status: statusOut}).runner()}
	for _, c := range []struct {
		port        int
		proto, from string
	}{{0, "tcp", ""}, {70000, "tcp", ""}, {80, "icmp", ""}, {80, "tcp", "evil; rm -rf /"}, {80, "tcp", "10.0.0.0/99"}} {
		if _, e := m.Allow(context.Background(), c.port, c.proto, c.from); e == nil || e.Code != CodeUsage {
			t.Errorf("%+v: ожидалась ошибка usage, получено %v", c, e)
		}
	}
	if len(m.Runner.(*host.FakeRunner).Calls) != 0 {
		t.Fatal("при неверном вводе ufw вызываться не должен")
	}
}

func TestRemove(t *testing.T) {
	f := &fake{status: statusOut, ssh: "port 22\n"}
	m := &Manager{Runner: f.runner()}
	if _, e := m.Remove(context.Background(), 8080, "tcp", "10.0.0.0/8", "allow"); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Remove(context.Background(), 5432, "tcp", "", "allow"); e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"ufw delete allow proto tcp from 10.0.0.0/8 to any port 8080", "ufw delete allow 5432/tcp"} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет вызова %q в\n%s", want, joined)
		}
	}
}

func TestRemoveProtectedAndMissing(t *testing.T) {
	f := &fake{status: statusOut, ssh: "port 22\n"}
	m := &Manager{Runner: f.runner()}
	for _, p := range []int{22, 80, 443} {
		if _, e := m.Remove(context.Background(), p, "tcp", "", "allow"); e == nil || e.Code != CodeProtected {
			t.Errorf("порт %d: %v", p, e)
		}
	}
	if _, e := m.Remove(context.Background(), 7777, "tcp", "", "allow"); e == nil || e.Code != CodeNotFound {
		t.Errorf("нет правила: %v", e)
	}
	if _, e := m.Remove(context.Background(), 8080, "tcp", "", "allow"); e == nil || e.Code != CodeNotFound {
		t.Errorf("другой источник: %v", e)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "delete") {
			t.Fatalf("удаление не должно вызываться: %s", c)
		}
	}
}

func TestProtectedFollowsSSHPort(t *testing.T) {
	status := strings.ReplaceAll(statusOut, "22/tcp", "2222/tcp")
	f := &fake{status: status, ssh: "port 2222\n"}
	if _, e := (&Manager{Runner: f.runner()}).Remove(context.Background(), 2222, "tcp", "", "limit"); e == nil || e.Code != CodeProtected {
		t.Fatalf("порт SSH 2222 должен быть защищён: %v", e)
	}
}

func TestEnableOrderAndFailure(t *testing.T) {
	f := &fake{status: statusOut, ssh: "port 2222\n"}
	m := &Manager{Runner: f.runner()}
	if _, e := m.Enable(context.Background()); e != nil {
		t.Fatal(e)
	}
	var ufwCalls []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "ufw ") && c != "ufw status" {
			ufwCalls = append(ufwCalls, c)
		}
	}
	last := ufwCalls[len(ufwCalls)-1]
	if last != "ufw --force enable" {
		t.Fatalf("включение должно быть последним: %v", ufwCalls)
	}
	if !strings.Contains(strings.Join(ufwCalls[:len(ufwCalls)-1], "|"), "ufw limit 2222/tcp") {
		t.Fatalf("правило для SSH должно появиться до включения: %v", ufwCalls)
	}
	f = &fake{status: statusOut, ssh: "port 22\n", failOn: "limit"}
	if _, e := (&Manager{Runner: f.runner()}).Enable(context.Background()); e == nil || e.Code != CodeFailed {
		t.Fatalf("сбой правила: %v", e)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "enable") {
			t.Fatal("при сбое правила для SSH брандмауэр включаться не должен")
		}
	}
}

func TestUfwMissing(t *testing.T) {
	r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) { return host.Result{}, context.DeadlineExceeded }}
	if _, e := (&Manager{Runner: r}).Status(context.Background()); e == nil || e.Code != CodePrecondition {
		t.Fatalf("%v", e)
	}
}
