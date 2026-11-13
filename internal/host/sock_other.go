//go:build !linux

package host

import (
	"errors"
	"net"
)

// PeerUID на этой системе недоступен (агент работает только на Linux).
func PeerUID(net.Conn) (int, error) {
	return -1, errors.New("SO_PEERCRED есть только в Linux")
}
