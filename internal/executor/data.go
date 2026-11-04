package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/render"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

func init() {
	handlers[actions.ProvisionPostgresName] = provisionPostgres
	handlers[actions.SetSecretName] = setSecret
	handlers[actions.RegistryLoginName] = registryLogin
	handlers[actions.WriteServiceEnvName] = writeServiceEnv
}

const dbPasswordName = "_db_password"

func secretKey(project, name string) string { return project + "/" + name }

// getSecret читает значение; в dry-run секрет, который запишет предыдущий шаг, считается «будет».
func (r *run) getSecret(project, name string) (val []byte, pending bool, err error) {
	v, err := r.Secrets.Get(project, name)
	if err == nil {
		return v, false, nil
	}
	if errors.Is(err, secrets.ErrNotFound) && r.dry && r.wouldSet[secretKey(project, name)] {
		return nil, true, nil
	}
	return nil, false, err
}

// setSecret (SPEC-v0.md, 4.3): расшифровывает и пишет значение в хранилище.
func setSecret(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.SetSecret)
	if strings.HasPrefix(p.Name, "_") && p.Name != dbPasswordName {
		return outcome{}, fmt.Errorf("служебное имя %s из плана задавать нельзя", p.Name)
	}
	key, err := r.FS.ReadFile(r.KeyPath)
	if err != nil {
		return outcome{}, fmt.Errorf("ключ исполнителя не прочитан (выполните lite-deploy init): %w", err)
	}
	val, err := secrets.Decrypt(string(key), p.Ciphertext, p.Project, p.Name)
	if err != nil {
		return outcome{}, err
	}
	if r.dry {
		cur, gerr := r.Secrets.Get(p.Project, p.Name)
		if gerr == nil && string(cur) == string(val) {
			return outcome{}, nil
		}
		r.wouldSet[secretKey(p.Project, p.Name)] = true
		return outcome{changed: true}, nil
	}
	changed, err := r.Secrets.Put(p.Project, p.Name, val)
	if err != nil {
		return outcome{}, err
	}
	return outcome{changed: changed}, nil
}

var pgHbaLocalAll = regexp.MustCompile(`^\s*local\s+all\s+all\s+`)

func (r *run) psql(ctx context.Context, db, query string, stdin []byte, env []string) (host.Result, error) {
	args := []string{"-u", "postgres", "--", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-tA", "-d", db}
	if query != "" {
		args = append(args, "-c", query)
	}
	return r.must(ctx, host.Cmd{Name: "runuser", Args: args, Stdin: stdin, Env: env})
}

// newPassword 24 случайных байта, base64 без символов / + =.
func (r *run) newPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := r.Rand.Read(b); err != nil {
		return "", err
	}
	s := base64.StdEncoding.EncodeToString(b)
	return strings.NewReplacer("/", "A", "+", "B", "=", "C").Replace(s), nil
}

