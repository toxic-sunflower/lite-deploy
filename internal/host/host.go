// Package host is the only place where code touches the system: running commands and files.
// Any external command is run only through Runner: an argument array, no shell, with a timeout.
// There are no direct os/exec calls outside this package (a test in internal/lint checks it).
package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"
)

// Cmd is a command without a shell.
type Cmd struct {
	Name    string
	Args    []string
	Stdin   []byte
	Env     []string      // additional variables, format NAME=value
	Timeout time.Duration // 0 = 30 seconds
	// Interactive: the command talks to a person directly (for example passwd): input and output go to the terminal, the output is not collected.
	Interactive bool
}

// Result is the result of a run.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs commands.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// ExecRunner runs real processes.
type ExecRunner struct{}

// Run runs a command; a non-zero exit code is returned in Result, not as an error.
func (ExecRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	if c.Name == "" {
		return Result{}, errors.New("empty command name")
	}
	t := c.Timeout
	if t == 0 {
		t = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	if c.Stdin != nil {
		cmd.Stdin = bytes.NewReader(c.Stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if c.Interactive {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	}
	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	if ctx.Err() != nil {
		return res, fmt.Errorf("command %s was stopped by the timeout %s: %w", c.Name, t, ctx.Err())
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err
}

// FS is the file operations the core needs.
type FS interface {
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]string, error) // entry names in alphabetical order
	// WriteFile atomically writes a file (a temporary file and rename) with the exact mode perm, regardless of umask.
	WriteFile(path string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	ModTime(path string) (time.Time, error)
	// Stat describes a file or a directory; for a missing one it returns an error compatible with fs.ErrNotExist.
	Stat(path string) (Info, error)
	// Chown changes the owner and group by name (an empty group leaves the group unchanged).
	Chown(path, owner, group string) error
	Chmod(path string, perm fs.FileMode) error
	Remove(path string) error
	// Append appends to a file; if the file does not exist, creates it with mode perm.
	Append(path string, data []byte, perm fs.FileMode) error
	// Overwrite writes an existing file in place, keeping its owner and mode.
	Overwrite(path string, data []byte) error
	// Open opens a file for streaming reads (large files are not read into memory whole).
	Open(path string) (io.ReadCloser, error)
	// Create creates or truncates a file with mode perm for streaming writes.
	Create(path string, perm fs.FileMode) (io.WriteCloser, error)
}

// Info describes a file system entry.
type Info struct {
	Mode  fs.FileMode // only access rights
	IsDir bool
	Owner string
	Group string
}

// OSFS is the real file system.
type OSFS struct{}

// ReadFile reads a file.
func (OSFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

// ReadDir returns the names of directory entries.
func (OSFS) ReadDir(p string) ([]string, error) {
	es, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out, nil
}

// WriteFile atomically writes a file with mode perm.
func (OSFS) WriteFile(p string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, p); err != nil {
		return err
	}
	ok = true
	return nil
}

// MkdirAll creates a directory with mode perm (for newly created directories, regardless of umask).
func (OSFS) MkdirAll(p string, perm fs.FileMode) error {
	if err := os.MkdirAll(p, perm); err != nil {
		return err
	}
	return os.Chmod(p, perm)
}

// ModTime is the file modification time.
func (OSFS) ModTime(p string) (time.Time, error) {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

// MemFS is an in-memory file system for tests; modes and times of entries are stored separately.
type MemFS struct {
	Files map[string][]byte
	Perms map[string]fs.FileMode
	Times map[string]time.Time
	Dirs  map[string]fs.FileMode
	// Owners are owners and groups; no entry means root:root. Writing a file resets the owner, like the rename of a real WriteFile.
	Owners map[string][2]string
	Now    func() time.Time
}

// NewMemFS creates an empty MemFS.
func NewMemFS() *MemFS {
	return &MemFS{Files: map[string][]byte{}, Perms: map[string]fs.FileMode{}, Times: map[string]time.Time{}, Dirs: map[string]fs.FileMode{}, Owners: map[string][2]string{}, Now: time.Now}
}

// WriteFile writes a file.
func (m *MemFS) WriteFile(p string, data []byte, perm fs.FileMode) error {
	p = filepath.Clean(p)
	m.Files[p] = append([]byte(nil), data...)
	m.Perms[p] = perm
	m.Times[p] = m.Now()
	delete(m.Owners, p)
	return nil
}

// MkdirAll remembers the directory and the missing parents.
func (m *MemFS) MkdirAll(p string, perm fs.FileMode) error {
	p = filepath.Clean(p)
	m.Dirs[p] = perm
	for d := filepath.Dir(p); d != "/" && d != "."; d = filepath.Dir(d) {
		if _, ok := m.Dirs[d]; !ok {
			m.Dirs[d] = 0o755
		}
	}
	return nil
}

// ModTime returns the write time.
func (m *MemFS) ModTime(p string) (time.Time, error) {
	t, ok := m.Times[filepath.Clean(p)]
	if !ok {
		if _, has := m.Files[filepath.Clean(p)]; has {
			return time.Time{}, nil
		}
		return time.Time{}, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
	}
	return t, nil
}

// ReadFile reads a file.
func (m *MemFS) ReadFile(p string) ([]byte, error) {
	b, ok := m.Files[filepath.Clean(p)]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	return b, nil
}

// ReadDir returns the names of the direct entries.
func (m *MemFS) ReadDir(p string) ([]string, error) {
	p = filepath.Clean(p)
	seen := map[string]bool{}
	for f := range m.Files {
		rel, err := filepath.Rel(p, f)
		if err != nil || rel == "." || rel == ".." || len(rel) >= 3 && rel[:3] == "../" {
			continue
		}
		seen[rel[:indexSlash(rel)]] = true
	}
	if len(seen) == 0 {
		return nil, &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrNotExist}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func indexSlash(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return i
		}
	}
	return len(s)
}

// FakeRunner records commands and answers with prepared results; for tests.
type FakeRunner struct {
	Calls   []Cmd
	Handler func(c Cmd) (Result, error)
}

// Run records the call and calls Handler (if set).
func (f *FakeRunner) Run(_ context.Context, c Cmd) (Result, error) {
	f.Calls = append(f.Calls, c)
	if f.Handler == nil {
		return Result{}, fmt.Errorf("FakeRunner: no handler for %s", c.Name)
	}
	return f.Handler(c)
}

// Stat describes an entry.
func (OSFS) Stat(p string) (Info, error) {
	st, err := os.Stat(p)
	if err != nil {
		return Info{}, err
	}
	info := Info{Mode: st.Mode().Perm(), IsDir: st.IsDir()}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		info.Owner, info.Group = strconv.FormatUint(uint64(sys.Uid), 10), strconv.FormatUint(uint64(sys.Gid), 10)
		if u, err := user.LookupId(info.Owner); err == nil {
			info.Owner = u.Username
		}
		if g, err := user.LookupGroupId(info.Group); err == nil {
			info.Group = g.Name
		}
	}
	return info, nil
}

// Chown changes the owner and group by name.
func (OSFS) Chown(p, owner, group string) error {
	u, err := user.Lookup(owner)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid := -1
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			return err
		}
		gid, _ = strconv.Atoi(g.Gid)
	}
	return os.Lchown(p, uid, gid)
}

