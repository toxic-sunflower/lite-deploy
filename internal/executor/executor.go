// Package executor applies a validated plan to the system: one handler per action
// (SPEC-v0.md, section 4). Each handler compares the wanted state with the current one and changes only the difference;
// in dry-run mode it changes nothing and reports what it would do.
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

// Step states.
const (
	StateChanged     = "changed"
	StateUnchanged   = "unchanged"
	StateSkipped     = "skipped"
	StateFailed      = "failed"
	StateWouldChange = "would_change"
	StateWouldFail   = "would_fail"
)

// Paths on the server (SPEC-v0.md, section 9). Absolute: tests replace the file system, not the paths.
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

// Env is everything external the executor depends on.
type Env struct {
	Runner   host.Runner
	FS       host.FS
	HTTP     host.HTTPClient
	Secrets  *secrets.Store
	KeyPath  string // the executor private key
	StateDir string // the state directory; empty: plan.DefaultStateDir
	Rand     io.Reader
	Now      func() time.Time
	UID      int // uid of the caller, for the journal
	// ProjectJSON is the project description (optional); the caller has checked its hash against the plan project_hash. record_project copies it to /etc/lite-deploy/projects.
	ProjectJSON []byte
}

// StepResult is the outcome of one step.
type StepResult struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Report is the outcome of applying.
type Report struct {
	PlanID   string       `json:"plan_id"`
	PlanHash string       `json:"plan_hash"`
	DryRun   bool         `json:"dry_run"`
	Steps    []StepResult `json:"steps"`
	// Failed is the id of the first failed step (with dry-run: the first would_fail); empty if there are no errors.
	Failed string `json:"failed,omitempty"`
}

// outcome is the result of an action handler.
type outcome struct {
	changed bool
	detail  string
}

// run is the state of one apply: what the earlier steps have already done.
type run struct {
	Env
	pl      *plan.Plan
	dry     bool
	managed bool // the project already has a management mark: the existing files are considered ours
	// what earlier steps changed or would change (needs dry-run, where steps write nothing)
	wouldSet     map[string]bool // the "project/name" of the secrets that will be written
	unitsChanged bool
	sitesChanged bool
	undo         []siteUndo // to restore the site files when the Caddy check fails
	expectUnits  map[string][]string
	noPodman     bool
}

type siteUndo struct {
	path    string
	prev    []byte
	existed bool
	mode    fs.FileMode
}

type handler func(ctx context.Context, r *run, params any) (outcome, error)

var handlers = map[string]handler{}

// Apply executes a plan. The plan has already passed plan.Verify. The first error stops applying (except dry-run):
// the following steps get the state skipped. A journal error before work starts is returned as an error.
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
	// Plan preview: what earlier steps need to know about later ones.
	for _, s := range pl.Steps {
		switch s.Action {
		case actions.VerifyHostName:
			v, _ := actions.Decode(s.Action, s.Params)
			if vh, ok := v.(*actions.VerifyHost); ok {
				r.noPodman = vh.NoPodman
			}
		case actions.InstallUnitName:
			v, _ := actions.Decode(s.Action, s.Params)
			if q, ok := v.(*actions.InstallUnit); ok {
				r.expectUnits[q.Project] = append(r.expectUnits[q.Project], q.Project+"-"+q.Service+"-"+q.Slot+".service")
			}
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
			return nil, fmt.Errorf("the journal is unavailable, applying did not start: %w", err)
		}
	}
	stop := false
	for _, s := range pl.Steps {
		res := StepResult{ID: s.ID, Action: s.Action}
		if stop {
			res.State = StateSkipped
			res.Detail = "the previous step failed"
			rep.Steps = append(rep.Steps, res)
			continue
		}
		if !dry {
			if err := r.journal(s.ID, s.Action, "start", ""); err != nil {
				res.State, res.Error = StateFailed, "the journal is unavailable: "+err.Error()
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
			err = fmt.Errorf("no handler for action %s", s.Action)
		case len(issues) > 0:
			err = fmt.Errorf("parameters did not pass validation: %s", issues[0].Message)
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

// journal appends a line to the journal (SPEC-v0.md, section 9). There are no secret values in the record.
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

// ---- shared helpers ----

func home(user string) string { return "/home/" + user }

func exists(f host.FS, p string) (bool, error) {
	_, err := f.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// fileSpec is the wanted state of a file.
type fileSpec struct {
	path  string
	data  []byte
	perm  fs.FileMode
	owner string
	group string
}

// ensureFile brings a file to the wanted state. It returns true if the file was changed (or would be changed with dry).
// Before replacing a foreign file (the project is not managed yet) a copy .bak-<plan id> is saved.
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

// ensureDir creates a directory if it is missing, with an owner and mode. An existing directory is not touched.
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

// asUser runs a command in the project user's systemd session. For rootless podman a plain sudo -u does not work
// (there is no XDG_RUNTIME_DIR or session), so systemd-run --machine=<user>@.host --user is used (D-53).
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

// must runs a command and turns a non-zero exit code into an error.
func (r *run) must(ctx context.Context, c host.Cmd) (host.Result, error) {
	res, err := r.Runner.Run(ctx, c)
	if err != nil {
		return res, fmt.Errorf("%s: %w", c.Name, err)
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("%s exited with code %d: %s", c.Name, res.ExitCode, trimErr(res))
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