// provisionPostgres (SPEC-v0.md, 4.3 и 4.2): роль, база, правило pg_hba для пары, проверка входа.
func provisionPostgres(ctx context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.ProvisionPostgres)
	role := project.RoleName(p.Project)
	var did []string

	res, err := r.psql(ctx, "postgres", fmt.Sprintf("SELECT 1 FROM pg_roles WHERE rolname='%s'", role), nil, nil)
	if err != nil {
		return outcome{}, err
	}
	roleExists := strings.TrimSpace(string(res.Stdout)) == "1"
	res, err = r.psql(ctx, "postgres", fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", role), nil, nil)
	if err != nil {
		return outcome{}, err
	}
	dbExists := strings.TrimSpace(string(res.Stdout)) == "1"

	saved, pending, gerr := r.getSecret(p.Project, dbPasswordName)
	if gerr != nil && !errors.Is(gerr, secrets.ErrNotFound) {
		return outcome{}, gerr
	}
	haveSecret := gerr == nil
	var password string

	switch {
	case !roleExists:
		did = append(did, "роль")
		password = ""
		if !r.dry {
			if password, err = r.newPassword(); err != nil {
				return outcome{}, err
			}
			script := "\\set pw '" + password + "'\n" + fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD :'pw';\n", role)
			if _, err := r.psql(ctx, "postgres", "", []byte(script), nil); err != nil {
				return outcome{}, err
			}
		}
	case p.RotatePassword:
		did = append(did, "пароль")
		if !r.dry {
			if password, err = r.newPassword(); err != nil {
				return outcome{}, err
			}
			script := "\\set pw '" + password + "'\n" + fmt.Sprintf("ALTER ROLE %s LOGIN PASSWORD :'pw';\n", role)
			if _, err := r.psql(ctx, "postgres", "", []byte(script), nil); err != nil {
				return outcome{}, err
			}
		}
	case !haveSecret && !pending:
		return outcome{}, errors.New("adopt_password_unknown: роль есть, но её пароль неизвестен; задайте секрет _db_password или rotate_password")
	default:
		password = string(saved)
	}
	if !dbExists {
		did = append(did, "база")
		if !r.dry {
			if _, err := r.psql(ctx, "postgres", fmt.Sprintf("CREATE DATABASE %s OWNER %s", role, role), nil, nil); err != nil {
				return outcome{}, err
			}
		}
	}
	if !dbExists || !roleExists {
		if !r.dry {
			if _, err := r.psql(ctx, "postgres", fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM PUBLIC", role), nil, nil); err != nil {
				return outcome{}, err
			}
		}
	}
	if password != "" && !r.dry {
		if _, err := r.Secrets.Put(p.Project, dbPasswordName, []byte(password)); err != nil {
			return outcome{}, err
		}
	}
	if !roleExists || p.RotatePassword {
		r.wouldSet[secretKey(p.Project, dbPasswordName)] = true
	}

	hbaChanged, err := r.ensureHba(ctx, role)
	if err != nil {
		return outcome{}, err
	}
	if hbaChanged {
		did = append(did, "pg_hba")
	}

	// Проверка входа: пароль передаётся переменной окружения дочернего процесса, не аргументом.
	if !r.dry && password != "" || r.dry && roleExists && haveSecret && !p.RotatePassword {
		pw := password
		if r.dry {
			pw = string(saved)
		}
		dsn := fmt.Sprintf("host=/var/run/postgresql dbname=%s user=%s", role, role)
		if _, err := r.must(ctx, host.Cmd{Name: "psql", Args: []string{"-X", "-tA", "-c", "SELECT 1", dsn}, Env: []string{"PGPASSWORD=" + pw}}); err != nil {
			return outcome{}, fmt.Errorf("проверка входа роли %s: %w", role, err)
		}
	}
	if len(did) == 0 {
		return outcome{}, nil
	}
	return outcome{changed: true, detail: strings.Join(did, ", ")}, nil
}

// ensureHba держит строку «local ROLE ROLE scram-sha-256» между маркерами, выше общего правила local all all.
func (r *run) ensureHba(ctx context.Context, role string) (bool, error) {
	res, err := r.psql(ctx, "postgres", "SHOW hba_file", nil, nil)
	if err != nil {
		return false, err
	}
	path := strings.TrimSpace(string(res.Stdout))
	raw, err := r.FS.ReadFile(path)
	if err != nil {
		return false, err
	}
	begin, end := "# lite-deploy:begin "+role, "# lite-deploy:end "+role
	block := begin + "\nlocal   " + role + "   " + role + "   scram-sha-256\n" + end
	lines := strings.Split(string(raw), "\n")
	var out []string
	inBlock, found := false, false
	for _, l := range lines {
		switch {
		case strings.TrimSpace(l) == begin:
			inBlock, found = true, true
			out = append(out, block)
		case strings.TrimSpace(l) == end:
			inBlock = false
		case inBlock:
		default:
			out = append(out, l)
		}
	}
	if !found {
		at := -1
		for i, l := range out {
			if pgHbaLocalAll.MatchString(l) {
				at = i
				break
			}
		}
		if at < 0 {
			return false, errors.New("в pg_hba.conf нет общего правила «local all all»: не знаю, куда вставить правило роли")
		}
		out = append(out[:at], append([]string{block}, out[at:]...)...)
	}
	next := strings.Join(out, "\n")
	if next == string(raw) {
		return false, nil
	}
	if r.dry {
		return true, nil
	}
	info, err := r.FS.Stat(path)
	if err != nil {
		return false, err
	}
	if err := r.FS.WriteFile(path, []byte(next), info.Mode); err != nil {
		return false, err
	}
	if err := r.FS.Chown(path, info.Owner, info.Group); err != nil {
		return false, err
	}
	if _, err := r.must(ctx, hostCmd("systemctl", "reload", "postgresql")); err != nil {
		return false, err
	}
	return true, nil
}

