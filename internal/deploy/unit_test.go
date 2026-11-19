package deploy

import (
	"context"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"strings"
	"testing"
)

const unitProjectJSON = `{"schema":"lite-deploy/project/v2","name":"shop",
 "services":{"api":{"kind":"unit","exec":["./bin/app"],"host_ports":[18001,18002],
   "release":{"minisign_key":"RWQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","url":"https://dl.example.com/shop/app-{sha}.tar.gz","token_secret":"secret://rel_token"},
   "health":{"path":"/health/ready","version_field":"version","timeout_seconds":10}}},
 "sites":[{"name":"main","host":"shop.example.com","service":"api"}]}`

func TestDeployUnitFetchesReleaseIntoIdleSlot(t *testing.T) {
	s, env := newSim(t)
	s.fs.Files["/etc/lite-deploy/projects/shop.json"] = []byte(unitProjectJSON)
	var calls []string
	inner := s.runner.Handler
	s.runner.Handler = func(c host.Cmd) (host.Result, error) {
		line := c.Name + " " + strings.Join(c.Args, " ")
		calls = append(calls, line)
		if c.Name == "systemctl" && len(c.Args) > 2 && c.Args[1] == "restart" {
			s.pulled = sha1
		}
		return inner(c)
	}
	m := open(t, env)
	r, e := m.Deploy(context.Background(), sha1)
	if e != nil {
		t.Fatal(e)
	}
	if r.Slot != "blue" || !r.Switched {
		t.Fatalf("итог: %+v", r)
	}
	all := strings.Join(calls, "\n")
	for _, want := range []string{
		"curl --fail", "--proto =https", "-H @/home/shop/.config/lite-deploy/release-api.hdr",
		"https://dl.example.com/shop/app-" + sha1 + ".tar.gz",
		"tar -xzf", "mv /home/shop/.local/share/lite-deploy/app/api/blue.new /home/shop/.local/share/lite-deploy/app/api/blue",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("нет вызова с %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "podman pull") {
		t.Error("для unit образ не скачивается")
	}
	if !strings.Contains(string(s.fs.Files["/home/shop/.local/share/lite-deploy/app/api/blue.new/.release"]), sha1) {
		t.Error("метка .release не записана")
	}
}

func TestDeployUnitRejectsBadSignature(t *testing.T) {
	s, env := newSim(t)
	s.fs.Files["/etc/lite-deploy/projects/shop.json"] = []byte(unitProjectJSON)
	s.fail["minisign -Vm"] = true
	var calls []string
	inner := s.runner.Handler
	s.runner.Handler = func(c host.Cmd) (host.Result, error) {
		calls = append(calls, c.Name+" "+strings.Join(c.Args, " "))
		return inner(c)
	}
	m := open(t, env)
	_, e := m.Deploy(context.Background(), sha1)
	if e == nil || e.Code != CodeFailed || !strings.Contains(e.Message, "подпись архива не подтверждена") {
		t.Fatalf("ошибка: %+v", e)
	}
	all := strings.Join(calls, "\n")
	if strings.Contains(all, "tar -xzf") || strings.Contains(all, "systemctl --user restart") {
		t.Fatalf("после неверной подписи архив не распаковывается и не запускается:\n%s", all)
	}
	if !strings.Contains(all, "minisign -Vm") || !strings.Contains(all, ".minisig") {
		t.Fatalf("подпись не проверялась:\n%s", all)
	}
	if m.Active() != "none" {
		t.Fatal("трафик не должен переключаться")
	}
}

func TestDeployUnitDownloadFailureKeepsTraffic(t *testing.T) {
	s, env := newSim(t)
	s.fs.Files["/etc/lite-deploy/projects/shop.json"] = []byte(unitProjectJSON)
	s.fail["curl --fail"] = true
	m := open(t, env)
	_, e := m.Deploy(context.Background(), sha1)
	if e == nil || e.Code != CodeFailed || !strings.Contains(e.Message, "скачивание релиза") {
		t.Fatalf("ошибка: %+v", e)
	}
	if m.Active() != "none" || strings.Contains(s.upstream(), "reverse_proxy") {
		t.Fatal("трафик не должен переключаться")
	}
}
