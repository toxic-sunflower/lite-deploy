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
	"github.com/toxic-sunflower/lite-deploy/internal/render"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

func init() {
	handlers[actions.SetSecretName] = setSecret
	handlers[actions.RegistryLoginName] = registryLogin
	handlers[actions.WriteReleaseAuthName] = writeReleaseAuth
	handlers[actions.WriteServiceEnvName] = writeServiceEnv
}

func secretKey(project, name string) string { return project + "/" + name }

// getSecret reads a value; in dry-run a secret that an earlier step will write counts as "will be".
func (r *run) getSecret(project, name string) (val []byte, pending bool, err error) {
	v, err := r.Secrets.Get(project, name)
	if err == nil {
		if !r.dry {
			// every read of a secret as root is written to the journal: the name, never the value
			_ = r.journal("", "", "secret_read", secretKey(project, name))
		}
		return v, false, nil
	}
	if errors.Is(err, secrets.ErrNotFound) && strings.HasPrefix(name, secrets.SharedPrefix) {
		return nil, false, fmt.Errorf("the shared secret %s is not set: add it in the interface (Projects, Shared secrets)", strings.TrimPrefix(name, secrets.SharedPrefix))
	}
	if errors.Is(err, secrets.ErrNotFound) && r.dry && r.wouldSet[secretKey(project, name)] {
		return nil, true, nil
	}
	return nil, false, err
}

// setSecret (SPEC-v0.md, 4.3): decrypts and writes the value to the store.
func setSecret(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.SetSecret)
	if strings.HasPrefix(p.Name, "_") {
		return outcome{}, fmt.Errorf("the internal name %s cannot be set from a plan", p.Name)
	}
	key, err := r.FS.ReadFile(r.KeyPath)
	if err != nil {
		return outcome{}, fmt.Errorf("the executor key could not be read (run lite-deploy init): %w", err)
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

// newPassword is 24 random bytes, base64 without the characters / + =.
func (r *run) newPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := r.Rand.Read(b); err != nil {
		return "", err
	}
	s := base64.StdEncoding.EncodeToString(b)
	return strings.NewReplacer("/", "A", "+", "B", "=", "C").Replace(s), nil
}

type authFile struct {
	Auths map[string]struct {
		Auth string `json:"auth"`
	} `json:"auths"`
}

// registryLogin (SPEC-v0.md, 4.3): podman login as the project user, the token on standard input.
func registryLogin(ctx context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.RegistryLogin)
	token, pending, err := r.getSecret(p.Project, p.Secret)
	if err != nil {
		return outcome{}, fmt.Errorf("registry token: %w", err)
	}
	if pending {
		return outcome{changed: true, detail: "the token will be written by an earlier step"}, nil
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
		return outcome{}, fmt.Errorf("podman login exited with code %d: %s", res.ExitCode, trimErr(res))
	}
	if err := r.FS.Chmod(authPath, 0o600); err != nil {
		return outcome{}, err
	}
	return outcome{changed: true}, nil
}

// writeServiceEnv (SPEC-v0.md, 4.3): the service env file from literals, secrets and the database connection string.
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
				return outcome{}, fmt.Errorf("secret %s for %s: %w", e.Secret, e.Name, err)
			}
			if pending {
				unknown = true
				continue
			}
			lines = append(lines, render.EnvLine{Name: e.Name, Value: string(val)})
		}
	}
	path := home(p.Project) + "/.config/lite-deploy/env/" + p.Service + ".env"
	if unknown {
		return outcome{changed: true, detail: "depends on secrets that earlier steps will write"}, nil
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
	return "the old file was saved: " + bak
}

// writeReleaseAuth: the Authorization header for downloading a release; a 600 file owned by the project user.
func writeReleaseAuth(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.WriteReleaseAuth)
	token, pending, err := r.getSecret(p.Project, p.Secret)
	if err != nil {
		return outcome{}, fmt.Errorf("release token: %w", err)
	}
	if pending {
		return outcome{changed: true, detail: "the token will be written by an earlier step"}, nil
	}
	if strings.ContainsAny(string(token), "\r\n\x00") {
		return outcome{}, errors.New("the release token contains a line break")
	}
	path := home(p.Project) + "/.config/lite-deploy/release-" + p.Service + ".hdr"
	changed, _, err := r.ensureFile(fileSpec{path: path, data: []byte("Authorization: Bearer " + string(token) + "\n"), perm: 0o600, owner: p.Project, group: p.Project})
	if err != nil {
		return outcome{}, err
	}
	return outcome{changed: changed}, nil
}
