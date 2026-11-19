package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
	"github.com/toxic-sunflower/lite-deploy/internal/servers"
)

// Server плагин `server`: реестр других серверов (имя и адрес), на которые ссылаются проекты.
type Server struct{}

func (Server) Name() string       { return "server" }
func (Server) Kind() string       { return "builtin" }
func (Server) Requires() []string { return nil }
func (Server) Description() string {
	return "реестр серверов: имя и адрес, на которые ссылаются ресурсы проектов (например, база на другом сервере)"
}
func (Server) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "list", Help: "известные серверы"},
		{Name: "add", Confirm: true, Args: "ИМЯ АДРЕС", Help: "добавить или изменить сервер (адрес во внутренней сети)"},
		{Name: "remove", Confirm: true, Args: "ИМЯ", Help: "убрать сервер из реестра"},
	}
}

func (Server) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	m, err := servers.Load(env.FS)
	if err != nil {
		return plugin.Output{}, plugin.Errorf("step_failed", "%v", err)
	}
	switch cmd {
	case "list":
		if len(args) != 0 {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite server list")
		}
		var sb strings.Builder
		for _, n := range servers.Names(m) {
			fmt.Fprintf(&sb, "%s\t%s\n", n, m[n])
		}
		if sb.Len() == 0 {
			sb.WriteString("Серверов нет.\n")
		}
		return plugin.Output{Text: sb.String(), Data: map[string]any{"servers": m}}, nil
	case "add":
		if len(args) != 2 || !servers.ValidName(args[0]) {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite server add ИМЯ АДРЕС (имя: строчные буквы, цифры, «-», не «local»)")
		}
		if err := servers.ValidAddress(args[1]); err != nil {
			return plugin.Output{}, plugin.Errorf("usage", "%v", err)
		}
		m[args[0]] = args[1]
		if err := servers.Save(env.FS, m); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "запись реестра: %v", err)
		}
		return textOut("Сервер " + args[0] + " → " + args[1] + " записан.\n"), nil
	case "remove":
		if len(args) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "использование: lite server remove ИМЯ")
		}
		if _, ok := m[args[0]]; !ok {
			return plugin.Output{}, plugin.Errorf("precondition_failed", "сервера %s нет в реестре", args[0])
		}
		delete(m, args[0])
		if err := servers.Save(env.FS, m); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "запись реестра: %v", err)
		}
		return textOut("Сервер " + args[0] + " убран из реестра.\n"), nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "у плагина server нет команды %q", cmd)
}

func (Server) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	if _, err := servers.Load(env.FS); err != nil {
		return []plugin.Finding{finding("server", "registry", plugin.LevelFail, err.Error(), "")}
	}
	return nil
}
