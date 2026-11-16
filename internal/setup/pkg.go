package setup

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// pluginPackages пакеты плагина по менеджерам пакетов.
var pluginPackages = map[string]map[string][]string{
	"podman": {
		"apt": {"podman", "uidmap"}, "dnf": {"podman"}, "zypper": {"podman"}, "pacman": {"podman"},
	},
	"postgres": {
		"apt": {"postgresql"}, "dnf": {"postgresql-server"}, "zypper": {"postgresql-server"}, "pacman": {"postgresql"},
	},
}

func pluginNames() []string {
	var n []string
	for k := range pluginPackages {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// manager определяет менеджер пакетов системы.
func (e Env) manager(ctx context.Context) (string, error) {
	for _, m := range [][2]string{{"apt", "apt-get"}, {"dnf", "dnf"}, {"zypper", "zypper"}, {"pacman", "pacman"}} {
		if res, err := e.run(ctx, m[1], "--version"); err == nil && res.ExitCode == 0 {
			return m[0], nil
		}
	}
	return "", fmt.Errorf("не найден менеджер пакетов (apt, dnf, zypper, pacman)")
}

func (e Env) pkgInstall(ctx context.Context, pkgs ...string) error {
	m, err := e.manager(ctx)
	if err != nil {
		return err
	}
	return e.pkgInstallWith(ctx, m, pkgs...)
}

func (e Env) pkgInstallWith(ctx context.Context, m string, pkgs ...string) error {
	var cmds []host.Cmd
	long := 15 * time.Minute
	switch m {
	case "apt":
		cmds = []host.Cmd{
			{Name: "apt-get", Args: []string{"update", "-qq"}, Timeout: long},
			{Name: "apt-get", Args: append([]string{"install", "-y", "-qq"}, pkgs...), Env: []string{"DEBIAN_FRONTEND=noninteractive"}, Timeout: long},
		}
	case "dnf":
		cmds = []host.Cmd{{Name: "dnf", Args: append([]string{"install", "-y", "-q"}, pkgs...), Timeout: long}}
	case "zypper":
		cmds = []host.Cmd{{Name: "zypper", Args: append([]string{"--non-interactive", "install"}, pkgs...), Timeout: long}}
	case "pacman":
		cmds = []host.Cmd{{Name: "pacman", Args: append([]string{"-S", "--noconfirm", "--needed"}, pkgs...), Timeout: long}}
	default:
		return fmt.Errorf("неизвестный менеджер пакетов %q", m)
	}
	for _, c := range cmds {
		res, err := e.Runner.Run(ctx, c)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s %s: %s", c.Name, strings.Join(c.Args, " "), strings.TrimSpace(string(res.Stderr)))
		}
	}
	return nil
}

func (e Env) installPlugin(ctx context.Context, name string) error {
	m, err := e.manager(ctx)
	if err != nil {
		return err
	}
	pkgs := pluginPackages[name][m]
	if len(pkgs) == 0 {
		return fmt.Errorf("для менеджера пакетов %s список пакетов не задан", m)
	}
	return e.pkgInstallWith(ctx, m, pkgs...)
}
