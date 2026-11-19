package host

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLocked блокировка уже занята другим процессом.
var ErrLocked = errors.New("блокировка занята")

// Locker неблокирующая исключающая блокировка по файлу (flock). Нужна, чтобы два деплоя не шли одновременно.
type Locker interface {
	// TryLock берёт блокировку или возвращает ErrLocked. unlock снимает её.
	TryLock(path string) (unlock func(), err error)
}

// OSLocker настоящий flock. Каталог файла должен существовать.
type OSLocker struct{}

// TryLock берёт LOCK_EX|LOCK_NB на файл (создаёт с правами 600).
func (OSLocker) TryLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // путь строится из проверенного имени проекта
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // дескриптор файла помещается в int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil //nolint:gosec // см. выше
}

// FakeLocker блокировки в памяти для тестов.
type FakeLocker struct{ Held map[string]bool }

// TryLock берёт блокировку в памяти.
func (l *FakeLocker) TryLock(path string) (func(), error) {
	if l.Held == nil {
		l.Held = map[string]bool{}
	}
	if l.Held[path] {
		return nil, ErrLocked
	}
	l.Held[path] = true
	return func() { delete(l.Held, path) }, nil
}

// Overwrite записывает файл на месте (O_TRUNC), сохраняя владельца и права. Нужен проектному пользователю:
// файл upstream принадлежит ему, а в каталог /etc/caddy/upstreams писать он не может (rename невозможен).
func (OSFS) Overwrite(p string, data []byte) error {
	f, err := os.OpenFile(filepath.Clean(p), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Overwrite записывает существующий файл, не меняя владельца и права.
func (m *MemFS) Overwrite(p string, data []byte) error {
	p = filepath.Clean(p)
	if _, ok := m.Files[p]; !ok {
		return &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	m.Files[p] = append([]byte(nil), data...)
	m.Times[p] = m.Now()
	return nil
}

// Open открывает файл для чтения.
func (OSFS) Open(p string) (io.ReadCloser, error) { return os.Open(p) } //nolint:gosec // путь задаёт вызывающий код ядра

// Create создаёт файл с точными правами perm.
func (OSFS) Create(p string, perm fs.FileMode) (io.WriteCloser, error) {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // путь задаёт вызывающий код ядра
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// Open открывает копию содержимого файла.
func (m *MemFS) Open(p string) (io.ReadCloser, error) {
	b, ok := m.Files[filepath.Clean(p)]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type memWriter struct {
	m    *MemFS
	path string
	buf  bytes.Buffer
}

func (w *memWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *memWriter) Close() error {
	w.m.Files[w.path] = append([]byte(nil), w.buf.Bytes()...)
	return nil
}

// Create начинает запись файла; содержимое появляется при Close.
func (m *MemFS) Create(p string, perm fs.FileMode) (io.WriteCloser, error) {
	p = filepath.Clean(p)
	m.Perms[p] = perm
	m.Times[p] = m.Now()
	return &memWriter{m: m, path: p}, nil
}
