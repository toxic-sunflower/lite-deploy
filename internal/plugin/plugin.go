// Package plugin механизм плагинов (REQUIREMENTS.md, R-19, R-25). Плагин добавляет команды в lite, раздел в интерфейс
// и проверки для verify. Встроенные плагины написаны на Go; внешние это каталог с манифестом plugin.json и скриптами
// или программами. Внешние плагины ставит только владелец сервера (каталог принадлежит root).
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Dir каталог внешних плагинов.
const Dir = "/etc/lite-deploy/plugins"

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	argRe  = regexp.MustCompile(`^[^\x00-\x1f\x7f]{0,256}$`)
)

// reserved имена команд lite, которые плагину занимать нельзя.
var reserved = map[string]bool{
	"version": true, "schema": true, "project": true, "apply": true, "init": true, "secret": true, "deploy": true,
	"rollback": true, "status": true, "gate": true, "ui": true, "agent": true, "exec": true,
	"call": true, "plugin": true, "info": true, "verify": true, "fix": true,
}

// Error ошибка плагина с кодом для конверта CLI.
type Error struct{ Code, Message, Hint string }

func (e *Error) Error() string { return e.Message }

// Errorf создаёт ошибку.
func Errorf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Command описание команды плагина.
type Command struct {
	Name string `json:"name"`
	Help string `json:"help"`
	// Confirm: команда меняет систему, нужно подтверждение (--yes или ответ в терминале).
	Confirm bool `json:"confirm,omitempty"`
	// Root: команде нужны права root даже без изменений (например, чтение состояния файервола).
	Root bool `json:"root,omitempty"`
	// Args описание аргументов для справки и интерфейса, например "ПАКЕТ".
	Args string `json:"args,omitempty"`
}

// Output итог команды.
type Output struct {
	Text string
	Data any
}

// Уровни результата проверки.
const (
	LevelOK   = "ok"
	LevelWarn = "warn"
	LevelFail = "fail"
)

// Finding результат одной проверки.
type Finding struct {
	Plugin  string `json:"plugin"`
	Check   string `json:"check"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Env что плагину доступно.
type Env struct {
	Runner host.Runner
	FS     host.FS
	Now    func() time.Time
	User   string // имя пользователя, от чьего имени выполняется команда
	// Projects пользователи проектов (CI заходит под ними по SSH): плагины не должны их отключать.
	Projects []string
}

// Plugin плагин.
type Plugin interface {
	Name() string
	Description() string
	// Kind: builtin или external.
	Kind() string
	// Requires пакеты ОС, нужные плагину.
	Requires() []string
	Commands() []Command
	Run(ctx context.Context, env Env, cmd string, args []string) (Output, error)
	Check(ctx context.Context, env Env) []Finding
}

// Registry набор плагинов.
type Registry struct {
	byName map[string]Plugin
}

// NewRegistry создаёт реестр; имена должны быть допустимыми и уникальными.
func NewRegistry() *Registry { return &Registry{byName: map[string]Plugin{}} }

// Add добавляет плагин.
func (r *Registry) Add(p Plugin) error {
	n := p.Name()
	if !nameRe.MatchString(n) {
		return fmt.Errorf("неверное имя плагина %q", n)
	}
	if reserved[n] {
		return fmt.Errorf("имя плагина %q занято командой lite", n)
	}
	if _, dup := r.byName[n]; dup {
		return fmt.Errorf("плагин %q уже есть", n)
	}
	r.byName[n] = p
	return nil
}

// Get возвращает плагин по имени.
func (r *Registry) Get(name string) (Plugin, bool) { p, ok := r.byName[name]; return p, ok }

// List плагины по алфавиту.
func (r *Registry) List() []Plugin {
	out := make([]Plugin, 0, len(r.byName))
	for _, p := range r.byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ValidArgs проверяет аргументы команды плагина: без управляющих символов, ограниченной длины.
func ValidArgs(args []string) bool {
	if len(args) > 16 {
		return false
	}
	for _, a := range args {
		if !argRe.MatchString(a) {
			return false
		}
	}
	return true
}

// Find ищет команду плагина.
func Find(p Plugin, cmd string) (Command, bool) {
	for _, c := range p.Commands() {
		if c.Name == cmd {
			return c, true
		}
	}
	return Command{}, false
}

// ---------- внешние плагины ----------

// Manifest содержимое plugin.json.
type Manifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Requires    []string        `json:"requires,omitempty"`
	Commands    []ManifestCmd   `json:"commands"`
	Checks      []ManifestCheck `json:"checks,omitempty"`
}

// ManifestCmd команда во внешнем плагине.
type ManifestCmd struct {
	Name    string `json:"name"`
	Help    string `json:"help"`
	Args    string `json:"args,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
	Root    bool   `json:"root,omitempty"`
	// Exec файл относительно каталога плагина.
	Exec string `json:"exec"`
}

