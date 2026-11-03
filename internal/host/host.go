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
	"path/filepath"
	"sort"
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

// MemFS файловая система в памяти для тестов.
type MemFS struct{ Files map[string][]byte }

// NewMemFS создаёт пустую MemFS.
func NewMemFS() *MemFS { return &MemFS{Files: map[string][]byte{}} }

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