// Chmod changes the mode.
func (OSFS) Chmod(p string, perm fs.FileMode) error { return os.Chmod(p, perm) }

// Remove deletes a file or an empty directory.
func (OSFS) Remove(p string) error { return os.Remove(p) }

// Append appends data to a file.
func (OSFS) Append(p string, data []byte, perm fs.FileMode) error {
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Stat describes an entry.
func (m *MemFS) Stat(p string) (Info, error) {
	p = filepath.Clean(p)
	own := m.Owners[p]
	if own == [2]string{} {
		own = [2]string{"root", "root"}
	}
	if _, ok := m.Files[p]; ok {
		return Info{Mode: m.Perms[p], Owner: own[0], Group: own[1]}, nil
	}
	if perm, ok := m.Dirs[p]; ok {
		return Info{Mode: perm, IsDir: true, Owner: own[0], Group: own[1]}, nil
	}
	return Info{}, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
}

// Chown remembers the owner.
func (m *MemFS) Chown(p, owner, group string) error {
	p = filepath.Clean(p)
	if _, err := m.Stat(p); err != nil {
		return err
	}
	cur := m.Owners[p]
	if cur == [2]string{} {
		cur = [2]string{"root", "root"}
	}
	cur[0] = owner
	if group != "" {
		cur[1] = group
	}
	m.Owners[p] = cur
	return nil
}

// Chmod changes the mode.
func (m *MemFS) Chmod(p string, perm fs.FileMode) error {
	p = filepath.Clean(p)
	if _, ok := m.Files[p]; ok {
		m.Perms[p] = perm
		return nil
	}
	if _, ok := m.Dirs[p]; ok {
		m.Dirs[p] = perm
		return nil
	}
	return &fs.PathError{Op: "chmod", Path: p, Err: fs.ErrNotExist}
}

// Remove deletes an entry.
func (m *MemFS) Remove(p string) error {
	p = filepath.Clean(p)
	if _, err := m.Stat(p); err != nil {
		return err
	}
	delete(m.Files, p)
	delete(m.Perms, p)
	delete(m.Times, p)
	delete(m.Dirs, p)
	delete(m.Owners, p)
	return nil
}

// Append appends data.
func (m *MemFS) Append(p string, data []byte, perm fs.FileMode) error {
	p = filepath.Clean(p)
	if old, ok := m.Files[p]; ok {
		m.Files[p] = append(append([]byte(nil), old...), data...)
		m.Times[p] = m.Now()
		return nil
	}
	return m.WriteFile(p, data, perm)
}
