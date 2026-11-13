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

// Коды ошибок выкатки (SPEC-v0.md, 6.3).
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

// Пути по умолчанию.
const (
	DefaultProjectsDir = "/etc/lite-deploy/projects"
	DefaultStateDir    = "/var/lib/lite-deploy/state"
)

// Error ошибка выкатки с кодом для CLI.
type Error struct {
	Code    string
	Message string
	Hint    string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Env окружение выкатки. Всё внешнее (команды, файлы, HTTP, блокировка, время) подменяется в тестах.
type Env struct {
	Runner host.Runner
	FS     host.FS
	HTTP   host.HTTPClient
	Lock   host.Locker
	Now    func() time.Time
	Sleep  func(time.Duration)
	// AsUser: процесс запущен от root, команды podman и systemctl --user идут в сессии пользователя проекта.
	AsUser      bool
	ProjectsDir string // пусто: DefaultProjectsDir
	StateDir    string // пусто: DefaultStateDir
	Poll        time.Duration
}

// Manager выкатка одного проекта.
type Manager struct {
	Env
	Name  string
	proj  project.Project
	ports actions.Ports
}

var nameRe = regexp.MustCompile(`\A[a-z][a-z0-9-]{1,30}\z`)

// Slots два слота blue-green.
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

// Open читает описание проекта и порты из состояния сервера.
func Open(env Env, name string) (*Manager, *Error) {
	if !nameRe.MatchString(name) {
		return nil, fail(CodeProjectNotFound, "неверное имя проекта")
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
			return nil, &Error{Code: CodeProjectNotFound, Message: "проект " + name + " не найден", Hint: "создайте его: lite-deploy project create"}
		}
		return nil, fail(CodeIO, err.Error())
	}
	m := &Manager{Env: env, Name: name}
	if err := json.Unmarshal(raw, &m.proj); err != nil {
		return nil, fail(CodeIO, "описание проекта повреждено: "+err.Error())
	}
	if raw, err := env.FS.ReadFile(env.StateDir + "/projects/" + name + ".json"); err == nil {
		var st struct {
			Ports actions.Ports `json:"ports"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			return nil, fail(CodeIO, "состояние проекта повреждено: "+err.Error())
		}
		m.ports = st.Ports
	}
	if len(m.proj.Services) == 0 {
		return nil, fail(CodeIO, "в описании проекта нет сервисов")
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

// run запускает команду в сессии проектного пользователя (или напрямую, если уже он).
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
		// от root созданные каталоги принадлежали бы root: возвращаем их пользователю проекта
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

// Active слот, на который идёт трафик: blue, green или none.
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

// HistoryEntry строка журнала выкатки.
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

// probe опрашивает один сервис в слоте. Возвращает (отвечает, версия).
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

// DeployResult итог выкатки.
type DeployResult struct {
	Project      string `json:"project"`
	Version      string `json:"version"`
	Slot         string `json:"slot"`
	PreviousSlot string `json:"previous_slot"`
	Switched     bool   `json:"switched"`
}

// Deploy выкатывает версию sha в свободный слот и переключает трафик (SPEC-v0.md, 5.2).
func (m *Manager) Deploy(ctx context.Context, sha string) (*DeployResult, *Error) {
	if !shaRe.MatchString(sha) {
		return nil, fail(CodeInvalidSHA, "нужен полный хэш коммита: 40 символов 0-9a-f")
	}
	if err := m.ensureStateDir(); err != nil {
		return nil, fail(CodeIO, err.Error())
	}
	unlock, err := m.Lock.TryLock(m.userStateDir() + "/lock")
	if err != nil {
		if errors.Is(err, host.ErrLocked) {
			return nil, &Error{Code: CodeInProgress, Message: "другая выкатка этого проекта ещё идёт", Hint: "подождите и повторите"}
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
		img := m.proj.Services[s].Image + ":" + sha
		res, err := m.run(ctx, 10*time.Minute, "podman", append([]string{"pull"}, append(authArgs, img)...)...)
		if err != nil {
			return nil, fail(CodeFailed, "podman pull "+img+": "+err.Error())
		}
		if res.ExitCode != 0 {
			return nil, fail(CodeFailed, "не удалось скачать образ "+img+": "+trim(res))
		}
		local := "localhost/" + m.Name + "-" + s + ":" + idle
		if res, err := m.run(ctx, time.Minute, "podman", "tag", img, local); err != nil || res.ExitCode != 0 {
			return nil, fail(CodeFailed, "podman tag "+local+": "+errText(res, err))
		}
	}
	for _, s := range svcs {
		unit := render.UnitName(m.Name, s, idle) + ".service"
		if res, err := m.run(ctx, 2*time.Minute, "systemctl", "--user", "restart", unit); err != nil || res.ExitCode != 0 {
			return nil, m.abort(ctx, idle, sha, active, svcs, "не удалось запустить "+unit+": "+errText(res, err))
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
			return nil, m.abort(ctx, idle, sha, active, bad, "сервисы не стали готовыми вовремя: "+strings.Join(bad, ", "))
		}
		m.Sleep(m.Poll)
	}

	if e := m.switchTraffic(ctx, idle); e != nil {
		return nil, e
	}
	if err := m.writeState("active", []byte(idle+"\n")); err != nil {
		return nil, fail(CodeIO, "трафик переключён, но active не записан: "+err.Error())
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

// abort: последние строки журнала, остановка слота, запись, ошибка. Трафик остаётся на прежнем слоте.
func (m *Manager) abort(ctx context.Context, idle, sha, active string, logsFor []string, msg string) *Error {
	var logs []string
	for _, s := range logsFor {
		if res, err := m.run(ctx, 30*time.Second, "podman", "logs", "--tail", "40", render.UnitName(m.Name, s, idle)); err == nil {
			if t := strings.TrimSpace(string(res.Stdout) + string(res.Stderr)); t != "" {
				logs = append(logs, s+":\n"+t)
			}
		}
	}
	for _, s := range m.services() {
		_, _ = m.run(ctx, time.Minute, "systemctl", "--user", "stop", render.UnitName(m.Name, s, idle)+".service")
	}
	m.history("deploy-failed", sha, idle, active)
	e := &Error{Code: CodeUnhealthy, Message: msg, Hint: "трафик остался на прежней версии"}
	if len(logs) > 0 {
		e.Message += "\n" + strings.Join(logs, "\n")
	}
	return e
}

// switchTraffic пишет upstream всех сайтов на порты слота и перезагружает Caddy. При сбое возвращает прежнее.
func (m *Manager) switchTraffic(ctx context.Context, slot string) *Error {
	var olds []upstreamBackup
	for _, site := range m.proj.Sites {
		path := render.UpstreamPath(m.Name, site.Name)
		old, err := m.FS.ReadFile(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fail(CodeIO, err.Error())
		}
		port := m.port(site.Service, slot)
		if port == 0 {
			return fail(CodeIO, "нет порта сервиса "+site.Service+" для слота "+slot)
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
			return fail(CodeIO, "запись "+path+": "+werr.Error())
		}
		olds = append(olds, upstreamBackup{path, old})
	}
	if res, err := m.reloadCaddy(ctx); err != nil || res.ExitCode != 0 {
		m.restore(ctx, olds, true)
		return &Error{Code: CodeCaddyReloadFailed, Message: "reload Caddy не удался: " + errText(res, err), Hint: "файлы upstream возвращены, трафик на прежней версии"}
	}
	return nil
}

// upstreamBackup прежнее содержимое файла upstream (nil: файла не было).
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

// RollbackResult итог отката.
type RollbackResult struct {
	Project      string `json:"project"`
	Version      string `json:"version"`
	Slot         string `json:"slot"`
	PreviousSlot string `json:"previous_slot"`
}

// Rollback переключает трафик на прежний слот (SPEC-v0.md, 5.3).
func (m *Manager) Rollback(ctx context.Context) (*RollbackResult, *Error) {
	if err := m.ensureStateDir(); err != nil {
		return nil, fail(CodeIO, err.Error())
	}
	unlock, err := m.Lock.TryLock(m.userStateDir() + "/lock")
	if err != nil {
		if errors.Is(err, host.ErrLocked) {
			return nil, &Error{Code: CodeInProgress, Message: "выкатка этого проекта идёт", Hint: "подождите и повторите"}
		}
		return nil, fail(CodeIO, err.Error())
	}
	defer unlock()

	active := m.Active()
	if active == "none" {
		return nil, fail(CodeNothingDeployed, "проект ещё ни разу не выкатывали")
	}
	prev := other(active)
	version := ""
	for _, s := range m.services() {
		ok, ver := m.probe(ctx, s, prev)
		if !ok {
			return nil, &Error{Code: CodeRollbackNA, Message: "прежняя версия (слот " + prev + ") не отвечает", Hint: "выкатите нужную версию заново: deploy <хэш>"}
		}
		if version == "" {
			version = ver
		}
	}
	if e := m.switchTraffic(ctx, prev); e != nil {
		return nil, e
	}
	if err := m.writeState("active", []byte(prev+"\n")); err != nil {
		return nil, fail(CodeIO, "трафик переключён, но active не записан: "+err.Error())
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

// SlotStatus состояние сервиса в слоте.
type SlotStatus struct {
	Port    int     `json:"port"`
	Up      bool    `json:"up"`
	Version *string `json:"version"`
}

// StatusResult состояние проекта.
type StatusResult struct {
	Project string                           `json:"project"`
	Active  string                           `json:"active"`
	Slots   map[string]map[string]SlotStatus `json:"slots"`
	History []HistoryEntry                   `json:"history"`
}

// Status показывает активный слот, ответы сервисов и последние события (SPEC-v0.md, 5.4).
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
