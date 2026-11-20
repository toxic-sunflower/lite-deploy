//go:build windows

package host

import "syscall"

// HideConsole closes the console window of the program (a task started at login must not leave a black window open).
func HideConsole() {
	_, _, _ = syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole").Call()
}
