// Package pkgmgr системные пакеты: установка, удаление, обновление, проверка и журнал того, что поставил lite
// (REQUIREMENTS.md, R-18, R-21, R-22). Поддерживаются apt, dnf, zypper, pacman.
package pkgmgr

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

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// DefaultJournal журнал пакетов, поставленных через lite.
const DefaultJournal = "/var/lib/lite-deploy/packages.json"

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,100}$`)

// protected пакеты, без которых сервер нельзя оставлять: их удаление отклоняется.
var protected = map[string]bool{
	"openssh-server": true, "openssh": true, "openssh-clients": true, "sudo": true, "systemd": true,
	"libc6": true, "glibc": true, "bash": true, "coreutils": true, "apt": true, "dpkg": true, "dnf": true, "rpm": true,
	"minisign": true,
}

// Error ошибка с кодом для конверта CLI.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

func fail(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Entry запись журнала.
type Entry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	InstalledAt string `json:"installed_at"`
	By          string `json:"by,omitempty"`
}

// Info состояние одного пакета.
type Info struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	ByLite    bool   `json:"by_lite"`
}

// Manager менеджер пакетов системы.
type Manager struct {
	Runner  host.Runner
	FS      host.FS
	Journal string // пусто: DefaultJournal
	Now     func() time.Time
	By      string // кто выполняет (имя пользователя) для журнала
}

func (m *Manager) journalPath() string {
	if m.Journal != "" {
		return m.Journal
	}
	return DefaultJournal
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// ValidName проверяет имя пакета.
func ValidName(n string) bool { return nameRe.MatchString(n) }

func (m *Manager) run(ctx context.Context, c host.Cmd) (host.Result, error) {
	if c.Timeout == 0 {
		c.Timeout = 15 * time.Minute
	}
	return m.Runner.Run(ctx, c)
}

// Detect определяет менеджер пакетов: apt, dnf, zypper или pacman.
func (m *Manager) Detect(ctx context.Context) (string, error) {
	for _, p := range [][2]string{{"apt", "apt-get"}, {"dnf", "dnf"}, {"zypper", "zypper"}, {"pacman", "pacman"}} {
		if res, err := m.Runner.Run(ctx, host.Cmd{Name: p[1], Args: []string{"--version"}}); err == nil && res.ExitCode == 0 {
			return p[0], nil
		}
	}
	return "", fail("precondition_failed", "не найден менеджер пакетов (apt, dnf, zypper, pacman)")
}

func checkNames(names []string) *Error {
	if len(names) == 0 {
		return fail("usage", "не указан пакет")
	}
	for _, n := range names {
		if !nameRe.MatchString(n) {
			return fail("usage", "неверное имя пакета %q", n)
		}
	}
	return nil
}

// Query возвращает версию установленного пакета.
func (m *Manager) Query(ctx context.Context, mgr, name string) (string, bool, error) {
	var c host.Cmd
	switch mgr {
	case "apt":
		c = host.Cmd{Name: "dpkg-query", Args: []string{"-W", "-f=${Status}|${Version}", name}}
	case "dnf", "zypper":
		c = host.Cmd{Name: "rpm", Args: []string{"-q", "--qf", "%{VERSION}-%{RELEASE}", name}}
	case "pacman":
		c = host.Cmd{Name: "pacman", Args: []string{"-Q", name}}
	default:
		return "", false, fail("internal", "неизвестный менеджер пакетов %q", mgr)
	}
	res, err := m.Runner.Run(ctx, c)
	if err != nil {
		return "", false, err
	}
	if res.ExitCode != 0 {
		return "", false, nil
	}
	out := strings.TrimSpace(string(res.Stdout))
	switch mgr {
	case "apt":
		st, ver, _ := strings.Cut(out, "|")
		if !strings.HasSuffix(st, " installed") || strings.HasPrefix(st, "deinstall") {
			return "", false, nil
		}
		return ver, true, nil
	case "pacman":
		_, ver, _ := strings.Cut(out, " ")
		return ver, true, nil
	}
	return out, out != "", nil
}

func (m *Manager) loadJournal() ([]Entry, error) {
	b, err := m.FS.ReadFile(m.journalPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var j struct {
		Packages []Entry `json:"packages"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("журнал пакетов %s повреждён: %w", m.journalPath(), err)
	}
	return j.Packages, nil
}

func (m *Manager) saveJournal(es []Entry) error {
	sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	b, _ := json.MarshalIndent(map[string]any{"packages": es}, "", "  ")
	dir := m.journalPath()[:strings.LastIndex(m.journalPath(), "/")]
	if err := m.FS.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return m.FS.WriteFile(m.journalPath(), append(b, '\n'), 0o644)
}

// List перечисляет пакеты, поставленные через lite (из журнала).
func (m *Manager) List() ([]Entry, error) { return m.loadJournal() }

// Check показывает состояние пакета.
func (m *Manager) Check(ctx context.Context, name string) (Info, error) {
	if !nameRe.MatchString(name) {
		return Info{}, fail("usage", "неверное имя пакета %q", name)
	}
	mgr, err := m.Detect(ctx)
	if err != nil {
		return Info{}, err
	}
	ver, ok, err := m.Query(ctx, mgr, name)
	if err != nil {
		return Info{}, err
	}
	es, err := m.loadJournal()
	if err != nil {
		return Info{}, err
	}
	in := Info{Name: name, Installed: ok, Version: ver}
	for _, e := range es {
		if e.Name == name {
			in.ByLite = true
		}
	}
	return in, nil
}

