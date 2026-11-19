//go:build !windows

package host

import (
	"os"
	"strconv"
	"syscall"
)

// fileIDs returns the numeric owner and group ids of a file.
func fileIDs(st os.FileInfo) (uid, gid string, ok bool) {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", false
	}
	return strconv.FormatUint(uint64(sys.Uid), 10), strconv.FormatUint(uint64(sys.Gid), 10), true
}
