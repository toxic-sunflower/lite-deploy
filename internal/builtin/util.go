package builtin

import (
	"context"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/pkgmgr"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// tools общие действия встроенных плагинов: запуск команд и пакеты.
type tools struct {
	ctx context.Context
	env plugin.Env
}

func (t tools) run(name string, args ...string) (host.Result, error) {
	return t.env.Runner.Run(t.ctx, host.Cmd{Name: name, Args: args, Timeout: 5 * time.Minute})
}

func (t tools) must(name string, args ...string) error {
	res, err := t.run(name, args...)
	if err != nil {
		return plugin.Errorf("step_failed", "%s: %v", name, err)
	}
	if res.ExitCode != 0 {
		return plugin.Errorf("step_failed", "%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(res.Stderr)+" "+string(res.Stdout)))
	}
	return nil
}

func (t tools) ok(name string, args ...string) bool {
	res, err := t.run(name, args...)
	return err == nil && res.ExitCode == 0
}

func (t tools) pm() *pkgmgr.Manager {
	return &pkgmgr.Manager{Runner: t.env.Runner, FS: t.env.FS, Now: t.env.Now, By: t.env.User}
}

func (t tools) install(names ...string) error {
	if err := t.pm().Install(t.ctx, names...); err != nil {
		return pkgErr(err)
	}
	return nil
}

func (t tools) manager() string {
	m, _ := t.pm().Detect(t.ctx)
	return m
}

func finding(plug, check, level, msg, fix string) plugin.Finding {
	return plugin.Finding{Plugin: plug, Check: check, Level: level, Message: msg, Fix: fix}
}

// textOut вывод с текстом и данными {"output": текст}.
func textOut(text string) plugin.Output {
	return plugin.Output{Text: text, Data: map[string]any{"output": strings.TrimSpace(text)}}
}

func noArgs(args []string, cmd, plug string) error {
	if len(args) != 0 {
		return plugin.Errorf("usage", "использование: lite %s %s", plug, cmd)
	}
	return nil
}

// Say получает сообщения о ходе установки; может быть nil.
type Say func(format string, a ...any)

func (s Say) say(format string, a ...any) {
	if s != nil {
		s(format, a...)
	}
}
