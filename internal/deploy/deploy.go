package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/render"
)

// Deploy error codes (SPEC-v0.md, 6.3).
const (
	CodeProjectNotFound   = "project_not_found"
	CodeInProgress        = "deploy_in_progress"
	CodeUnhealthy         = "deploy_unhealthy"
	CodeFailed            = "deploy_failed"
	CodeNothingDeployed   = "nothing_deployed"
	CodeRollbackNA        = "rollback_unavailable"
	CodeCaddyReloadFailed = "caddy_reload_failed"
	CodeIO                = "io_error"
)

// Default paths.
const (
	DefaultProjectsDir = "/etc/lite-deploy/projects"
	DefaultStateDir    = "/var/lib/lite-deploy/state"
)

// Error is a deploy error with a code for the CLI.
type Error struct {
	Code    string
	Message string
	Hint    string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Env is the deploy environment. Everything external (commands, files, HTTP, locking, time) is replaced in tests.
type Env struct {
	Runner host.Runner
	FS     host.FS
	HTTP   host.HTTPClient
	Lock   host.Locker
	Now    func() time.Time
	Sleep  func(time.Duration)
	// AsUser: the process runs as root, podman and systemctl --user commands go to the project user's session.
	AsUser bool
	// Switch moves traffic to a slot through the root helper (a project user must not write Caddy configuration or reload
	// Caddy itself). It returns ErrNoHelper if the helper is not installed; then the old way is used.
	Switch      func(ctx context.Context, slot string) error
	ProjectsDir string // empty: DefaultProjectsDir
	StateDir    string // empty: DefaultStateDir
	Poll        time.Duration
}

// Manager deploys one project.
type Manager struct {
	Env
	Name  string
	proj  project.Project
	ports actions.Ports
}

var nameRe = regexp.MustCompile(`\A[a-z][a-z0-9-]{1,30}\z`)

// Slots are the two blue-green slots.
var Slots = [2]string{"blue", "green"}

func slotIndex(s string) int {
	if s == Slots[1] {
		return 1
	}
	return 0
}

func other(s string) string {
	if s == Slots[0] {
		return Slots[1]
	}
	return Slots[0]
}

// Open reads the project description and ports from the server state.
func Open(env Env, name string) (*Manager, *Error) {
	if !nameRe.MatchString(name) {
		return nil, fail(CodeProjectNotFound, "invalid project name")
	}
	if env.ProjectsDir == "" {
		env.ProjectsDir = DefaultProjectsDir
	}
	if env.StateDir == "" {
		env.StateDir = DefaultStateDir
	}
	if env.Poll == 0 {
		env.Poll = 2 * time.Second
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.Sleep == nil {
		env.Sleep = time.Sleep
	}
	raw, err := env.FS.ReadFile(env.ProjectsDir + "/" + name + ".json")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &Error{Code: CodeProjectNotFound, Message: "project " + name + " not found", Hint: "create it: lite-deploy project create"}
		}
		return nil, fail(CodeIO, err.Error())
	}
	m := &Manager{Env: env, Name: name}
	if err := json.Unmarshal(raw, &m.proj); err != nil {
		return nil, fail(CodeIO, "the project description is corrupt: "+err.Error())
	}
	if raw, err := env.FS.ReadFile(env.StateDir + "/projects/" + name + ".json"); err == nil {
		var st struct {
			Ports actions.Ports `json:"ports"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, fail(CodeIO, "the project state is corrupt: "+err.Error())
		}
		m.ports = st.Ports
	}
	if len(m.proj.Services) == 0 {
		return nil, fail(CodeIO, "the project description has no services")
	}
	return m, nil
}

func (m *Manager) services() []string {
	out := make([]string, 0, len(m.proj.Services))
	for n := range m.proj.Services {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) port(service, slot string) int {
	if p := m.ports.Services[service]; len(p) == 2 {
		return p[slotIndex(slot)]
	}
	if p := m.proj.Services[service].HostPorts; len(p) == 2 {
		return p[slotIndex(slot)]
	}
	return 0
}

func (m *Manager) home() string         { return "/home/" + m.Name }
func (m *Manager) userStateDir() string { return m.home() + "/.local/state/lite-deploy" }

// run runs a command in the project user's session (or directly, if it is already that user).
func (m *Manager) run(ctx context.Context, timeout time.Duration, name string, args ...string) (host.Result, error) {
	if m.AsUser {
		a := append([]string{"--machine=" + m.Name + "@.host", "--user", "--pipe", "--wait", "--quiet", "--collect", "--", name}, args...)
		return m.Runner.Run(ctx, host.Cmd{Name: "systemd-run", Args: a, Timeout: timeout})
	}
	return m.Runner.Run(ctx, host.Cmd{Name: name, Args: args, Timeout: timeout})
}

func trim(r host.Result) string {
	s := strings.TrimSpace(string(r.Stderr))
	if s == "" {
		s = strings.TrimSpace(string(r.Stdout))
	}
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func (m *Manager) ensureStateDir() error {
	d := m.userStateDir()
	if err := m.FS.MkdirAll(d, 0o700); err != nil {
		return err
	}
	if m.AsUser {
		// created as root, the directories would belong to root: give them back to the project user
		for _, p := range []string{m.home() + "/.local", m.home() + "/.local/state", d} {
			if err := m.FS.Chown(p, m.Name, m.Name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) writeState(name string, data []byte) error {
	p := m.userStateDir() + "/" + name
	if err := m.FS.WriteFile(p, data, 0o600); err != nil {
		return err
	}
	if m.AsUser {
		return m.FS.Chown(p, m.Name, m.Name)
	}
	return nil
}

// Active is the slot that receives traffic: blue, green or none.
func (m *Manager) Active() string {
	raw, err := m.FS.ReadFile(m.userStateDir() + "/active")
	if err != nil {
		return "none"
	}
	switch s := strings.TrimSpace(string(raw)); s {
	case "blue", "green":
		return s
	}
	return "none"
}

func (m *Manager) history(event, version, slot, prev string) {
	line := fmt.Sprintf("%s %s %s slot=%s previous=%s\n", m.Now().UTC().Format(time.RFC3339), event, version, slot, prev)
	perm := fs.FileMode(0o600)
	p := m.userStateDir() + "/history.log"
	_ = m.FS.Append(p, []byte(line), perm)
	if m.AsUser {
		_ = m.FS.Chown(p, m.Name, m.Name)
	}
}

// HistoryEntry is a line of the deploy journal.
type HistoryEntry struct {
	Time     string `json:"time"`
	Event    string `json:"event"`
	Version  string `json:"version"`
	Slot     string `json:"slot"`
	Previous string `json:"previous,omitempty"`
}

func (m *Manager) lastHistory(n int) []HistoryEntry {
	raw, err := m.FS.ReadFile(m.userStateDir() + "/history.log")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var out []HistoryEntry
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		e := HistoryEntry{Time: f[0], Event: f[1], Version: f[2]}
		for _, kv := range f[3:] {
			if v, ok := strings.CutPrefix(kv, "slot="); ok {
				e.Slot = v
			}
			if v, ok := strings.CutPrefix(kv, "previous="); ok {
				e.Previous = v
			}
		}
		out = append(out, e)
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// probe polls one service in a slot. It returns (answers, version).
func (m *Manager) probe(ctx context.Context, service, slot string) (bool, string) {
	svc := m.proj.Services[service]
	port := m.port(service, slot)
	if svc.Health == nil || svc.Health.Path == "" || port == 0 {
		res, err := m.run(ctx, 20*time.Second, "systemctl", "--user", "is-active", render.UnitName(m.Name, service, slot)+".service")
		if err != nil || res.ExitCode != 0 {
			return false, ""
		}
		return true, ""
	}
	resp, err := m.HTTP.Get(ctx, fmt.Sprintf("http://127.0.0.1:%d%s", port, svc.Health.Path), "")
	if err != nil || resp.Status != 200 {
		return false, ""
	}
	if svc.Health.VersionField == "" {
		return true, ""
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return false, ""
	}
	v, _ := body[svc.Health.VersionField].(string)
	return true, v
}

func (m *Manager) healthTimeout() time.Duration {
	t := 0
	for _, s := range m.proj.Services {
		if s.Health != nil && s.Health.TimeoutSeconds > t {
			t = s.Health.TimeoutSeconds
		}
	}
	if t == 0 {
		t = 90
	}
	return time.Duration(t) * time.Second
}

// DeployResult is the outcome of a deploy.
type DeployResult struct {
	Project      string `json:"project"`
	Version      string `json:"version"`
	Slot         string `json:"slot"`
	PreviousSlot string `json:"previous_slot"`
	Switched     bool   `json:"switched"`
}

// Deploy deploys version sha into the free slot and switches traffic (SPEC-v0.md, 5.2).
func (m *Manager) Deploy(ctx context.Context, sha string) (*DeployResult, *Error) {
	if !shaRe.MatchString(sha) {
		return nil, fail(CodeInvalidSHA, "the full commit hash is required: 40 characters 0-9a-f")
	}
	if err := m.ensureStateDir(); err != nil {
		return nil, fail(CodeIO, err.Error())
	}
	unlock, err := m.Lock.TryLock(m.userStateDir() + "/lock")
	if err != nil {
		if errors.Is(err, host.ErrLocked) {
			return nil, &Error{Code: CodeInProgress, Message: "another deploy of this project is still running", Hint: "wait and try again"}
		}
		return nil, fail(CodeIO, err.Error())
	}
	defer unlock()

	active := m.Active()
	idle := Slots[0]
	if active == Slots[0] {
		idle = Slots[1]
	}
	svcs := m.services()

	authArgs := []string{}
	if m.proj.Registry != nil {
		if ok, _ := fileExists(m.FS, m.home()+"/.config/lite-deploy/auth.json"); ok {
			authArgs = []string{"--authfile", m.home() + "/.config/lite-deploy/auth.json"}
		}
	}
	for _, s := range svcs {
		if m.proj.Services[s].KindOf() == project.KindUnit {
			if e := m.fetchRelease(ctx, s, sha, idle); e != nil {
				return nil, e
			}
			continue
		}
		img := m.proj.Services[s].Image + ":" + sha
		res, err := m.run(ctx, 10*time.Minute, "podman", append([]string{"pull"}, append(authArgs, img)...)...)
		if err != nil {
			return nil, fail(CodeFailed, "podman pull "+img+": "+err.Error())
		}
		if res.ExitCode != 0 {
			return nil, fail(CodeFailed, "could not download the image "+img+": "+trim(res))
		}
		local := "localhost/" + m.Name + "-" + s + ":" + idle
		if res, err := m.run(ctx, time.Minute, "podman", "tag", img, local); err != nil || res.ExitCode != 0 {
			return nil, fail(CodeFailed, "podman tag "+local+": "+errText(res, err))
		}
	}
	for _, s := range svcs {
		unit := render.UnitName(m.Name, s, idle) + ".service"
		if res, err := m.run(ctx, 2*time.Minute, "systemctl", "--user", "restart", unit); err != nil || res.ExitCode != 0 {
			return nil, m.abort(ctx, idle, sha, active, svcs, "could not start "+unit+": "+errText(res, err))
		}
	}

	deadline := m.Now().Add(m.healthTimeout())
	ready := map[string]bool{}
	for {
		for _, s := range svcs {
			if ready[s] {
				continue
			}
			ok, ver := m.probe(ctx, s, idle)
			if ok && (m.proj.Services[s].Health == nil || m.proj.Services[s].Health.VersionField == "" || ver == sha) {
				ready[s] = true
			}
		}
		if len(ready) == len(svcs) {
			break
		}
		if !m.Now().Before(deadline) {
			var bad []string
			for _, s := range svcs {
				if !ready[s] {
					bad = append(bad, s)
				}
			}
			return nil, m.abort(ctx, idle, sha, active, bad, "the services did not become ready in time: "+strings.Join(bad, ", "))
		}
		m.Sleep(m.Poll)
	}

	if e := m.switchTraffic(ctx, idle); e != nil {
		return nil, e
	}
	if err := m.writeState("active", []byte(idle+"\n")); err != nil {
		return nil, fail(CodeIO, "traffic was switched but active was not written: "+err.Error())
	}
	m.history("deploy", sha, idle, active)
	return &DeployResult{Project: m.Name, Version: sha, Slot: idle, PreviousSlot: active, Switched: true}, nil
}

func errText(r host.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return trim(r)
}

// abort: the last journal lines, stopping the slot, a record, an error. Traffic stays on the previous slot.
func (m *Manager) abort(ctx context.Context, idle, sha, active string, logsFor []string, msg string) *Error {
	var logs []string
	for _, s := range logsFor {
		logCmd := []string{"podman", "logs", "--tail", "40", render.UnitName(m.Name, s, idle)}
		if m.proj.Services[s].KindOf() == project.KindUnit {
			logCmd = []string{"journalctl", "--user", "-u", render.UnitName(m.Name, s, idle) + ".service", "-n", "40", "--no-pager"}
		}
		if res, err := m.run(ctx, 30*time.Second, logCmd[0], logCmd[1:]...); err == nil {
			if t := strings.TrimSpace(string(res.Stdout) + string(res.Stderr)); t != "" {
				logs = append(logs, s+":\n"+t)
			}
		}
	}
	for _, s := range m.services() {
		_, _ = m.run(ctx, time.Minute, "systemctl", "--user", "stop", render.UnitName(m.Name, s, idle)+".service")
	}
	m.history("deploy-failed", sha, idle, active)
	e := &Error{Code: CodeUnhealthy, Message: msg, Hint: "traffic stayed on the previous version"}
	if len(logs) > 0 {
		e.Message += "\n" + strings.Join(logs, "\n")
	}
	return e
}

// switchTraffic writes the upstream of all sites to the slot ports and reloads Caddy. On a failure it restores the previous state.
func (m *Manager) switchTraffic(ctx context.Context, slot string) *Error {
	if !m.AsUser && m.Switch != nil {
		err := m.Switch(ctx, slot)
		switch {
		case err == nil:
			return nil
		case !errors.Is(err, ErrNoHelper):
			return &Error{Code: CodeCaddyReloadFailed, Message: "switching traffic failed: " + err.Error(), Hint: "traffic stayed on the previous version"}
		}
	}
	var olds []upstreamBackup
	for _, site := range m.proj.Sites {
		path := render.UpstreamPath(m.Name, site.Name)
		old, err := m.FS.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fail(CodeIO, err.Error())
		}
		port := m.port(site.Service, slot)
		if port == 0 {
			return fail(CodeIO, "no service port "+site.Service+" for slot "+slot)
		}
		body := []byte(render.Upstream(port, site.TrustedProxy))
		var werr error
		if err == nil {
			werr = m.FS.Overwrite(path, body)
		} else {
			werr = m.FS.WriteFile(path, body, 0o644)
		}
		if werr != nil {
			m.restore(ctx, olds, false)
			return fail(CodeIO, "write "+path+": "+werr.Error())
		}
		olds = append(olds, upstreamBackup{path, old})
	}
	if res, err := m.reloadCaddy(ctx); err != nil || res.ExitCode != 0 {
		m.restore(ctx, olds, true)
		return &Error{Code: CodeCaddyReloadFailed, Message: "Caddy reload failed: " + errText(res, err), Hint: "the upstream files were restored, traffic is on the previous version"}
	}
	return nil
}

// upstreamBackup is the previous content of an upstream file (nil: there was no file).
type upstreamBackup struct {
	path string
	data []byte
}

func (m *Manager) restore(ctx context.Context, olds []upstreamBackup, reload bool) {
	for _, o := range olds {
		if o.data != nil {
			_ = m.FS.Overwrite(o.path, o.data)
		}
	}
	if reload {
		_, _ = m.reloadCaddy(ctx)
	}
}

func (m *Manager) reloadCaddy(ctx context.Context) (host.Result, error) {
	if m.AsUser {
		return m.Runner.Run(ctx, host.Cmd{Name: "systemctl", Args: []string{"reload", "caddy"}, Timeout: time.Minute})
	}
	return m.Runner.Run(ctx, host.Cmd{Name: "sudo", Args: []string{"-n", "/usr/bin/systemctl", "reload", "caddy"}, Timeout: time.Minute})
}

func fileExists(f host.FS, p string) (bool, error) {
	if _, err := f.Stat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RollbackResult is the outcome of a rollback.
type RollbackResult struct {
	Project      string `json:"project"`
	Version      string `json:"version"`
	Slot         string `json:"slot"`
	PreviousSlot string `json:"previous_slot"`
}

// Rollback switches traffic to the previous slot (SPEC-v0.md, 5.3).
func (m *Manager) Rollback(ctx context.Context) (*RollbackResult, *Error) {
	if err := m.ensureStateDir(); err != nil {
		return nil, fail(CodeIO, err.Error())
	}
	unlock, err := m.Lock.TryLock(m.userStateDir() + "/lock")
	if err != nil {
		if errors.Is(err, host.ErrLocked) {
			return nil, &Error{Code: CodeInProgress, Message: "a deploy of this project is in progress", Hint: "wait and try again"}
		}
		return nil, fail(CodeIO, err.Error())
	}
	defer unlock()

	active := m.Active()
	if active == "none" {
		return nil, fail(CodeNothingDeployed, "the project has never been deployed")
	}
	prev := other(active)
	version := ""
	for _, s := range m.services() {
		ok, ver := m.probe(ctx, s, prev)
		if !ok {
			return nil, &Error{Code: CodeRollbackNA, Message: "the previous version (slot " + prev + ") is not responding", Hint: "deploy the version you need again: deploy <hash>"}
		}
		if version == "" {
			version = ver
		}
	}
	if e := m.switchTraffic(ctx, prev); e != nil {
		return nil, e
	}
	if err := m.writeState("active", []byte(prev+"\n")); err != nil {
		return nil, fail(CodeIO, "traffic was switched but active was not written: "+err.Error())
	}
	m.history("rollback", orDash(version), prev, active)
	return &RollbackResult{Project: m.Name, Version: version, Slot: prev, PreviousSlot: active}, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// SlotStatus is the state of a service in a slot.
type SlotStatus struct {
	Port    int     `json:"port"`
	Up      bool    `json:"up"`
	Version *string `json:"version"`
}

// StatusResult is the state of a project.
type StatusResult struct {
	Project string                           `json:"project"`
	Active  string                           `json:"active"`
	Slots   map[string]map[string]SlotStatus `json:"slots"`
	History []HistoryEntry                   `json:"history"`
}

// Status shows the active slot, service answers and recent events (SPEC-v0.md, 5.4).
func (m *Manager) Status(ctx context.Context) (*StatusResult, *Error) {
	res := &StatusResult{Project: m.Name, Active: m.Active(), Slots: map[string]map[string]SlotStatus{}, History: m.lastHistory(5)}
	if res.History == nil {
		res.History = []HistoryEntry{}
	}
	for _, slot := range Slots {
		res.Slots[slot] = map[string]SlotStatus{}
		for _, s := range m.services() {
			ok, ver := m.probe(ctx, s, slot)
			st := SlotStatus{Port: m.port(s, slot), Up: ok}
			if ok && ver != "" {
				v := ver
				st.Version = &v
			}
			res.Slots[slot][s] = st
		}
	}
	return res, nil
}

// fetchRelease downloads the release archive of a kind unit component and puts it into the free slot directory.
// The slot receives no traffic, so its old content can be replaced; on any error traffic is not affected.
func (m *Manager) fetchRelease(ctx context.Context, service, sha, slot string) *Error {
	svc := m.proj.Services[service]
	if svc.Release == nil || svc.Release.URL == "" {
		return fail(CodeFailed, "component "+service+" has no release described")
	}
	url := strings.ReplaceAll(svc.Release.URL, "{sha}", sha)
	dir := m.home() + "/" + render.AppDir(service, slot)
	tmp := dir + ".new"
	archive := m.userStateDir() + "/" + service + "-" + slot + ".tar.gz"
	step := func(timeout time.Duration, what, name string, args ...string) *Error {
		res, err := m.run(ctx, timeout, name, args...)
		if err != nil || res.ExitCode != 0 {
			_, _ = m.run(ctx, time.Minute, "rm", "-rf", tmp, archive)
			return fail(CodeFailed, what+": "+errText(res, err))
		}
		return nil
	}
	if e := step(time.Minute, "preparing the release directory", "mkdir", "-p", dir[:strings.LastIndex(dir, "/")]); e != nil {
		return e
	}
	if e := step(time.Minute, "cleaning the release directory", "rm", "-rf", tmp, archive); e != nil {
		return e
	}
	if e := step(time.Minute, "creating the release directory", "mkdir", tmp); e != nil {
		return e
	}
	curl := []string{"--fail", "--silent", "--show-error", "--location", "--proto", "=https", "--proto-redir", "=https", "--max-filesize", "2147483648", "--max-time", "900"}
	if svc.Release.TokenSecret != "" {
		curl = append(curl, "-H", "@"+m.home()+"/.config/lite-deploy/release-"+service+".hdr")
	}
	if e := step(16*time.Minute, "downloading the release "+url, "curl", append(curl, "-o", archive, url)...); e != nil {
		return e
	}
	sig := m.userStateDir() + "/" + service + "-" + slot + ".minisig"
	if e := step(5*time.Minute, "downloading the signature "+url+".minisig", "curl", append(curl, "-o", sig, url+".minisig")...); e != nil {
		return e
	}
	if res, err := m.run(ctx, time.Minute, "minisign", "-Vm", archive, "-P", svc.Release.MinisignKey, "-x", sig); err != nil || res.ExitCode != 0 {
		_, _ = m.run(ctx, time.Minute, "rm", "-rf", tmp, archive, sig)
		return fail(CodeFailed, "the archive signature is not confirmed, the release was rejected: "+errText(res, err))
	}
	_, _ = m.run(ctx, time.Minute, "rm", "-f", sig)
	if e := step(10*time.Minute, "unpacking the release", "tar", "-xzf", archive, "-C", tmp, "--no-same-owner"); e != nil {
		return e
	}
	if err := m.FS.WriteFile(tmp+"/.release", []byte(sha+"\n"), 0o644); err != nil {
		return fail(CodeFailed, "write the release mark: "+err.Error())
	}
	if m.AsUser {
		if err := m.FS.Chown(tmp+"/.release", m.Name, m.Name); err != nil {
			return fail(CodeFailed, "owner of the release mark: "+err.Error())
		}
	}
	// the slot is free: stop it and replace the directory
	_, _ = m.run(ctx, 2*time.Minute, "systemctl", "--user", "stop", render.UnitName(m.Name, service, slot)+".service")
	if e := step(time.Minute, "replacing the slot directory", "rm", "-rf", dir); e != nil {
		return e
	}
	if e := step(time.Minute, "moving the release into the slot", "mv", tmp, dir); e != nil {
		return e
	}
	_, _ = m.run(ctx, time.Minute, "rm", "-f", archive)
	return nil
}

// ErrNoHelper means the root helper that switches traffic is not installed on this server.
var ErrNoHelper = errors.New("the traffic switch helper is not installed")

// SwitchSlot moves traffic of all sites of the project to a slot (the root helper calls it).
func (m *Manager) SwitchSlot(ctx context.Context, slot string) *Error {
	return m.switchTraffic(ctx, slot)
}
