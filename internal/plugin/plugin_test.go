package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

const goodManifest = `{
  "name": "logs",
  "description": "просмотр журналов",
  "requires": ["jq"],
  "commands": [
    {"name": "tail", "help": "последние строки", "args": "СЛУЖБА", "exec": "bin/tail.sh"},
    {"name": "clear", "help": "очистить", "exec": "bin/clear.sh", "confirm": true}
  ],
  "checks": [{"name": "size", "exec": "bin/size.sh", "fix": "lite logs clear"}]
}`

func setup(t *testing.T, manifest string) (*host.MemFS, *Registry, []error, *host.FakeRunner) {
	t.Helper()
	m := host.NewMemFS()
	_ = m.MkdirAll("/etc/lite-deploy/plugins/logs", 0o755)
	m.Files["/etc/lite-deploy/plugins/logs/plugin.json"] = []byte(manifest)
	r := NewRegistry()
	errs := LoadDir(m, Dir, r, func(_ context.Context, n string) bool { return n != "jq" || m.Files["/jq"] != nil })
	return m, r, errs, &host.FakeRunner{}
}

func TestLoadAndRun(t *testing.T) {
	m, r, errs, fr := setup(t, goodManifest)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	p, ok := r.Get("logs")
	if !ok || p.Kind() != "external" || len(p.Commands()) != 2 {
		t.Fatalf("%v %v", p, ok)
	}
	env := Env{Runner: fr, FS: m, User: "anna"}
	fr.Handler = func(c host.Cmd) (host.Result, error) { return host.Result{Stdout: []byte("строка 1")}, nil }
	// не хватает пакета jq
	_, err := p.Run(context.Background(), env, "tail", []string{"caddy"})
	pe, _ := err.(*Error)
	if pe == nil || pe.Code != "precondition_failed" || pe.Hint != "lite package install jq" {
		t.Fatalf("%v", err)
	}
	m.Files["/jq"] = []byte("x")
	out, err := p.Run(context.Background(), env, "tail", []string{"caddy"})
	if err != nil || out.Text != "строка 1\n" {
		t.Fatalf("%+v %v", out, err)
	}
	c := fr.Calls[0]
	if c.Name != "/etc/lite-deploy/plugins/logs/bin/tail.sh" || strings.Join(c.Args, " ") != "caddy" {
		t.Fatalf("%+v", c)
	}
	if strings.Join(c.Env, ",") != "LITE_PLUGIN=logs,LITE_PLUGIN_DIR=/etc/lite-deploy/plugins/logs,LITE_USER=anna" {
		t.Fatalf("%v", c.Env)
	}
	if _, err := p.Run(context.Background(), env, "nope", nil); err == nil {
		t.Fatal("неизвестная команда")
	}
}

func TestRunFailure(t *testing.T) {
	m, r, _, fr := setup(t, goodManifest)
	m.Files["/jq"] = []byte("x")
	p, _ := r.Get("logs")
	fr.Handler = func(c host.Cmd) (host.Result, error) {
		return host.Result{ExitCode: 3, Stderr: []byte("нет доступа")}, nil
	}
	_, err := p.Run(context.Background(), Env{Runner: fr, FS: m}, "clear", nil)
	if pe, _ := err.(*Error); pe == nil || pe.Code != "step_failed" || !strings.Contains(pe.Message, "нет доступа") {
		t.Fatalf("%v", err)
	}
}

func TestChecks(t *testing.T) {
	m, r, _, fr := setup(t, goodManifest)
	p, _ := r.Get("logs")
	env := Env{Runner: fr, FS: m}
	fr.Handler = func(c host.Cmd) (host.Result, error) {
		return host.Result{ExitCode: 1, Stdout: []byte("журналы 5 ГБ\nвторая строка")}, nil
	}
	fs := p.Check(context.Background(), env)
	if len(fs) != 2 || fs[0].Check != "requires" || fs[0].Level != LevelFail {
		t.Fatalf("%+v", fs)
	}
	if fs[1].Level != LevelWarn || fs[1].Message != "журналы 5 ГБ" || fs[1].Fix != "lite logs clear" {
		t.Fatalf("%+v", fs[1])
	}
	m.Files["/jq"] = []byte("x")
	for code, want := range map[int]string{0: LevelOK, 1: LevelWarn, 2: LevelFail} {
		fr.Handler = func(c host.Cmd) (host.Result, error) { return host.Result{ExitCode: code}, nil }
		fs := p.Check(context.Background(), env)
		if len(fs) != 1 || fs[0].Level != want {
			t.Errorf("код %d: %+v", code, fs)
		}
	}
}

func TestBadManifests(t *testing.T) {
	bad := map[string]string{
		"не JSON":            `{`,
		"чужое поле":         `{"name":"logs","commands":[{"name":"a","exec":"a.sh"}],"extra":1}`,
		"занятое имя":        `{"name":"deploy","commands":[{"name":"a","exec":"a.sh"}]}`,
		"другое имя":         `{"name":"other","commands":[{"name":"a","exec":"a.sh"}]}`,
		"путь наружу":        `{"name":"logs","commands":[{"name":"a","exec":"../a.sh"}]}`,
		"абсолютный путь":    `{"name":"logs","commands":[{"name":"a","exec":"/bin/sh"}]}`,
		"нет команд":         `{"name":"logs"}`,
		"повтор команды":     `{"name":"logs","commands":[{"name":"a","exec":"a"},{"name":"a","exec":"b"}]}`,
		"плохой пакет":       `{"name":"logs","requires":["a b"],"commands":[{"name":"a","exec":"a.sh"}]}`,
		"плохое имя команды": `{"name":"logs","commands":[{"name":"A b","exec":"a.sh"}]}`,
	}
	for name, mf := range bad {
		_, r, errs, _ := setup(t, mf)
		if len(errs) != 1 || len(r.List()) != 0 {
			t.Errorf("%s: должен отклоняться, ошибки %v", name, errs)
		}
	}
}

func TestDirOwnershipAndBrokenPlugin(t *testing.T) {
	m, _, _, _ := setup(t, goodManifest)
	m.Perms["/etc/lite-deploy/plugins/logs"] = 0o777 // доступен для записи всем
	_ = m.MkdirAll("/etc/lite-deploy/plugins/mine", 0o755)
	m.Files["/etc/lite-deploy/plugins/mine/plugin.json"] = []byte(goodManifest)
	_ = m.Chown("/etc/lite-deploy/plugins/mine", "anna", "anna")
	m.Dirs["/etc/lite-deploy/plugins/logs"] = 0o777
	r := NewRegistry()
	errs := LoadDir(m, Dir, r, nil)
	if len(errs) != 2 || len(r.List()) != 0 {
		t.Fatalf("плагины с небезопасным каталогом не загружаются: %v", errs)
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(&external{m: Manifest{Name: "ingress"}}); err != nil {
		t.Fatal(err)
	}
	if r.Add(&external{m: Manifest{Name: "ingress"}}) == nil || r.Add(&external{m: Manifest{Name: "exec"}}) == nil || r.Add(&external{m: Manifest{Name: "Bad"}}) == nil {
		t.Fatal("повтор, занятое и неверное имя отклоняются")
	}
	if ValidArgs([]string{"a\nb"}) || ValidArgs(make([]string, 17)) || !ValidArgs([]string{"ok", ""}) {
		t.Fatal("проверка аргументов")
	}
}
