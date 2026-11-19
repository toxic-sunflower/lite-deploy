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

// ErrLocked means the lock is already held by another process.
var ErrLocked = errors.New("lock is held")

// Locker is a non-blocking exclusive file lock (flock). It keeps two deploys from running at once.
type Locker interface {
	// TryLock takes the lock or returns ErrLocked. unlock releases it.
	TryLock(path string) (unlock func(), err error)
}

// OSLocker is a real flock. The directory of the file must exist.
type OSLocker struct{}

// TryLock takes LOCK_EX|LOCK_NB on a file (creates it with mode 600).
func (OSLocker) TryLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // the path is built from a validated project name
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // the file descriptor fits into an int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil //nolint:gosec // see above
}

// FakeLocker is in-memory locking for tests.
type FakeLocker struct{ Held map[string]bool }

// TryLock takes an in-memory lock.
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

// Overwrite writes a file in place (O_TRUNC), keeping its owner and mode. A project user needs it:
// the upstream file belongs to it, but it cannot write to /etc/caddy/upstreams (rename is impossible).
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

// Overwrite writes an existing file without changing the owner and mode.
func (m *MemFS) Overwrite(p string, data []byte) error {
	p = filepath.Clean(p)
	if _, ok := m.Files[p]; !ok {
		return &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	m.Files[p] = append([]byte(nil), data...)
	m.Times[p] = m.Now()
	return nil
}

// Open opens a file for reading.
func (OSFS) Open(p string) (io.ReadCloser, error) { return os.Open(p) } //nolint:gosec // the path is set by the calling core code

// Create creates a file with the exact mode perm.
func (OSFS) Create(p string, perm fs.FileMode) (io.WriteCloser, error) {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // the path is set by the calling core code
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// Open opens a copy of the file content.
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

// Create starts writing a file; the content appears on Close.
func (m *MemFS) Create(p string, perm fs.FileMode) (io.WriteCloser, error) {
	p = filepath.Clean(p)
	m.Perms[p] = perm
	m.Times[p] = m.Now()
	return &memWriter{m: m, path: p}, nil
}
