// Package executor применяет проверенный план к системе: по одному обработчику на действие
// (SPEC-v0.md, раздел 4). Каждый обработчик сравнивает желаемое с текущим состоянием и меняет только разницу;
// в режиме dry-run ничего не меняет и сообщает, что бы сделал.
package executor

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

// Состояния шага.
const (
	StateChanged     = "changed"
	StateUnchanged   = "unchanged"
	StateSkipped     = "skipped"
	StateFailed      = "failed"
	StateWouldChange = "would_change"
	StateWouldFail   = "would_fail"
)

// Пути на сервере (SPEC-v0.md, раздел 9). Абсолютные: в тестах подменяется файловая система, а не пути.
const (
	ConfDir      = "/etc/caddy/conf.d"
	UpstreamDir  = "/etc/caddy/upstreams"
	CaddyfilePth = "/etc/caddy/Caddyfile"
	CaddyLogDir  = "/var/log/caddy"
	SudoersDir   = "/etc/sudoers.d"
	LibDir       = "/var/lib/lite-deploy"
	ProjectsCopy = "/etc/lite-deploy/projects"
	JournalPath  = LibDir + "/journal.jsonl"
	OSRelease    = "/etc/os-release"
	SubUID       = "/etc/subuid"
	SubGID       = "/etc/subgid"
	LingerDir    = "/var/lib/systemd/linger"
	CaddyAPI     = "http://localhost:2019/config/"
)

// Env всё внешнее, от чего зависит исполнитель.
type Env struct {
	Runner   host.Runner
	FS       host.FS
	HTTP     host.HTTPClient
	Secrets  *secrets.Store
	KeyPath  string // закрытый ключ исполнителя
	StateDir string // каталог state; пусто: plan.DefaultStateDir
	Rand     io.Reader
	Now      func() time.Time
	UID      int // uid запустившего, для журнала
	// ProjectJSON описание проекта (необязательно); вызывающий сверил его хэш с project_hash плана. record_project копирует его в /etc/lite-deploy/projects.
	ProjectJSON []byte
}

// StepResult итог одного шага.
type StepResult struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Report итог применения.
type Report struct {
	PlanID   string       `json:"plan_id"`
	PlanHash string       `json:"plan_hash"`
	DryRun   bool         `json:"dry_run"`
	Steps    []StepResult `json:"steps"`
	// Failed это id первого упавшего шага (при dry-run: первого would_fail); пусто, если ошибок нет.
	Failed string `json:"failed,omitempty"`
}

// outcome результат обработчика действия.
type outcome struct {
	changed bool
	detail  string
}

// run состояние одного применения: что уже сделали предыдущие шаги.
type run struct {
	Env
	pl      *plan.Plan
	dry     bool
	managed bool // у проекта уже есть метка управления: существующие файлы считаются нашими
	// что предыдущие шаги изменили или изменили бы (нужно dry-run, где шаги ничего не пишут)
	wouldSet     map[string]bool // "проект/имя" секретов, которые будут записаны
	unitsChanged bool
	sitesChanged bool
	undo         []siteUndo // для отката файлов сайтов при ошибке проверки Caddy
	expectUnits  map[string][]string
	needPostgres bool
}

type siteUndo struct {
	path    string
	prev    []byte
	existed bool
	mode    fs.FileMode
}

type handler func(ctx context.Context, r *run, params any) (outcome, error)

var handlers = map[string]handler{}

