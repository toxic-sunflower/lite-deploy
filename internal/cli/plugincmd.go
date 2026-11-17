package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/builtin"
	"github.com/toxic-sunflower/lite-deploy/internal/pkgmgr"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

const pluginUsage = "использование: lite plugin list | lite plugin run ПЛАГИН КОМАНДА [аргументы] | lite ПЛАГИН КОМАНДА [аргументы]"

// registry собирает встроенные и внешние плагины. Ошибки загрузки внешних плагинов возвращаются отдельно.
func registry(env Env) (*plugin.Registry, []error) {
	r := plugin.NewRegistry()
	for _, p := range []plugin.Plugin{builtin.Pkg{}} {
		if err := r.Add(p); err != nil {
			panic(err) // ошибка в самой программе: встроенные имена фиксированы
		}
	}
	dir := env.PluginDir
	if dir == "" {
		dir = plugin.Dir
	}
	installed := func(ctx context.Context, name string) bool {
		m := &pkgmgr.Manager{Runner: env.Runner, FS: env.FS}
		in, err := m.Check(ctx, name)
		return err == nil && in.Installed
	}
	return r, plugin.LoadDir(env.FS, dir, r, installed)
}

func (e Env) pluginEnv() plugin.Env {
	user := ""
	if e.Getenv != nil {
		user = e.Getenv("SUDO_USER")
	}
	return plugin.Env{Runner: e.Runner, FS: e.FS, Now: e.Now, User: user}
}

func pluginFail(command string, err error) result {
	var pe *plugin.Error
	if errors.As(err, &pe) {
		return result{command: command, err: &ErrorBody{Code: pe.Code, Message: pe.Message, Hint: pe.Hint}}
	}
	return fail(command, CodeInternal, err.Error())
}

