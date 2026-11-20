package host

import (
	"io"
	"os"
	"os/exec"
)

// StartDetached starts a program and does not wait for it; extra are added to the environment (NAME=value). It is used
// for restarting lite-ui after an update.
func StartDetached(name string, args, extra []string, out, errw io.Writer) error {
	cmd := exec.Command(name, args...) //nolint:gosec // the caller passes our own program
	cmd.Env = append(os.Environ(), extra...)
	cmd.Stdout, cmd.Stderr = out, errw
	return cmd.Start()
}
