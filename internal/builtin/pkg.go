// Package builtin встроенные плагины lite.
package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/pkgmgr"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// Pkg плагин `package`: системные пакеты (R-18, R-21, R-22).
type Pkg struct{}

func (Pkg) Name() string       { return "package" }
func (Pkg) Kind() string       { return "builtin" }
func (Pkg) Requires() []string { return nil }
func (Pkg) Description() string {
	return "системные пакеты: установка, удаление, обновление, проверка, журнал"
}
func (Pkg) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "list", Help: "пакеты, установленные через lite"},
		{Name: "check", Help: "установлен ли пакет и какой версии", Args: "ПАКЕТ"},
		{Name: "install", Help: "установить пакеты", Args: "ПАКЕТ…", Confirm: true},
		{Name: "update", Help: "обновить пакеты", Args: "ПАКЕТ…", Confirm: true},
		{Name: "remove", Help: "удалить пакеты (ssh-сервер, sudo, systemd и оболочку удалить нельзя)", Args: "ПАКЕТ…", Confirm: true},
	}
}

func pkgErr(err error) error {
	var pe *pkgmgr.Error
	if errors.As(err, &pe) {
		return &plugin.Error{Code: pe.Code, Message: pe.Message}
	}
	return plugin.Errorf("io_error", "%v", err)
}

func manager(env plugin.Env) *pkgmgr.Manager {
	return &pkgmgr.Manager{Runner: env.Runner, FS: env.FS, Now: env.Now, By: env.User}
}

func (Pkg) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	m := manager(env)
	switch cmd {
	case "list":
		if len(args) != 0 {
			return plugin.Output{}, plugin.Errorf("usage", "package list: аргументов нет")
		}
		es, err := m.List()
		if err != nil {
			return plugin.Output{}, pkgErr(err)
		}
		if es == nil {
			es = []pkgmgr.Entry{}
		}
		var sb strings.Builder
		if len(es) == 0 {
			sb.WriteString("Через lite пакеты пока не ставились.\n")
		}
		for _, e := range es {
			fmt.Fprintf(&sb, "%-30s %-20s %s\n", e.Name, e.Version, e.InstalledAt)
		}
		return plugin.Output{Text: sb.String(), Data: map[string]any{"packages": es}}, nil
	case "check":
		if len(args) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite package check ПАКЕТ")
		}
		in, err := m.Check(ctx, args[0])
		if err != nil {
			return plugin.Output{}, pkgErr(err)
		}
		text := fmt.Sprintf("%s: не установлен\n", in.Name)
		if in.Installed {
			by := "поставлен не через lite"
			if in.ByLite {
				by = "поставлен через lite"
			}
			text = fmt.Sprintf("%s: установлен, версия %s (%s)\n", in.Name, in.Version, by)
		}
		return plugin.Output{Text: text, Data: in}, nil
	case "install", "update", "remove":
		if len(args) == 0 {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite package %s ПАКЕТ…", cmd)
		}
		var err error
		verb := map[string]string{"install": "установлены", "update": "обновлены", "remove": "удалены"}[cmd]
		switch cmd {
		case "install":
			err = m.Install(ctx, args...)
		case "update":
			err = m.Update(ctx, args...)
		default:
			err = m.Remove(ctx, args...)
		}
		if err != nil {
			return plugin.Output{}, pkgErr(err)
		}
		return plugin.Output{Text: fmt.Sprintf("Пакеты %s: %s\n", verb, strings.Join(args, ", ")), Data: map[string]any{"packages": args, "action": cmd}}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "у плагина package нет команды %q", cmd)
}

func (Pkg) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	m := manager(env)
	mgr, err := m.Detect(ctx)
	if err != nil {
		return []plugin.Finding{{Plugin: "package", Check: "manager", Level: plugin.LevelFail, Message: err.Error()}}
	}
	if _, err := m.List(); err != nil {
		return []plugin.Finding{{Plugin: "package", Check: "journal", Level: plugin.LevelWarn, Message: err.Error()}}
	}
	return []plugin.Finding{{Plugin: "package", Check: "manager", Level: plugin.LevelOK, Message: "менеджер пакетов: " + mgr}}
}
