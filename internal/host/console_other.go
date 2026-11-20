//go:build !windows

package host

// HideConsole does nothing outside Windows.
func HideConsole() {}
