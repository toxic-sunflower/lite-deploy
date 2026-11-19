// Package plugin is the plugin mechanism (REQUIREMENTS.md, R-19, R-25). A plugin adds commands to lite, a section to the interface
// and checks for verify. Built-in plugins are written in Go; external ones are a directory with a plugin.json manifest and scripts
// or programs. External plugins are installed only by the server owner (the directory belongs to root).
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Dir is the directory of external plugins.
const Dir = "/etc/lite-deploy/plugins"

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	argRe  = regexp.MustCompile(`^[^\x00-\x1f\x7f]{0,256}$`)
)

// reserved are lite command names a plugin must not take.
var reserved = map[string]bool{
	"version": true, "schema": true, "project": true, "apply": true, "init": true, "secret": true, "deploy": true,
	"rollback": true, "status": true, "gate": true, "ui": true, "agent": true, "exec": true,
	"call": true, "plugin": true, "info": true, "verify": true, "fix": true,
}

// Error is a plugin error with a code for the CLI envelope.
type Error struct{ Code, Message, Hint string }

func (e *Error) Error() string { return e.Message }

// Errorf creates an error.
func Errorf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Command describes a plugin command.
type Command struct {
	Name string `json:"name"`
	Help string `json:"help"`
	// Confirm: the command changes the system and needs confirmation (--yes or an answer in the terminal).
	Confirm bool `json:"confirm,omitempty"`
	// Root: the command needs root even without changes (for example, reading the firewall state).
	Root bool `json:"root,omitempty"`
	// Args describes the arguments for help and the interface, for example "PACKAGE".
	Args string `json:"args,omitempty"`
}

// Output is the result of a command.
type Output struct {
	Text string
	Data any
}

// Check result levels.
const (
	LevelOK   = "ok"
	LevelWarn = "warn"
	LevelFail = "fail"
)

// Finding is the result of one check.
type Finding struct {
	Plugin  string `json:"plugin"`
	Check   string `json:"check"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Env is what the plugin has access to.
type Env struct {
	Runner host.Runner
	FS     host.FS
	Now    func() time.Time
	User   string // the name of the user the command runs as
	// Projects are the project users (CI logs in as them over SSH): plugins must not disable them.
	Projects []string
	// Stdin is the command standard input (for passwords and keys that must not be passed as arguments); may be nil.
	Stdin io.Reader
}

// Plugin is a plugin.
type Plugin interface {
	Name() string
	Description() string
	// Kind: builtin or external.
	Kind() string
	// Requires are the OS packages the plugin needs.
	Requires() []string
	Commands() []Command
	Run(ctx context.Context, env Env, cmd string, args []string) (Output, error)
	Check(ctx context.Context, env Env) []Finding
}

// Registry is a set of plugins.
type Registry struct {
	byName map[string]Plugin
}

// NewRegistry creates a registry; names must be valid and unique.
func NewRegistry() *Registry { return &Registry{byName: map[string]Plugin{}} }

// Add adds a plugin.
func (r *Registry) Add(p Plugin) error {
	n := p.Name()
	if !nameRe.MatchString(n) {
		return fmt.Errorf("invalid plugin name %q", n)
	}
	if reserved[n] {
		return fmt.Errorf("the plugin name %q is taken by a lite command", n)
	}
	if _, dup := r.byName[n]; dup {
		return fmt.Errorf("plugin %q already exists", n)
	}
	r.byName[n] = p
	return nil
}

// Get returns a plugin by name.
func (r *Registry) Get(name string) (Plugin, bool) { p, ok := r.byName[name]; return p, ok }

// List returns the plugins in alphabetical order.
func (r *Registry) List() []Plugin {
	out := make([]Plugin, 0, len(r.byName))
	for _, p := range r.byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ValidArgs validates the arguments of a plugin command: no control characters, limited length.
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

// Find looks up a plugin command.
func Find(p Plugin, cmd string) (Command, bool) {
	for _, c := range p.Commands() {
		if c.Name == cmd {
			return c, true
		}
	}
	return Command{}, false
}

// ---------- external plugins ----------

// Manifest is the content of plugin.json.
type Manifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Requires    []string        `json:"requires,omitempty"`
	Commands    []ManifestCmd   `json:"commands"`
	Checks      []ManifestCheck `json:"checks,omitempty"`
}

// ManifestCmd is a command in an external plugin.
type ManifestCmd struct {
	Name    string `json:"name"`
	Help    string `json:"help"`
	Args    string `json:"args,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
	Root    bool   `json:"root,omitempty"`
	// Exec is a file relative to the plugin directory.
	Exec string `json:"exec"`
}