func (m *Manager) commands(mgr, op string, pkgs []string) []host.Cmd {
	switch mgr {
	case "apt":
		env := []string{"DEBIAN_FRONTEND=noninteractive"}
		switch op {
		case "install":
			return []host.Cmd{{Name: "apt-get", Args: []string{"update", "-qq"}}, {Name: "apt-get", Args: append([]string{"install", "-y", "-qq"}, pkgs...), Env: env}}
		case "remove":
			return []host.Cmd{{Name: "apt-get", Args: append([]string{"remove", "-y", "-qq"}, pkgs...), Env: env}}
		default:
			return []host.Cmd{{Name: "apt-get", Args: []string{"update", "-qq"}}, {Name: "apt-get", Args: append([]string{"install", "-y", "-qq", "--only-upgrade"}, pkgs...), Env: env}}
		}
	case "dnf":
		verb := map[string]string{"install": "install", "remove": "remove", "update": "upgrade"}[op]
		return []host.Cmd{{Name: "dnf", Args: append([]string{verb, "-y", "-q"}, pkgs...)}}
	case "zypper":
		verb := map[string]string{"install": "install", "remove": "remove", "update": "update"}[op]
		return []host.Cmd{{Name: "zypper", Args: append([]string{"--non-interactive", verb}, pkgs...)}}
	default: // pacman
		switch op {
		case "remove":
			return []host.Cmd{{Name: "pacman", Args: append([]string{"-R", "--noconfirm"}, pkgs...)}}
		case "install":
			return []host.Cmd{{Name: "pacman", Args: append([]string{"-S", "--noconfirm", "--needed"}, pkgs...)}}
		default:
			return []host.Cmd{{Name: "pacman", Args: append([]string{"-S", "--noconfirm"}, pkgs...)}}
		}
	}
}

func (m *Manager) exec(ctx context.Context, mgr, op string, pkgs []string) error {
	for _, c := range m.commands(mgr, op, pkgs) {
		res, err := m.run(ctx, c)
		if err != nil {
			return fail("step_failed", "%s: %v", c.Name, err)
		}
		if res.ExitCode != 0 {
			return fail("step_failed", "%s %s: %s", c.Name, strings.Join(c.Args, " "), strings.TrimSpace(string(res.Stderr)))
		}
	}
	return nil
}

// Install ставит пакеты и записывает в журнал те, которых раньше не было.
func (m *Manager) Install(ctx context.Context, names ...string) error {
	if e := checkNames(names); e != nil {
		return e
	}
	mgr, err := m.Detect(ctx)
	if err != nil {
		return err
	}
	was := map[string]bool{}
	for _, n := range names {
		if _, ok, _ := m.Query(ctx, mgr, n); ok {
			was[n] = true
		}
	}
	if err := m.exec(ctx, mgr, "install", names); err != nil {
		return err
	}
	es, err := m.loadJournal()
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, e := range es {
		have[e.Name] = true
	}
	for _, n := range names {
		if was[n] || have[n] {
			continue
		}
		ver, ok, _ := m.Query(ctx, mgr, n)
		if !ok {
			return fail("step_failed", "пакет %s не появился в системе после установки", n)
		}
		es = append(es, Entry{Name: n, Version: ver, InstalledAt: m.now().UTC().Format(time.RFC3339), By: m.By})
	}
	return m.saveJournal(es)
}

// Update обновляет пакеты и версию в журнале.
func (m *Manager) Update(ctx context.Context, names ...string) error {
	if e := checkNames(names); e != nil {
		return e
	}
	mgr, err := m.Detect(ctx)
	if err != nil {
		return err
	}
	for _, n := range names {
		if _, ok, _ := m.Query(ctx, mgr, n); !ok {
			return fail("precondition_failed", "пакет %s не установлен", n)
		}
	}
	if err := m.exec(ctx, mgr, "update", names); err != nil {
		return err
	}
	es, err := m.loadJournal()
	if err != nil {
		return err
	}
	for i := range es {
		for _, n := range names {
			if es[i].Name == n {
				if v, ok, _ := m.Query(ctx, mgr, n); ok {
					es[i].Version = v
				}
			}
		}
	}
	return m.saveJournal(es)
}

// Remove удаляет пакеты. Системно важные пакеты (ssh-сервер, sudo, systemd, оболочка) не удаляются.
func (m *Manager) Remove(ctx context.Context, names ...string) error {
	if e := checkNames(names); e != nil {
		return e
	}
	for _, n := range names {
		if protected[n] {
			return fail("protected_rule", "пакет %s удалять нельзя: без него сервер перестанет работать", n)
		}
	}
	mgr, err := m.Detect(ctx)
	if err != nil {
		return err
	}
	for _, n := range names {
		if _, ok, _ := m.Query(ctx, mgr, n); !ok {
			return fail("precondition_failed", "пакет %s не установлен", n)
		}
	}
	if err := m.exec(ctx, mgr, "remove", names); err != nil {
		return err
	}
	es, err := m.loadJournal()
	if err != nil {
		return err
	}
	var keep []Entry
	for _, e := range es {
		drop := false
		for _, n := range names {
			drop = drop || e.Name == n
		}
		if !drop {
			keep = append(keep, e)
		}
	}
	return m.saveJournal(keep)
}
