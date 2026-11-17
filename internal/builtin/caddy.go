package builtin

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plugin"
)

// Пути Caddy.
const (
	caddyfile    = "/etc/caddy/Caddyfile"
	caddyConfDir = "/etc/caddy/conf.d"
	caddyMark    = "# managed-by: lite-deploy"
)

// Caddy плагин `caddy`: входящий трафик (HTTP) для проектов.
type Caddy struct{}

func (Caddy) Name() string       { return "caddy" }
func (Caddy) Kind() string       { return "builtin" }
func (Caddy) Requires() []string { return nil }
func (Caddy) Description() string {
	return "входящий трафик: веб-сервер Caddy, подключает сайты проектов из " + caddyConfDir
}
func (Caddy) Commands() []plugin.Command {
	return []plugin.Command{
		{Name: "install", Help: "поставить Caddy и главный Caddyfile, запустить службу", Confirm: true},
		{Name: "status", Help: "состояние Caddy и подключённые сайты"},
		{Name: "validate", Help: "проверить конфигурацию Caddy"},
		{Name: "reload", Help: "перечитать конфигурацию без остановки сайтов", Confirm: true},
		{Name: "sites", Help: "сайты проектов (файлы в " + caddyConfDir + ")"},
	}
}

// InstallCaddy ставит Caddy (на Debian и Ubuntu из официального репозитория) и главный Caddyfile,
// который подключает сайты проектов из /etc/caddy/conf.d, затем запускает службу.
func InstallCaddy(ctx context.Context, env plugin.Env, say Say) error {
	t := tools{ctx, env}
	m := t.manager()
	if m == "" {
		return plugin.Errorf("precondition_failed", "не найден менеджер пакетов (apt, dnf, zypper, pacman)")
	}
	if !t.ok("caddy", "version") {
		say.say("Плагин caddy: ставлю Caddy...")
		switch m {
		case "apt":
			if err := t.caddyApt(); err != nil {
				return err
			}
		case "dnf":
			if err := t.must("dnf", "install", "-y", "-q", "dnf-command(copr)"); err != nil {
				return err
			}
			if err := t.must("dnf", "copr", "enable", "-y", "@caddy/caddy"); err != nil {
				return err
			}
			if err := t.install("caddy"); err != nil {
				return err
			}
		default:
			if err := t.install("caddy"); err != nil {
				return err
			}
		}
	}
	if err := env.FS.MkdirAll(caddyConfDir, fs.FileMode(0o755)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	old, rerr := env.FS.ReadFile(caddyfile)
	if rerr != nil || !strings.Contains(string(old), caddyMark) {
		if rerr == nil {
			_ = env.FS.WriteFile(fmt.Sprintf("%s.bak.%s", caddyfile, time.Now().Format("20060102150405")), old, fs.FileMode(0o644))
		}
		body := caddyMark + "\n# Каждый проект добавляет свой файл " + caddyConfDir + "/<проект>.caddy. Не правьте этот файл руками.\nimport " + caddyConfDir + "/*.caddy\n"
		if err := env.FS.WriteFile(caddyfile, []byte(body), fs.FileMode(0o644)); err != nil {
			return plugin.Errorf("io_error", "%v", err)
		}
	}
	const placeholder = caddyConfDir + "/00-placeholder.caddy"
	if _, err := env.FS.Stat(placeholder); err != nil {
		if err := env.FS.WriteFile(placeholder, []byte("# заглушка: чтобы маска import всегда находила файл\n"), fs.FileMode(0o644)); err != nil {
			return plugin.Errorf("io_error", "%v", err)
		}
	}
	if err := t.must("caddy", "validate", "--config", caddyfile, "--adapter", "caddyfile"); err != nil {
		return plugin.Errorf("step_failed", "главный Caddyfile не прошёл проверку: %v", err)
	}
	if err := t.must("systemctl", "enable", "--now", "caddy"); err != nil {
		return err
	}
	return t.must("systemctl", "reload", "caddy")
}

// caddyApt подключает официальный репозиторий Caddy и ставит пакет (как в официальной инструкции).
func (t tools) caddyApt() error {
	if err := t.install("debian-keyring", "debian-archive-keyring", "apt-transport-https", "curl", "gnupg"); err != nil {
		return err
	}
	get := func(url string) ([]byte, error) {
		res, err := t.env.Runner.Run(t.ctx, host.Cmd{Name: "curl", Args: []string{"-fsSL", "--proto", "=https", "--tlsv1.2", url}, Timeout: 2 * time.Minute})
		if err != nil {
			return nil, plugin.Errorf("step_failed", "curl: %v", err)
		}
		if res.ExitCode != 0 {
			return nil, plugin.Errorf("step_failed", "не удалось скачать %s: %s", url, strings.TrimSpace(string(res.Stderr)))
		}
		return res.Stdout, nil
	}
	key, err := get("https://dl.cloudsmith.io/public/caddy/stable/gpg.key")
	if err != nil {
		return err
	}
	const keyring = "/usr/share/keyrings/caddy-stable-archive-keyring.gpg"
	res, err := t.env.Runner.Run(t.ctx, host.Cmd{Name: "gpg", Args: []string{"--dearmor", "--yes", "-o", keyring}, Stdin: key})
	if err != nil {
		return plugin.Errorf("step_failed", "gpg: %v", err)
	}
	if res.ExitCode != 0 {
		return plugin.Errorf("step_failed", "gpg --dearmor: %s", strings.TrimSpace(string(res.Stderr)))
	}
	list, err := get("https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt")
	if err != nil {
		return err
	}
	const listFile = "/etc/apt/sources.list.d/caddy-stable.list"
	if err := t.env.FS.WriteFile(listFile, list, fs.FileMode(0o644)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	if err := t.env.FS.Chmod(keyring, fs.FileMode(0o644)); err != nil {
		return plugin.Errorf("io_error", "%v", err)
	}
	return t.install("caddy")
}

func (Caddy) Run(ctx context.Context, env plugin.Env, cmd string, args []string) (plugin.Output, error) {
	t := tools{ctx, env}
	if err := noArgs(args, cmd, "caddy"); err != nil {
		return plugin.Output{}, err
	}
	switch cmd {
	case "install":
		if err := InstallCaddy(ctx, env, nil); err != nil {
			return plugin.Output{}, err
		}
		return textOut("Caddy установлен и запущен.\n"), nil
	case "validate", "reload", "status", "sites":
		if !t.ok("caddy", "version") {
			e := plugin.Errorf("precondition_failed", "Caddy не установлен")
			e.Hint = "sudo lite caddy install"
			return plugin.Output{}, e
		}
	default:
		return plugin.Output{}, plugin.Errorf("usage", "у плагина caddy нет команды %q", cmd)
	}
	switch cmd {
	case "validate":
		if err := t.must("caddy", "validate", "--config", caddyfile, "--adapter", "caddyfile"); err != nil {
			return plugin.Output{}, err
		}
		return textOut("Конфигурация верна.\n"), nil
	case "reload":
		if err := t.must("caddy", "validate", "--config", caddyfile, "--adapter", "caddyfile"); err != nil {
			return plugin.Output{}, plugin.Errorf("step_failed", "конфигурация не прошла проверку, Caddy не тронут: %v", err)
		}
		if err := t.must("systemctl", "reload", "caddy"); err != nil {
			return plugin.Output{}, err
		}
		return textOut("Конфигурация перечитана.\n"), nil
	case "sites":
		sites := t.sites()
		return plugin.Output{Text: strings.Join(append(sites, ""), "\n"), Data: map[string]any{"sites": sites}}, nil
	}
	res, _ := t.run("caddy", "version")
	active := t.ok("systemctl", "is-active", "--quiet", "caddy")
	sites := t.sites()
	text := fmt.Sprintf("Caddy %s, служба %s, сайтов: %d\n", strings.TrimSpace(string(res.Stdout)), map[bool]string{true: "работает", false: "не работает"}[active], len(sites))
	return plugin.Output{Text: text, Data: map[string]any{"version": strings.TrimSpace(string(res.Stdout)), "active": active, "sites": sites}}, nil
}

func (t tools) sites() []string {
	names, _ := t.env.FS.ReadDir(caddyConfDir)
	sites := []string{}
	for _, n := range names {
		if s, ok := strings.CutSuffix(n, ".caddy"); ok && !strings.HasPrefix(n, "00-") {
			sites = append(sites, s)
		}
	}
	sort.Strings(sites)
	return sites
}

func (Caddy) Check(ctx context.Context, env plugin.Env) []plugin.Finding {
	t := tools{ctx, env}
	if !t.ok("caddy", "version") {
		return []plugin.Finding{finding("caddy", "installed", plugin.LevelOK, "Caddy не установлен (не используется)", "")}
	}
	var out []plugin.Finding
	if t.ok("systemctl", "is-active", "--quiet", "caddy") {
		out = append(out, finding("caddy", "service", plugin.LevelOK, "служба Caddy работает", ""))
	} else {
		out = append(out, finding("caddy", "service", plugin.LevelFail, "служба Caddy не работает", "sudo systemctl enable --now caddy"))
	}
	if t.ok("caddy", "validate", "--config", caddyfile, "--adapter", "caddyfile") {
		out = append(out, finding("caddy", "config", plugin.LevelOK, "конфигурация Caddy верна", ""))
	} else {
		out = append(out, finding("caddy", "config", plugin.LevelFail, "конфигурация Caddy не проходит проверку", "sudo lite caddy validate"))
	}
	if b, err := env.FS.ReadFile(caddyfile); err != nil || !strings.Contains(string(b), caddyMark) {
		out = append(out, finding("caddy", "caddyfile", plugin.LevelWarn, "главный Caddyfile не от lite: сайты проектов могут не подключаться", "sudo lite caddy install"))
	}
	return out
}
