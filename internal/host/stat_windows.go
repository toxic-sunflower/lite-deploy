//go:build windows

package host

import "os"

// fileIDs: Windows files have no numeric owner ids.
func fileIDs(os.FileInfo) (uid, gid string, ok bool) { return "", "", false }