type authFile struct {
	Auths map[string]struct {
		Auth string `json:"auth"`
	} `json:"auths"`
}

// registryLogin (SPEC-v0.md, 4.3): podman login от имени проектного пользователя, токен на стандартном вводе.
func registryLogin(ctx context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.RegistryLogin)
	token, pending, err := r.getSecret(p.Project, p.Secret)
	if err != nil {
		return outcome{}, fmt.Errorf("токен реестра: %w", err)
	}
	if pending {
		return outcome{changed: true, detail: "токен будет записан предыдущим шагом"}, nil
	}
	authPath := home(p.Project) + "/.config/lite-deploy/auth.json"
	want := base64.StdEncoding.EncodeToString([]byte(p.Username + ":" + string(token)))
	if raw, err := r.FS.ReadFile(authPath); err == nil {
		var af authFile
		if json.Unmarshal(raw, &af) == nil && af.Auths[p.Registry].Auth == want {
			return outcome{}, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return outcome{}, err
	}
	if r.dry {
		return outcome{changed: true}, nil
	}
	res, err := r.asUser(ctx, p.Project, token, "podman", "login", "--authfile", authPath, "--username", p.Username, "--password-stdin", p.Registry)
	if err != nil {
		return outcome{}, err
	}
	if res.ExitCode != 0 {
		return outcome{}, fmt.Errorf("podman login завершился с кодом %d: %s", res.ExitCode, trimErr(res))
	}
	if err := r.FS.Chmod(authPath, 0o600); err != nil {
		return outcome{}, err
	}
	return outcome{changed: true}, nil
}

// writeServiceEnv (SPEC-v0.md, 4.3): env-файл сервиса из литералов, секретов и строки подключения к базе.
func writeServiceEnv(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.WriteServiceEnv)
	var lines []render.EnvLine
	unknown := false
	for _, e := range p.Env {
		switch e.Kind {
		case "literal":
			lines = append(lines, render.EnvLine{Name: e.Name, Value: e.Value})
		case "secret":
			val, pending, err := r.getSecret(p.Project, e.Secret)
			if err != nil {
				return outcome{}, fmt.Errorf("секрет %s для %s: %w", e.Secret, e.Name, err)
			}
			if pending {
				unknown = true
				continue
			}
			lines = append(lines, render.EnvLine{Name: e.Name, Value: string(val)})
		case "database":
			role := project.RoleName(p.Project)
			pw, pending, err := r.getSecret(p.Project, dbPasswordName)
			if err != nil {
				return outcome{}, fmt.Errorf("пароль базы: %w", err)
			}
			if pending {
				unknown = true
				continue
			}
			s, err := render.DatabaseURL(e.Value, role, role, string(pw))
			if err != nil {
				return outcome{}, err
			}
			lines = append(lines, render.EnvLine{Name: e.Name, Value: s})
		}
	}
	path := home(p.Project) + "/.config/lite-deploy/env/" + p.Service + ".env"
	if unknown {
		return outcome{changed: true, detail: "зависит от секретов, которые запишут предыдущие шаги"}, nil
	}
	text, err := render.EnvFile(lines)
	if err != nil {
		return outcome{}, err
	}
	changed, bak, err := r.ensureFile(fileSpec{path: path, data: []byte(text), perm: 0o600, owner: p.Project, group: p.Project})
	if err != nil {
		return outcome{}, err
	}
	return outcome{changed: changed, detail: bakNote(bak)}, nil
}

func bakNote(bak string) string {
	if bak == "" {
		return ""
	}
	return "старый файл сохранён: " + bak
}
