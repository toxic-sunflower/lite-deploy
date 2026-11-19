//go:build windows

package host

import "errors"

// TryLock is not supported on Windows: deploys run on the Linux server, not on a Windows client.
func (OSLocker) TryLock(string) (func(), error) {
	return nil, errors.New("file locks are not supported on Windows")
}