// confirm спрашивает подтверждение в терминале. Без терминала и без --yes команда отказывает.
func confirm(env Env, question string) bool {
	if env.Interactive == nil || !env.Interactive() {
		return false
	}
	_, _ = fmt.Fprintf(env.Stderr, "%s [y/N]: ", question)
	line, _ := bufio.NewReader(env.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes" || a == "д" || a == "да"
}

// cmdPluginRun выполняет команду плагина: lite ПЛАГИН КОМАНДА [аргументы] [--yes].
func cmdPluginRun(name string, args []string, env Env) result {
	command := name
	r, _ := registry(env)
	p, ok := r.Get(name)
	if !ok {
		return fail(command, CodeUsage, fmt.Sprintf("нет плагина %q (список: lite plugin list)", name))
	}
	yes := false
	var rest []string
	for _, a := range args {
		if a == "--yes" {
			yes = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		return fail(command, CodeUsage, "использование: lite "+name+" КОМАНДА [аргументы]; команды: "+commandNames(p))
	}
	cmd, cargs := rest[0], rest[1:]
	command = name + "." + cmd
	c, ok := plugin.Find(p, cmd)
	if !ok {
		return fail(command, CodeUsage, fmt.Sprintf("у плагина %s нет команды %q; есть: %s", name, cmd, commandNames(p)))
	}
	if !plugin.ValidArgs(cargs) {
		return fail(command, CodeUsage, "неверные аргументы: без управляющих символов, не длиннее 256 знаков, не больше 16")
	}
	if c.Confirm {
		if !env.root() {
			return fail(command, "permission_denied", "команда меняет систему и требует root: sudo lite "+name+" "+cmd+" …")
		}
		if !yes && !confirm(env, fmt.Sprintf("Выполнить: lite %s %s %s?", name, cmd, strings.Join(cargs, " "))) {
			return fail(command, "confirmation_required", "это изменит систему; повторите с --yes")
		}
	}
	out, err := p.Run(context.Background(), env.pluginEnv(), cmd, cargs)
	if err != nil {
		return pluginFail(command, err)
	}
	return result{command: command, data: out.Data, text: out.Text}
}

func commandNames(p plugin.Plugin) string {
	var n []string
	for _, c := range p.Commands() {
		n = append(n, c.Name)
	}
	return strings.Join(n, ", ")
}

// cmdPlugin: lite plugin list | run.
func cmdPlugin(args []string, env Env) result {
	if len(args) == 0 {
		return fail("plugin", CodeUsage, pluginUsage)
	}
	switch args[0] {
	case "list":
		return cmdInfo(env, "plugin.list")
	case "run":
		if len(args) < 2 {
			return fail("plugin.run", CodeUsage, pluginUsage)
		}
		return cmdPluginRun(args[1], args[2:], env)
	}
	return fail("plugin", CodeUsage, pluginUsage)
}

type pluginInfo struct {
	Name        string           `json:"name"`
	Kind        string           `json:"kind"`
	Description string           `json:"description"`
	Requires    []string         `json:"requires"`
	Commands    []plugin.Command `json:"commands"`
}

// cmdInfo: lite info (плагины и их команды).
func cmdInfo(env Env, command string) result {
	r, errs := registry(env)
	var list []pluginInfo
	var sb strings.Builder
	fmt.Fprintf(&sb, "lite %s\nПлагины:\n", Version)
	for _, p := range r.List() {
		req := p.Requires()
		if req == nil {
			req = []string{}
		}
		list = append(list, pluginInfo{Name: p.Name(), Kind: p.Kind(), Description: p.Description(), Requires: req, Commands: p.Commands()})
		fmt.Fprintf(&sb, "  %-12s %-9s %s\n", p.Name(), p.Kind(), p.Description())
		for _, c := range p.Commands() {
			fmt.Fprintf(&sb, "      lite %s %s %s — %s\n", p.Name(), c.Name, c.Args, c.Help)
		}
	}
	var warns []Warning
	var msgs []string
	for _, e := range errs {
		warns = append(warns, Warning{Code: "plugin_load_failed", Message: e.Error()})
		msgs = append(msgs, e.Error())
	}
	if msgs == nil {
		msgs = []string{}
	}
	return result{command: command, data: map[string]any{"version": Version, "plugins": list, "load_errors": msgs}, text: sb.String(), warn: warns}
}

// cmdVerify: lite verify (только чтение): проверки всех плагинов.
func cmdVerify(env Env) result {
	r, errs := registry(env)
	ctx := context.Background()
	var all []plugin.Finding
	for _, p := range r.List() {
		all = append(all, p.Check(ctx, env.pluginEnv())...)
	}
	for _, e := range errs {
		all = append(all, plugin.Finding{Plugin: "plugin", Check: "load", Level: plugin.LevelFail, Message: e.Error()})
	}
	var sb strings.Builder
	counts := map[string]int{}
	for _, f := range all {
		counts[f.Level]++
		mark := map[string]string{plugin.LevelOK: "  ок  ", plugin.LevelWarn: "внимание", plugin.LevelFail: "ОШИБКА"}[f.Level]
		fmt.Fprintf(&sb, "[%s] %s/%s: %s\n", mark, f.Plugin, f.Check, f.Message)
		if f.Fix != "" && f.Level != plugin.LevelOK {
			fmt.Fprintf(&sb, "           исправить: %s\n", f.Fix)
		}
	}
	if all == nil {
		all = []plugin.Finding{}
	}
	data := map[string]any{"findings": all, "ok": counts[plugin.LevelOK], "warnings": counts[plugin.LevelWarn], "failures": counts[plugin.LevelFail]}
	fmt.Fprintf(&sb, "Итого: ок %d, предупреждений %d, ошибок %d\n", counts[plugin.LevelOK], counts[plugin.LevelWarn], counts[plugin.LevelFail])
	res := result{command: "verify", data: data, text: sb.String()}
	if counts[plugin.LevelFail] > 0 {
		res.err = &ErrorBody{Code: "verify_failed", Message: fmt.Sprintf("проверка нашла ошибок: %d", counts[plugin.LevelFail])}
	}
	return res
}
