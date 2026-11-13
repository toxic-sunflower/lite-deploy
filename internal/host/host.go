// Package host единственное место, где код обращается к системе: запуск команд и файлы.
// Любая внешняя команда запускается только через Runner: массив аргументов, без оболочки, с таймаутом.
// Прямых вызовов os/exec вне этого пакета нет (проверяет тест в internal/lint).
package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// Cmd команда без оболочки.
type Cmd struct {
	Name    string
	Args    []string
	Stdin   []byte
	Env     []string      // дополнительные переменные, формат NAME=value
	Timeout time.Duration // 0 = 30 секунд
}

// Result результат запуска.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner запускает команды.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// ExecRunner настоящий запуск процессов.
type ExecRunner struct{}

// Run запускает команду; ненулевой код выхода возвращается в Result, а не как ошибка.
func (ExecRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	if c.Name == "" {
		return Result{}, errors.New("пустое имя команды")
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
	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	if ctx.Err() != nil {
		return res, fmt.Errorf("команда %s остановлена по таймауту %s: %w", c.Name, t, ctx.Err())
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err
}

// FS файловые операции, нужные ядру.
type FS interface {
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]string, error) // имена записей по алфавиту
	// WriteFile атомарно записывает файл (временный файл и rename) с точными правами perm, независимо от umask.
	WriteFile(path string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	ModTime(path string) (time.Time, error)
	// Stat описывает файл или каталог; для отсутствующего возвращает ошибку, совместимую с fs.ErrNotExist.
	Stat(path string) (Info, error)
	// Chown меняет владельца и группу по именам (пустая группа: группа не меняется).
	Chown(path, owner, group string) error
	Chmod(path string, perm fs.FileMode) error
	Remove(path string) error
	// Append дописывает в файл; если файла нет, создаёт его с правами perm.
	Append(path string, data []byte, perm fs.FileMode) error
	// Overwrite записывает существующий файл на месте, сохраняя владельца и права.
	Overwrite(path string, data []byte) error
}

// Info описание записи файловой системы.
type Info struct {
	Mode  fs.FileMode // только права доступа
	IsDir bool
	Owner string
	Group string
}

// OSFS настоящая файловая система.
type OSFS struct{}

// ReadFile читает файл.
func (OSFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

// ReadDir возвращает имена записей каталога.
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

// WriteFile атомарно записывает файл с правами perm.
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

// MkdirAll создаёт каталог с правами perm (для вновь созданных каталогов, независимо от umask).
func (OSFS) MkdirAll(p string, perm fs.FileMode) error {
	if err := os.MkdirAll(p, perm); err != nil {
		return err
	}
	return os.Chmod(p, perm)
}

// ModTime время изменения файла.
func (OSFS) ModTime(p string) (time.Time, error) {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

// MemFS файловая система в памяти для тестов; права и время записей хранятся отдельно.
type MemFS struct {
	Files map[string][]byte
	Perms map[string]fs.FileMode
	Times map[string]time.Time
	Dirs  map[string]fs.FileMode
	// Owners владельцы и группы; нет записи означает root:root. Запись файла сбрасывает владельца, как rename настоящего WriteFile.
	Owners map[string][2]string
	Now    func() time.Time
}

// NewMemFS создаёт пустую MemFS.
func NewMemFS() *MemFS {
	return &MemFS{Files: map[string][]byte{}, Perms: map[string]fs.FileMode{}, Times: map[string]time.Time{}, Dirs: map[string]fs.FileMode{}, Owners: map[string][2]string{}, Now: time.Now}
}

// WriteFile записывает файл.
func (m *MemFS) WriteFile(p string, data []byte, perm fs.FileMode) error {
	p = filepath.Clean(p)
	m.Files[p] = append([]byte(nil), data...)
	m.Perms[p] = perm
	m.Times[p] = m.Now()
	delete(m.Owners, p)
	return nil
}

// MkdirAll запоминает каталог и недостающие родительские.
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

// ModTime возвращает время записи.
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

// ReadFile читает файл.
func (m *MemFS) ReadFile(p string) ([]byte, error) {
	b, ok := m.Files[filepath.Clean(p)]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	return b, nil
}

// ReadDir возвращает имена непосредственных записей.
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

// FakeRunner записывает команды и отвечает заготовленными результатами; для тестов.
type FakeRunner struct {
	Calls   []Cmd
	Handler func(c Cmd) (Result, error)
}

// Run записывает вызов и вызывает Handler (если он задан).
func (f *FakeRunner) Run(_ context.Context, c Cmd) (Result, error) {
	f.Calls = append(f.Calls, c)
	if f.Handler == nil {
		return Result{}, fmt.Errorf("FakeRunner: нет обработчика для %s", c.Name)
	}
	return f.Handler(c)
}

// Stat описывает запись.
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

// Chown меняет владельца и группу по именам.
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

// Chmod меняет права.
func (OSFS) Chmod(p string, perm fs.FileMode) error { return os.Chmod(p, perm) }

// Remove удаляет файл или пустой каталог.
func (OSFS) Remove(p string) error { return os.Remove(p) }

// Append дописывает данные в файл.
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

// Stat описывает запись.
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

// Chown запоминает владельца.
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

// Chmod меняет права.
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

// Remove удаляет запись.
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

// Append дописывает данные.
func (m *MemFS) Append(p string, data []byte, perm fs.FileMode) error {
	p = filepath.Clean(p)
	if old, ok := m.Files[p]; ok {
		m.Files[p] = append(append([]byte(nil), old...), data...)
		m.Times[p] = m.Now()
		return nil
	}
	return m.WriteFile(p, data, perm)
}
