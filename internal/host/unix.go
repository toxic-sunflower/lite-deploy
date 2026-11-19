package host

import (
	"net"
	"os"
	"strconv"
)

// InheritedListener returns the socket passed by systemd (socket activation, the LISTEN_FDS protocol).
// ok=false if systemd did not pass a socket.
func InheritedListener(getenv func(string) string, pid int) (ln net.Listener, ok bool, err error) {
	if getenv == nil || getenv("LISTEN_PID") != strconv.Itoa(pid) || getenv("LISTEN_FDS") != "1" {
		return nil, false, nil
	}
	f := os.NewFile(3, "systemd-socket")
	ln, err = net.FileListener(f)
	_ = f.Close()
	if err != nil {
		return nil, true, err
	}
	return ln, true, nil
}

// ListenUnix listens on a unix socket for the owner only (development mode, without systemd). An old socket file is replaced.
func ListenUnix(path string) (net.Listener, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}
