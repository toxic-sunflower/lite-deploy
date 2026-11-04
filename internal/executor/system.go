package executor

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
)

func init() {
	handlers[actions.VerifyHostName] = verifyHost
	handlers[actions.CreateProjectUserName] = createProjectUser
}

var versionRe = regexp.MustCompile(`^(\d+)\.(\d+)`)

// verifyHost только читает (SPEC-v0.md, 4.3). Список проблем собирается целиком.
func verifyHost(ctx context.Context, r *run, _ any) (outcome, error) {
	var problems, warns []string
	osr, err := r.FS.ReadFile(OSRelease)
	if err != nil {
		problems = append(problems, "не прочитан "+OSRelease)
	} else {
		kv := parseOSRelease(string(osr))
		m := versionRe.FindStringSubmatch(kv["VERSION_ID"])
		if kv["ID"] != "ubuntu" || m == nil || atoi(m[1]) < 24 {
			problems = append(problems, fmt.Sprintf("нужна Ubuntu 24.04 или новее, найдено %s %s", kv["ID"], kv["VERSION_ID"]))
		}
	}
	tools := [][]string{{"podman", "--version"}, {"caddy", "version"}, {"loginctl", "--version"}, {"visudo", "--version"}}
	if r.needPostgres {
		tools = append(tools, []string{"psql", "--version"})
	}
	for _, t := range tools {
		res, err := r.Runner.Run(ctx, hostCmd(t[0], t[1:]...))
		if err != nil || res.ExitCode != 0 {
			problems = append(problems, t[0]+" не установлен или не запускается")
			continue
		}
		if t[0] == "podman" {
			m := regexp.MustCompile(`version (\d+)\.(\d+)`).FindStringSubmatch(string(res.Stdout))
			switch {
			case m == nil:
				problems = append(problems, "не удалось определить версию podman: "+strings.TrimSpace(string(res.Stdout)))
			case atoi(m[1]) < 4 || atoi(m[1]) == 4 && atoi(m[2]) < 4:
				problems = append(problems, "нужен podman 4.4 или новее (Quadlet): "+strings.TrimSpace(string(res.Stdout)))
			case atoi(m[1]) < 5:
				warns = append(warns, "podman "+m[1]+"."+m[2]+": проверено только на 5.x")
			}
		}
	}
	if res, err := r.Runner.Run(ctx, hostCmd("ufw", "status")); err != nil || !strings.Contains(string(res.Stdout), "Status: active") {
		warns = append(warns, "ufw не активен")
	}
	if cf, err := r.FS.ReadFile(CaddyfilePth); err != nil {
		problems = append(problems, "не прочитан "+CaddyfilePth)
	} else if !strings.Contains(string(cf), "import "+ConfDir+"/*") {
		problems = append(problems, "главный Caddyfile не содержит import "+ConfDir+"/*")
	}
	if ok, _ := exists(r.FS, ConfDir); !ok {
		problems = append(problems, "нет каталога "+ConfDir)
	}
	if resp, err := r.HTTP.Get(ctx, CaddyAPI, ""); err != nil || resp.Status >= 500 {
		problems = append(problems, "API Caddy на localhost:2019 недоступен (нужен для reload)")
	}
	if len(problems) > 0 {
		return outcome{}, fmt.Errorf("precondition_failed: %s", strings.Join(problems, "; "))
	}
	return outcome{detail: joinDetail(warns...)}, nil
}

func parseOSRelease(s string) map[string]string {
	kv := map[string]string{}
	for _, l := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			kv[k] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return kv
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// createProjectUser (SPEC-v0.md, 4.3): пользователь без пароля и sudo, linger, права, subuid/subgid, каталоги.
func createProjectUser(ctx context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.CreateProjectUser)
	u, h := p.Project, home(p.Project)
	var did []string
	res, err := r.Runner.Run(ctx, hostCmd("id", "-u", u))
	if err != nil {
		return outcome{}, err
	}
	userExists := res.ExitCode == 0
	if !userExists {
		did = append(did, "пользователь")
		if !r.dry {
			if _, err := r.must(ctx, hostCmd("adduser", "--disabled-password", "--gecos", "", u)); err != nil {
				return outcome{}, err
			}
		}
	}
	if ok, _ := exists(r.FS, LingerDir+"/"+u); !ok {
		did = append(did, "linger")
		if !r.dry {
			if _, err := r.must(ctx, hostCmd("loginctl", "enable-linger", u)); err != nil {
				return outcome{}, err
			}
		}
	}
	if userExists || !r.dry {
		if info, err := r.FS.Stat(h); err != nil {
			return outcome{}, fmt.Errorf("нет домашнего каталога %s: %w", h, err)
		} else if info.Mode != 0o750 {
			did = append(did, "права "+h)
			if !r.dry {
				if err := r.FS.Chmod(h, 0o750); err != nil {
					return outcome{}, err
				}
			}
		}
	}
	if !r.dry || userExists {
		for _, f := range []string{SubUID, SubGID} {
			b, err := r.FS.ReadFile(f)
			if err != nil || !strings.Contains("\n"+string(b), "\n"+u+":") {
				return outcome{}, fmt.Errorf("для %s нет диапазона в %s (нужен для rootless podman)", u, f)
			}
		}
	}
	for _, d := range []string{".config", ".config/containers", ".config/containers/systemd", ".config/lite-deploy", ".config/lite-deploy/env", ".local", ".local/state", ".local/state/lite-deploy"} {
		perm := fsMode(0o755)
		if d == ".config/lite-deploy/env" || d == ".local/state/lite-deploy" || d == ".config/lite-deploy" {
			perm = 0o700
		}
		made, err := r.ensureDir(h+"/"+d, perm, u, u)
		if err != nil {
			return outcome{}, err
		}
		if made {
			did = append(did, "каталог "+d)
		}
	}
	if len(did) == 0 {
		return outcome{}, nil
	}
	return outcome{changed: true, detail: strings.Join(did, ", ")}, nil
}
