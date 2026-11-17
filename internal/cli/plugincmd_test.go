package cli

import (
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

func TestPackageCommands(t *testing.T) {
	env, out, _ := testEnv(t)
	installed := map[string]string{}
	env.Runner = &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		switch c.Name {
		case "apt-get":
			if c.Args[0] == "install" {
				for _, a := range c.Args[1:] {
					if !strings.HasPrefix(a, "-") {
						installed[a] = "1.0"
					}
				}
			}
		case "dpkg-query":
			if v, ok := installed[c.Args[len(c.Args)-1]]; ok {
				return host.Result{Stdout: []byte("install ok installed|" + v)}, nil
			}
			return host.Result{ExitCode: 1}, nil
		}
		return host.Result{}, nil
	}}
	// без --yes и без терминала команда не выполняется
	if c := run(t, env, "package", "install", "nginx", "--json"); c != 4 {
		t.Fatalf("код %d: %s", c, out.String())
	}
	out.Reset()
	if c := run(t, env, "package", "install", "nginx", "--yes", "--json"); c != 0 {
		t.Fatalf("код %d: %s", c, out.String())
	}
	e := decode(t, out)
	if !e.OK || e.Command != "package.install" {
		t.Fatalf("%+v", e)
	}
	out.Reset()
	run(t, env, "package", "list", "--json")
	if !strings.Contains(out.String(), `"name":"nginx"`) {
		t.Fatalf("журнал: %s", out.String())
	}
	out.Reset()
	run(t, env, "package", "check", "nginx", "--json")
	if !strings.Contains(out.String(), `"installed":true`) || !strings.Contains(out.String(), `"by_lite":true`) {
		t.Fatalf("%s", out.String())
	}
	out.Reset()
	if c := run(t, env, "package", "remove", "sudo", "--yes", "--json"); c != 4 {
		t.Fatalf("защищённый пакет: код %d %s", c, out.String())
	}
	out.Reset()
	if c := run(t, env, "package", "install", "-y", "--yes", "--json"); c != 2 {
		t.Fatalf("неверное имя: код %d %s", c, out.String())
	}
	out.Reset()
	// не root: изменяющая команда запрещена, чтение можно
	env.IsRoot = func() bool { return false }
	if c := run(t, env, "package", "install", "nginx", "--yes", "--json"); c != 4 {
		t.Fatalf("не root: код %d %s", c, out.String())
	}
	out.Reset()
	if c := run(t, env, "package", "check", "nginx", "--json"); c != 0 {
		t.Fatalf("проверка без root: код %d %s", c, out.String())
	}
}

func TestInfoVerifyAndExternalPlugin(t *testing.T) {
	env, out, _ := testEnv(t)
	m := memfs(env)
	_ = m.MkdirAll("/etc/lite-deploy/plugins/logs", 0o755)
	m.Files["/etc/lite-deploy/plugins/logs/plugin.json"] = []byte(`{"name":"logs","description":"журналы","commands":[{"name":"tail","help":"хвост","exec":"bin/tail.sh"}],"checks":[{"name":"size","exec":"bin/size.sh","fix":"lite logs clear"}]}`)
	env.Runner = &host.FakeRunner{Handler: func(c host.Cmd) (host.Result, error) {
		switch {
		case c.Name == "apt-get" && c.Args[0] == "--version":
			return host.Result{}, nil
		case strings.HasSuffix(c.Name, "tail.sh"):
			return host.Result{Stdout: []byte("строка\n")}, nil
		case strings.HasSuffix(c.Name, "size.sh"):
			return host.Result{ExitCode: 1, Stdout: []byte("журналы большие")}, nil
		}
		return host.Result{ExitCode: 127}, nil
	}}
	if c := run(t, env, "info", "--json"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	if s := out.String(); !strings.Contains(s, `"name":"package"`) || !strings.Contains(s, `"name":"logs"`) || !strings.Contains(s, `"kind":"external"`) {
		t.Fatalf("%s", s)
	}
	out.Reset()
	if c := run(t, env, "logs", "tail", "--json"); c != 0 || !strings.Contains(out.String(), "строка") {
		t.Fatalf("%d %s", c, out.String())
	}
	out.Reset()
	if c := run(t, env, "plugin", "run", "logs", "tail", "--json"); c != 0 {
		t.Fatalf("%d %s", c, out.String())
	}
	out.Reset()
	// в этом окружении у security есть ошибки (нет fail2ban), поэтому verify завершается кодом 8; предупреждение плагина logs видно
	if c := run(t, env, "verify", "--json"); c != 8 {
		t.Fatalf("%d %s", c, out.String())
	}
	if s := out.String(); !strings.Contains(s, `"plugin":"logs","check":"size","level":"warn"`) || !strings.Contains(s, "lite logs clear") {
		t.Fatalf("%s", s)
	}
	out.Reset()
	if c := run(t, env, "nothing", "--json"); c != 2 {
		t.Fatalf("неизвестная команда: %d", c)
	}
	out.Reset()
	if c := run(t, env, "logs", "bad", "--json"); c != 2 {
		t.Fatalf("неизвестная команда плагина: %d", c)
	}
}

func TestVerifyFails(t *testing.T) {
	env, out, _ := testEnv(t) // менеджера пакетов нет: проверка package даёт ошибку
	if c := run(t, env, "verify", "--json"); c != 8 {
		t.Fatalf("код %d %s", c, out.String())
	}
	e := decode(t, out)
	if e.OK || e.Error == nil || e.Error.Code != "verify_failed" {
		t.Fatalf("%+v", e)
	}
}

func TestPluginsDeniedForRootLogin(t *testing.T) {
	env, out, _ := testEnv(t)
	env.LoginUID = func() (int, bool) { return 0, true }
	for _, a := range [][]string{{"info"}, {"verify"}, {"package", "list"}, {"plugin", "list"}} {
		out.Reset()
		if c := run(t, env, append(a, "--json")...); c != 4 {
			t.Errorf("%v при входе под root: код %d", a, c)
		}
	}
}
