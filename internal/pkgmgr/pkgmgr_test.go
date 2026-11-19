package pkgmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// fakeApt pretends to be apt and dpkg: it remembers which packages are installed.
type fakeApt struct {
	installed map[string]string
	calls     []string
	failOn    string
}

func (f *fakeApt) run(c host.Cmd) (host.Result, error) {
	line := c.Name + " " + strings.Join(c.Args, " ")
	f.calls = append(f.calls, line)
	if f.failOn != "" && strings.Contains(line, f.failOn) {
		return host.Result{ExitCode: 100, Stderr: []byte("not found")}, nil
	}
	switch c.Name {
	case "apt-get":
		switch {
		case len(c.Args) > 0 && c.Args[0] == "--version":
		case c.Args[0] == "install":
			for _, a := range c.Args {
				if _, have := f.installed[a]; !strings.HasPrefix(a, "-") && a != "install" && !have {
					f.installed[a] = "1.0"
				}
			}
		case c.Args[0] == "remove":
			for _, a := range c.Args[1:] {
				delete(f.installed, a)
			}
		}
	case "dpkg-query":
		n := c.Args[len(c.Args)-1]
		if v, ok := f.installed[n]; ok {
			return host.Result{Stdout: []byte("install ok installed|" + v)}, nil
		}
		return host.Result{ExitCode: 1}, nil
	}
	return host.Result{}, nil
}

func newMgr() (*Manager, *fakeApt) {
	f := &fakeApt{installed: map[string]string{"curl": "7.0"}}
	return &Manager{Runner: &host.FakeRunner{Handler: f.run}, FS: host.NewMemFS(), Journal: "/var/lib/lite-deploy/packages.json",
		Now: func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) }, By: "anna"}, f
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestInstallJournalCheckRemove(t *testing.T) {
	m, f := newMgr()
	ctx := context.Background()
	if err := m.Install(ctx, "nginx", "curl"); err != nil {
		t.Fatal(err)
	}
	es, _ := m.List()
	if len(es) != 1 || es[0].Name != "nginx" || es[0].By != "anna" || es[0].InstalledAt != "2026-11-18T12:00:00Z" {
		t.Fatalf("only new packages go into the journal: %+v", es)
	}
	in, err := m.Check(ctx, "nginx")
	if err != nil || !in.Installed || !in.ByLite || in.Version != "1.0" {
		t.Fatalf("%+v %v", in, err)
	}
	in, _ = m.Check(ctx, "curl")
	if !in.Installed || in.ByLite {
		t.Fatalf("curl installed not through lite: %+v", in)
	}
	in, _ = m.Check(ctx, "redis")
	if in.Installed {
		t.Fatalf("%+v", in)
	}
	if err := m.Remove(ctx, "nginx"); err != nil {
		t.Fatal(err)
	}
	if es, _ := m.List(); len(es) != 0 {
		t.Fatalf("after removal the journal is empty: %+v", es)
	}
	if _, ok := f.installed["nginx"]; ok {
		t.Fatal("nginx must be removed")
	}
}

func TestUpdateRefreshesVersion(t *testing.T) {
	m, f := newMgr()
	ctx := context.Background()
	_ = m.Install(ctx, "nginx")
	f.installed["nginx"] = "2.0"
	if err := m.Update(ctx, "nginx"); err != nil {
		t.Fatal(err)
	}
	if es, _ := m.List(); es[0].Version != "2.0" {
		t.Fatalf("%+v", es)
	}
	if code(m.Update(ctx, "redis")) != "precondition_failed" {
		t.Fatal("an uninstalled package cannot be updated")
	}
}

func TestRejects(t *testing.T) {
	m, f := newMgr()
	ctx := context.Background()
	for _, n := range []string{"", "-y", "a b", "x;id", "../x", "$(id)"} {
		if code(m.Install(ctx, n)) != "usage" {
			t.Errorf("the name %q must be rejected", n)
		}
	}
	if code(m.Install(ctx)) != "usage" {
		t.Error("without names: a refusal")
	}
	for _, n := range []string{"openssh-server", "sudo", "systemd", "bash"} {
		if code(m.Remove(ctx, n)) != "protected_rule" {
			t.Errorf("%s cannot be removed", n)
		}
	}
	if code(m.Remove(ctx, "redis")) != "precondition_failed" {
		t.Error("an uninstalled package cannot be removed")
	}
	for _, c := range f.calls {
		if strings.Contains(c, "remove") && strings.Contains(c, "sudo") {
			t.Fatalf("the command to remove a protected package reached the system: %s", c)
		}
	}
}

func TestInstallFailureLeavesJournalAlone(t *testing.T) {
	m, f := newMgr()
	f.failOn = "install -y -qq nginx"
	if code(m.Install(context.Background(), "nginx")) != "step_failed" {
		t.Fatal("a package manager error")
	}
	if es, _ := m.List(); len(es) != 0 {
		t.Fatalf("%+v", es)
	}
}

func TestOtherManagers(t *testing.T) {
	for _, mgr := range []string{"dnf", "zypper", "pacman"} {
		var calls []string
		r := &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
			line := c.Name + " " + strings.Join(c.Args, " ")
			calls = append(calls, line)
			if c.Name == "apt-get" {
				return host.Result{}, errors.New("none")
			}
			if c.Name != mgr && len(c.Args) > 0 && c.Args[0] == "--version" {
				return host.Result{}, errors.New("none")
			}
			if c.Name == "rpm" || (c.Name == "pacman" && c.Args[0] == "-Q") {
				if mgr == "pacman" {
					return host.Result{Stdout: []byte("htop 3.3")}, nil
				}
				return host.Result{Stdout: []byte("3.3-1")}, nil
			}
			return host.Result{}, nil
		}}
		m := &Manager{Runner: r, FS: host.NewMemFS(), Now: time.Now}
		got, _ := m.Detect(context.Background())
		if got != mgr {
			t.Fatalf("%s: defined %q", mgr, got)
		}
		in, err := m.Check(context.Background(), "htop")
		if err != nil || !in.Installed || in.Version != map[string]string{"pacman": "3.3"}[mgr]+map[string]string{"dnf": "3.3-1", "zypper": "3.3-1"}[mgr] {
			t.Errorf("%s: %+v %v", mgr, in, err)
		}
	}
}

func TestDockerIsForbidden(t *testing.T) {
	m := &Manager{Runner: &host.FakeRunner{Handler: func(host.Cmd) (host.Result, error) { return host.Result{}, nil }}, FS: host.NewMemFS()}
	for _, n := range []string{"docker", "docker.io", "docker-ce", "docker-compose-plugin", "containerd.io", "moby-engine"} {
		for name, op := range map[string]func(context.Context, ...string) error{"install": m.Install, "update": m.Update} {
			err := op(context.Background(), n)
			var pe *Error
			if !errors.As(err, &pe) || pe.Code != "protected_rule" {
				t.Errorf("%s %s: %v", name, n, err)
			}
		}
	}
	if forbidden.MatchString("podman") || forbidden.MatchString("docker-clean") {
		t.Error("extra packages fell under the ban")
	}
}
