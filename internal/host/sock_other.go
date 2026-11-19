//go:build !linux

package host

import (
	"errors"
	"net"
)

// PeerUID is unavailable on this system (the agent runs on Linux only).
func PeerUID(net.Conn) (int, error) {
	return -1, errors.New("SO_PEERCRED exists on Linux only")
}
