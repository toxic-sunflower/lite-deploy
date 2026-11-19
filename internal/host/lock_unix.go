//go:build !windows

package host

import (
	"errors"
	"os"
	"syscall"
)

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