// ManifestCheck is a check: the file exits with 0 (ok), 1 (warning) or another code (error); the first output line is the message.
type ManifestCheck struct {
	Name string `json:"name"`
	Exec string `json:"exec"`
	Fix  string `json:"fix,omitempty"`
}

func execOK(p string) bool {
	return p != "" && !path.IsAbs(p) && path.Clean(p) == p && !strings.HasPrefix(p, "..")
}

// ParseManifest parses and validates plugin.json.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("plugin.json: %w", err)
	}
	if !nameRe.MatchString(m.Name) || reserved[m.Name] {
		return m, fmt.Errorf("plugin.json: invalid or reserved name %q", m.Name)
	}
	if len(m.Commands) == 0 && len(m.Checks) == 0 {
		return m, errors.New("plugin.json: neither commands nor checks")
	}
	seen := map[string]bool{}
	for _, c := range m.Commands {
		if !nameRe.MatchString(c.Name) || seen[c.Name] {
			return m, fmt.Errorf("plugin.json: invalid or duplicate command %q", c.Name)
		}
		seen[c.Name] = true
		if !execOK(c.Exec) {
			return m, fmt.Errorf("plugin.json: command %s: file %q must be a path inside the plugin directory", c.Name, c.Exec)
		}
	}
	for _, c := range m.Checks {
		if !nameRe.MatchString(c.Name) || !execOK(c.Exec) {
			return m, fmt.Errorf("plugin.json: invalid check %q", c.Name)
		}
	}
	for _, r := range m.Requires {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,100}$`).MatchString(r) {
			return m, fmt.Errorf("plugin.json: invalid package name %q", r)
		}
	}
	return m, nil
}

type external struct {
	m   Manifest
	dir string
	// Installed reports whether an OS package is installed (for requires); nil: do not check.
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
			e := Errorf("precondition_failed", "plugin %s is missing packages: %s", x.m.Name, strings.Join(miss, ", "))
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
				msg = fmt.Sprintf("the command exited with code %d", res.ExitCode)
			}
			return Output{}, Errorf("step_failed", "%s %s: %s", x.m.Name, cmd, msg)
		}
		out := string(res.Stdout)
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return Output{Text: out, Data: map[string]string{"output": strings.TrimRight(out, "\n")}}, nil
	}
	return Output{}, Errorf("usage", "plugin %s has no command %q", x.m.Name, cmd)
}

func (x *external) Check(ctx context.Context, env Env) []Finding {
	var out []Finding
	if miss := x.missing(ctx); len(miss) > 0 {
		out = append(out, Finding{Plugin: x.m.Name, Check: "requires", Level: LevelFail,
			Message: "packages not installed: " + strings.Join(miss, ", "), Fix: "lite package install " + strings.Join(miss, " ")})
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

// LoadDir reads external plugins from the directory dir and adds them to the registry. Errors of individual plugins are returned
// as a list: one broken plugin does not hinder the others. installed checks the packages from requires (may be nil).
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
		// the plugin runs with lite rights: the directory must belong to root and not be writable by others
		if st.Owner != "root" || st.Mode&0o022 != 0 {
			errs = append(errs, fmt.Errorf("plugin %s: directory %s must be owned by root and not writable by others (chown root, chmod go-w)", n, pd))
			continue
		}
		b, err := fsys.ReadFile(path.Join(pd, "plugin.json"))
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", n, err))
			continue
		}
		m, err := ParseManifest(b)
		if err == nil && m.Name != n {
			err = fmt.Errorf("plugin.json: name %q does not match the directory name %q", m.Name, n)
		}
		if err == nil {
			err = r.Add(&external{m: m, dir: pd, installed: installed})
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", n, err))
		}
	}
	return errs
}
