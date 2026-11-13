package host

import (
	"net"
	"os"
	"strconv"
)

// InheritedListener возвращает сокет, переданный systemd (socket activation, протокол LISTEN_FDS).
// ok=false, если systemd сокет не передавал.
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

// ListenUnix слушает unix-сокет только для владельца (режим разработки, без systemd). Старый файл сокета заменяется.
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
