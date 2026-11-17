package setup

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Пути Caddy.
const (
	caddyfile    = "/etc/caddy/Caddyfile"
	caddyConfDir = "/etc/caddy/conf.d"
	caddyMark    = "# managed-by: lite-deploy"
)

// installCaddy ставит Caddy (на Debian и Ubuntu из официального репозитория) и главный Caddyfile,
// который подключает сайты проектов из /etc/caddy/conf.d, затем запускает службу.
func (e Env) installCaddy(ctx context.Context, m string) error {
	if res, err := e.run(ctx, "caddy", "version"); err != nil || res.ExitCode != 0 {
		switch m {
		case "apt":
			if err := e.caddyApt(ctx); err != nil {
				return err
			}
		case "dnf":
			if err := e.must(ctx, "dnf", "install", "-y", "-q", "dnf-command(copr)"); err != nil {
				return err
			}
			if err := e.must(ctx, "dnf", "copr", "enable", "-y", "@caddy/caddy"); err != nil {
				return err
			}
			if err := e.pkgInstallWith(ctx, m, "caddy"); err != nil {
				return err
			}
		default:
			if err := e.pkgInstallWith(ctx, m, "caddy"); err != nil {
				return err
			}
		}
	}
	if err := e.FS.MkdirAll(caddyConfDir, fs.FileMode(0o755)); err != nil {
		return err
	}
	old, rerr := e.FS.ReadFile(caddyfile)
	if rerr != nil || !strings.Contains(string(old), caddyMark) {
		if rerr == nil {
			_ = e.FS.WriteFile(fmt.Sprintf("%s.bak.%s", caddyfile, time.Now().Format("20060102150405")), old, fs.FileMode(0o644))
		}
		body := caddyMark + "\n# Каждый проект добавляет свой файл " + caddyConfDir + "/<проект>.caddy. Не правьте этот файл руками.\nimport " + caddyConfDir + "/*.caddy\n"
		if err := e.FS.WriteFile(caddyfile, []byte(body), fs.FileMode(0o644)); err != nil {
			return err
		}
	}
	const placeholder = caddyConfDir + "/00-placeholder.caddy"
	if _, err := e.FS.Stat(placeholder); err != nil {
		if err := e.FS.WriteFile(placeholder, []byte("# заглушка: чтобы маска import всегда находила файл\n"), fs.FileMode(0o644)); err != nil {
			return err
		}
	}
	if err := e.must(ctx, "caddy", "validate", "--config", caddyfile, "--adapter", "caddyfile"); err != nil {
		return fmt.Errorf("Caddyfile не прошёл проверку: %w", err)
	}
	if err := e.must(ctx, "systemctl", "enable", "--now", "caddy"); err != nil {
		return err
	}
	return e.must(ctx, "systemctl", "reload", "caddy")
}

// caddyApt подключает официальный репозиторий Caddy и ставит пакет (как в официальной инструкции).
func (e Env) caddyApt(ctx context.Context) error {
	if err := e.pkgInstallWith(ctx, "apt", "debian-keyring", "debian-archive-keyring", "apt-transport-https", "curl", "gnupg"); err != nil {
		return err
	}
	get := func(url string) ([]byte, error) {
		res, err := e.Runner.Run(ctx, host.Cmd{Name: "curl", Args: []string{"-fsSL", "--proto", "=https", "--tlsv1.2", url}, Timeout: 2 * time.Minute})
		if err != nil {
			return nil, err
		}
		if res.ExitCode != 0 {
			return nil, fmt.Errorf("не удалось скачать %s: %s", url, strings.TrimSpace(string(res.Stderr)))
		}
		return res.Stdout, nil
	}
	key, err := get("https://dl.cloudsmith.io/public/caddy/stable/gpg.key")
	if err != nil {
		return err
	}
	const keyring = "/usr/share/keyrings/caddy-stable-archive-keyring.gpg"
	res, err := e.Runner.Run(ctx, host.Cmd{Name: "gpg", Args: []string{"--dearmor", "--yes", "-o", keyring}, Stdin: key})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("gpg --dearmor: %s", strings.TrimSpace(string(res.Stderr)))
	}
	list, err := get("https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt")
	if err != nil {
		return err
	}
	const listFile = "/etc/apt/sources.list.d/caddy-stable.list"
	if err := e.FS.WriteFile(listFile, list, fs.FileMode(0o644)); err != nil {
		return err
	}
	if err := e.FS.Chmod(keyring, fs.FileMode(0o644)); err != nil {
		return err
	}
	return e.pkgInstallWith(ctx, "apt", "caddy")
}
