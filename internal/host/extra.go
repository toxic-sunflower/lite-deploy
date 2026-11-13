package host

import (
	"errors"
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
