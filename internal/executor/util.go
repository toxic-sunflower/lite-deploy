package executor

import (
	"io/fs"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

func fsMode(m fs.FileMode) fs.FileMode { return m }

func hostCmd(name string, args ...string) host.Cmd {
	return host.Cmd{Name: name, Args: args, Timeout: 30 * time.Second}
}