// ManifestCheck проверка: файл завершается кодом 0 (ок), 1 (предупреждение) или другим (ошибка); первая строка вывода это сообщение.
type ManifestCheck struct {
	Name string `json:"name"`
	Exec string `json:"exec"`
	Fix  string `json:"fix,omitempty"`
}

func execOK(p string) bool {
	return p != "" && !path.IsAbs(p) && path.Clean(p) == p && !strings.HasPrefix(p, "..")
}

// ParseManifest разбирает и проверяет plugin.json.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("plugin.json: %w", err)
	}
	if !nameRe.MatchString(m.Name) || reserved[m.Name] {
		return m, fmt.Errorf("plugin.json: неверное или занятое имя %q", m.Name)
	}
	if len(m.Commands) == 0 && len(m.Checks) == 0 {
		return m, errors.New("plugin.json: нет ни команд, ни проверок")
	}
	seen := map[string]bool{}
	for _, c := range m.Commands {
		if !nameRe.MatchString(c.Name) || seen[c.Name] {
			return m, fmt.Errorf("plugin.json: неверная или повторная команда %q", c.Name)
		}
		seen[c.Name] = true
		if !execOK(c.Exec) {
			return m, fmt.Errorf("plugin.json: команда %s: файл %q должен быть путём внутри каталога плагина", c.Name, c.Exec)
		}
	}
	for _, c := range m.Checks {
		if !nameRe.MatchString(c.Name) || !execOK(c.Exec) {
			return m, fmt.Errorf("plugin.json: неверная проверка %q", c.Name)
		}
	}
	for _, r := range m.Requires {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,100}$`).MatchString(r) {
			return m, fmt.Errorf("plugin.json: неверное имя пакета %q", r)
		}
	}
	return m, nil
}

type external struct {
	m   Manifest
	dir string
	// Installed сообщает, установлен ли пакет ОС (для requires); nil: не проверять.
	installed func(ctx context.Context, name string) bool
}

func (x *external) Name() string        { return x.m.Name }
func (x *external) Description() string { return x.m.Description }
func (x *external) Kind() string        { return "external" }
func (x *external) Requires() []string  { return x.m.Requires }
func (x *external) Commands() []Command {
	out := make([]Command, 0, len(x.m.Commands))
	for _, c := range x.m.Commands {
		out = append(out, Command{Name: c.Name, Help: c.Help, Args: c.Args, Confirm: c.Confirm, Root: c.Root})
	}
	return out
}

func (x *external) env(env Env) []string {
	return []string{"LITE_PLUGIN=" + x.m.Name, "LITE_PLUGIN_DIR=" + x.dir, "LITE_USER=" + env.User}
}

func (x *external) missing(ctx context.Context) []string {
	var out []string
	if x.installed == nil {
		return nil
	}
	for _, r := range x.m.Requires {
		if !x.installed(ctx, r) {
			out = append(out, r)
		}
	}
	return out
}

func (x *external) Run(ctx context.Context, env Env, cmd string, args []string) (Output, error) {
	for _, c := range x.m.Commands {
		if c.Name != cmd {
			continue
		}
		if miss := x.missing(ctx); len(miss) > 0 {
			e := Errorf("precondition_failed", "плагину %s не хватает пакетов: %s", x.m.Name, strings.Join(miss, ", "))
			e.Hint = "lite package install " + strings.Join(miss, " ")
			return Output{}, e
		}
		res, err := env.Runner.Run(ctx, host.Cmd{Name: path.Join(x.dir, c.Exec), Args: args, Env: x.env(env), Timeout: 10 * time.Minute})
		if err != nil {
			return Output{}, Errorf("step_failed", "%s %s: %v", x.m.Name, cmd, err)
		}
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(string(res.Stderr))
			if msg == "" {
				msg = fmt.Sprintf("команда завершилась с кодом %d", res.ExitCode)
			}
			return Output{}, Errorf("step_failed", "%s %s: %s", x.m.Name, cmd, msg)
		}
		out := string(res.Stdout)
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return Output{Text: out, Data: map[string]string{"output": strings.TrimRight(out, "\n")}}, nil
	}
	return Output{}, Errorf("usage", "у плагина %s нет команды %q", x.m.Name, cmd)
}

func (x *external) Check(ctx context.Context, env Env) []Finding {
	var out []Finding
	if miss := x.missing(ctx); len(miss) > 0 {
		out = append(out, Finding{Plugin: x.m.Name, Check: "requires", Level: LevelFail,
			Message: "не установлены пакеты: " + strings.Join(miss, ", "), Fix: "lite package install " + strings.Join(miss, " ")})
	}
	for _, c := range x.m.Checks {
		res, err := env.Runner.Run(ctx, host.Cmd{Name: path.Join(x.dir, c.Exec), Env: x.env(env), Timeout: 2 * time.Minute})
		f := Finding{Plugin: x.m.Name, Check: c.Name, Fix: c.Fix}
		msg, _, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), "\n")
		switch {
		case err != nil:
			f.Level, f.Message = LevelFail, err.Error()
		case res.ExitCode == 0:
			f.Level, f.Message = LevelOK, msg
		case res.ExitCode == 1:
			f.Level, f.Message = LevelWarn, msg
		default:
			f.Level, f.Message = LevelFail, msg
		}
		if f.Message == "" {
			f.Message = c.Name
		}
		out = append(out, f)
	}
	return out
}

// LoadDir читает внешние плагины из каталога dir и добавляет их в реестр. Ошибки отдельных плагинов возвращаются
// списком: один сломанный плагин не мешает остальным. installed проверяет пакеты из requires (может быть nil).
func LoadDir(fsys host.FS, dir string, r *Registry, installed func(ctx context.Context, name string) bool) []error {
	names, err := fsys.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return []error{err}
	}
	var errs []error
	for _, n := range names {
		pd := path.Join(dir, n)
		st, err := fsys.Stat(pd)
		if err != nil || !st.IsDir {
			continue
		}
		// плагин выполняется с правами lite: каталог должен принадлежать root и быть закрыт для записи другим
		if st.Owner != "root" || st.Mode&0o022 != 0 {
			errs = append(errs, fmt.Errorf("плагин %s: каталог %s должен принадлежать root и быть закрыт для записи другим (chown root, chmod go-w)", n, pd))
			continue
		}
		b, err := fsys.ReadFile(path.Join(pd, "plugin.json"))
		if err != nil {
			errs = append(errs, fmt.Errorf("плагин %s: %w", n, err))
			continue
		}
		m, err := ParseManifest(b)
		if err == nil && m.Name != n {
			err = fmt.Errorf("plugin.json: имя %q не совпадает с именем каталога %q", m.Name, n)
		}
		if err == nil {
			err = r.Add(&external{m: m, dir: pd, installed: installed})
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("плагин %s: %w", n, err))
		}
	}
	return errs
}
