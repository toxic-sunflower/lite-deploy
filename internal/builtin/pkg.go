// Package builtin holds the built-in lite plugins.
package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/pkgmgr"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// Pkg is the `package` plugin: system packages (R-18, R-21, R-22).
type Pkg struct{}

func (Pkg) Name() string       { return "package" }
func (Pkg) Kind() string       { return "builtin" }
func (Pkg) Requires() []string { return nil }
func (Pkg) Description() string {
	return "system packages: install, remove, update, check, journal"
}
func (Pkg) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "list", Args: "[--all]", Help: "packages installed through lite (--all: every installed package), with the newer version where one is available"},
		{Name: "search", Help: "find packages by name or description (reads only)", Args: "WORDS"},
		{Name: "check", Help: "whether a package is installed and its version", Args: "PACKAGE"},
		{Name: "install", Help: "install packages", Args: "PACKAGE…", Confirm: true},
		{Name: "update", Help: "update packages (--all: every installed package)", Args: "PACKAGE…|--all", Confirm: true},
		{Name: "remove", Help: "remove packages (the ssh server, sudo, systemd and the shell cannot be removed)", Args: "PACKAGE…", Confirm: true},
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
		all := len(args) == 1 && args[0] == "--all"
		if len(args) != 0 && !all {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite package list [--all]")
		}
		es, err := m.List()
		if err != nil {
			return plugin.Output{}, pkgErr(err)
		}
		upd, uerr := m.Upgradable(ctx)
		if uerr != nil {
			upd = map[string]string{} // the list is still useful without the marks
		}
		type row struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			InstalledAt string `json:"installed_at,omitempty"`
			Lite        bool   `json:"lite"`
			Update      string `json:"update,omitempty"`
		}
		rows := []row{}
		byLite := map[string]bool{}
		for _, e := range es {
			byLite[e.Name] = true
			if !all {
				rows = append(rows, row{Name: e.Name, Version: e.Version, InstalledAt: e.InstalledAt, Lite: true, Update: upd[e.Name]})
			}
		}
		if all {
			every, err := m.ListAll(ctx)
			if err != nil {
				return plugin.Output{}, pkgErr(err)
			}
			for _, p := range every {
				rows = append(rows, row{Name: p.Name, Version: p.Version, Lite: byLite[p.Name], Update: upd[p.Name]})
			}
		}
		var sb strings.Builder
		if len(rows) == 0 {
			sb.WriteString("No packages have been installed through lite yet.\n")
		}
		for _, r := range rows {
			note := ""
			if r.Update != "" {
				note = "  update: " + r.Update
			}
			fmt.Fprintf(&sb, "%-30s %-20s%s\n", r.Name, r.Version, note)
		}
		return plugin.Output{Text: sb.String(), Data: map[string]any{"packages": rows}}, nil
	case "search":
		if len(args) == 0 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite package search WORDS")
		}
		found, err := m.Search(ctx, strings.Join(args, " "))
		if err != nil {
			return plugin.Output{}, pkgErr(err)
		}
		var sb strings.Builder
		for _, f := range found {
			fmt.Fprintf(&sb, "%-30s %s\n", f.Name, f.Summary)
		}
		if len(found) == 0 {
			sb.WriteString("Nothing found.\n")
		}
		return plugin.Output{Text: sb.String(), Data: map[string]any{"found": found}}, nil
	case "check":
		if len(args) != 1 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite package check PACKAGE")
		}
		in, err := m.Check(ctx, args[0])
		if err != nil {
			return plugin.Output{}, pkgErr(err)
		}
		text := fmt.Sprintf("%s: not installed\n", in.Name)
		if in.Installed {
			by := "not installed through lite"
			if in.ByLite {
				by = "installed through lite"
			}
			text = fmt.Sprintf("%s: installed, version %s (%s)\n", in.Name, in.Version, by)
		}
		return plugin.Output{Text: text, Data: in}, nil
	case "install", "update", "remove":
		if cmd == "update" && len(args) == 1 && args[0] == "--all" {
			if err := m.UpdateAll(ctx); err != nil {
				return plugin.Output{}, pkgErr(err)
			}
			return textOut("Every package is updated.\n"), nil
		}
		if len(args) == 0 {
			return plugin.Output{}, plugin.Errorf("usage", "usage: lite package %s PACKAGE…", cmd)
		}
		var err error
		verb := map[string]string{"install": "installed", "update": "updated", "remove": "removed"}[cmd]
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
		return plugin.Output{Text: fmt.Sprintf("Packages %s: %s\n", verb, strings.Join(args, ", ")), Data: map[string]any{"packages": args, "action": cmd}}, nil
	}
	return plugin.Output{}, plugin.Errorf("usage", "plugin package has no command %q", cmd)
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
	return []plugin.Finding{{Plugin: "package", Check: "manager", Level: plugin.LevelOK, Message: "package manager: " + mgr}}
}