// Apply выполняет план. План уже прошёл plan.Verify. Первая ошибка останавливает применение (кроме dry-run):
// последующие шаги получают состояние skipped. Ошибка журнала до начала работы возвращается как error.
func Apply(ctx context.Context, e Env, pl *plan.Plan, dry bool) (*Report, error) {
	if e.Rand == nil {
		e.Rand = rand.Reader
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.StateDir == "" {
		e.StateDir = plan.DefaultStateDir
	}
	r := &run{Env: e, pl: pl, dry: dry, wouldSet: map[string]bool{}, expectUnits: map[string][]string{}}
	if _, err := e.FS.Stat(path.Join(e.StateDir, "projects", pl.Project+".json")); err == nil {
		r.managed = true
	}
	// Предварительный просмотр плана: что нужно предыдущим шагам знать о последующих.
	for _, s := range pl.Steps {
		switch s.Action {
		case actions.ProvisionPostgresName:
			r.needPostgres = true
		case actions.InstallQuadletName:
			v, _ := actions.Decode(s.Action, s.Params)
			if q, ok := v.(*actions.InstallQuadlet); ok {
				r.expectUnits[q.Project] = append(r.expectUnits[q.Project], q.Project+"-"+q.Service+"-"+q.Slot+".service")
			}
		}
	}

	rep := &Report{PlanID: pl.ID, PlanHash: pl.Hash, DryRun: dry}
	if !dry {
		if err := r.journal("", "", "run_start", ""); err != nil {
			return nil, fmt.Errorf("журнал недоступен, применение не начато: %w", err)
		}
	}
	stop := false
	for _, s := range pl.Steps {
		res := StepResult{ID: s.ID, Action: s.Action}
		if stop {
			res.State = StateSkipped
			res.Detail = "предыдущий шаг завершился ошибкой"
			rep.Steps = append(rep.Steps, res)
			continue
		}
		if !dry {
			if err := r.journal(s.ID, s.Action, "start", ""); err != nil {
				res.State, res.Error = StateFailed, "журнал недоступен: "+err.Error()
				rep.Steps = append(rep.Steps, res)
				rep.Failed = s.ID
				stop = true
				continue
			}
		}
		h, ok := handlers[s.Action]
		params, issues := actions.Decode(s.Action, s.Params)
		var out outcome
		var err error
		switch {
		case !ok:
			err = fmt.Errorf("нет обработчика действия %s", s.Action)
		case len(issues) > 0:
			err = fmt.Errorf("параметры не прошли проверку: %s", issues[0].Message)
		default:
			out, err = h(ctx, r, params)
		}
		res.Detail = out.detail
		switch {
		case err != nil && dry:
			res.State, res.Error = StateWouldFail, err.Error()
		case err != nil:
			res.State, res.Error = StateFailed, err.Error()
		case out.changed && dry:
			res.State = StateWouldChange
		case out.changed:
			res.State = StateChanged
		default:
			res.State = StateUnchanged
		}
		if err != nil {
			if rep.Failed == "" {
				rep.Failed = s.ID
			}
			if !dry {
				stop = true
			}
		}
		if !dry {
			_ = r.journal(s.ID, s.Action, res.State, res.Error)
		}
		rep.Steps = append(rep.Steps, res)
	}
	if !dry {
		state := "ok"
		if rep.Failed != "" {
			state = "failed"
		}
		_ = r.journal("", "", "run_end:"+state, "")
	}
	return rep, nil
}

// journal дописывает строку в журнал (SPEC-v0.md, раздел 9). Значений секретов в записи нет.
func (r *run) journal(step, action, state, errMsg string) error {
	line := map[string]any{"ts": r.Now().UTC().Format(time.RFC3339), "plan_id": r.pl.ID, "plan_hash": r.pl.Hash,
		"project": r.pl.Project, "uid": r.UID, "state": state}
	if step != "" {
		line["step_id"], line["action"] = step, action
	}
	if errMsg != "" {
		line["error"] = errMsg
	}
	b, err := json.Marshal(line)
	if err != nil {
		return err
	}
	if err := r.FS.MkdirAll(LibDir, 0o755); err != nil {
		return err
	}
	return r.FS.Append(JournalPath, append(b, '\n'), 0o600)
}

// ---- общие помощники ----

func home(user string) string { return "/home/" + user }

func exists(f host.FS, p string) (bool, error) {
	_, err := f.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// fileSpec желаемое состояние файла.
type fileSpec struct {
	path  string
	data  []byte
	perm  fs.FileMode
	owner string
	group string
}

// ensureFile приводит файл к желаемому виду. Возвращает true, если файл изменён (или был бы изменён при dry).
// Перед заменой чужого файла (проект ещё не под управлением) сохраняется копия .bak-<id плана>.
func (r *run) ensureFile(s fileSpec) (changed bool, backup string, err error) {
	info, serr := r.FS.Stat(s.path)
	switch {
	case serr == nil:
		cur, rerr := r.FS.ReadFile(s.path)
		if rerr != nil {
			return false, "", rerr
		}
		same := string(cur) == string(s.data)
		if same && info.Mode == s.perm && info.Owner == s.owner && (s.group == "" || info.Group == s.group) {
			return false, "", nil
		}
		if r.dry {
			return true, "", nil
		}
		if !same && !r.managed {
			backup = s.path + ".bak-" + r.pl.ID
			if err := r.FS.WriteFile(backup, cur, info.Mode); err != nil {
				return false, "", err
			}
			if err := r.FS.Chown(backup, info.Owner, info.Group); err != nil {
				return false, "", err
			}
		}
	case errors.Is(serr, fs.ErrNotExist):
		if r.dry {
			return true, "", nil
		}
	default:
		return false, "", serr
	}
	if err := r.FS.WriteFile(s.path, s.data, s.perm); err != nil {
		return false, "", err
	}
	if err := r.FS.Chown(s.path, s.owner, s.group); err != nil {
		return false, "", err
	}
	return true, backup, nil
}

// ensureDir создаёт каталог, если его нет, с владельцем и правами. Существующий каталог не трогает.
func (r *run) ensureDir(p string, perm fs.FileMode, owner, group string) (bool, error) {
	ok, err := exists(r.FS, p)
	if err != nil || ok {
		return false, err
	}
	if r.dry {
		return true, nil
	}
	if err := r.FS.MkdirAll(p, perm); err != nil {
		return false, err
	}
	return true, r.FS.Chown(p, owner, group)
}

// asUser запускает команду в сессии пользовательского systemd проекта. Для rootless podman обычный sudo -u не годится
// (нет XDG_RUNTIME_DIR и сессии), поэтому используется systemd-run --machine=<user>@.host --user (D-53).
func (r *run) asUser(ctx context.Context, user string, stdin []byte, name string, args ...string) (host.Result, error) {
	a := append([]string{"--machine=" + user + "@.host", "--user", "--pipe", "--wait", "--quiet", "--collect", "--", name}, args...)
	return r.Runner.Run(ctx, host.Cmd{Name: "systemd-run", Args: a, Stdin: stdin, Timeout: 120 * time.Second})
}

func trimErr(res host.Result) string {
	s := strings.TrimSpace(string(res.Stderr))
	if s == "" {
		s = strings.TrimSpace(string(res.Stdout))
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// must запускает команду и превращает ненулевой код выхода в ошибку.
func (r *run) must(ctx context.Context, c host.Cmd) (host.Result, error) {
	res, err := r.Runner.Run(ctx, c)
	if err != nil {
		return res, fmt.Errorf("%s: %w", c.Name, err)
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("%s завершилась с кодом %d: %s", c.Name, res.ExitCode, trimErr(res))
	}
	return res, nil
}

func joinDetail(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}
