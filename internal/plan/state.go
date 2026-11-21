package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Paths on the server (SPEC-v0.md, section 9).
const (
	DefaultStateDir  = "/var/lib/lite-deploy/state"
	DefaultMachineID = "/etc/machine-id"
	DefaultPasswd    = "/etc/passwd"
)

// Paths is where to read state from; it can be replaced in tests and in client mode.
type Paths struct {
	StateDir  string
	MachineID string
	Passwd    string
}

// DefaultPaths are the default paths.
func DefaultPaths() Paths {
	return Paths{StateDir: DefaultStateDir, MachineID: DefaultMachineID, Passwd: DefaultPasswd}
}

// LoadState reads the non-secret server state: the server id, managed projects and existing users.
// Root rights are not needed. A missing state/projects directory means "no projects yet".
func LoadState(fsys host.FS, p Paths) (State, error) {
	var st State
	mid, err := fsys.ReadFile(p.MachineID)
	if err != nil {
		return st, fmt.Errorf("could not read %s: %w", p.MachineID, err)
	}
	if strings.TrimSpace(string(mid)) == "" {
		return st, fmt.Errorf("%s is empty", p.MachineID)
	}
	st.ServerID = ServerID(string(mid))

	st.Projects = map[string]ProjectState{}
	dir := p.StateDir + "/projects"
	names, err := fsys.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return st, fmt.Errorf("could not read %s: %w", dir, err)
	}
	for _, n := range names {
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		raw, err := fsys.ReadFile(dir + "/" + n)
		if err != nil {
			return st, fmt.Errorf("could not read %s: %w", n, err)
		}
		var ps ProjectState
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err := dec.Decode(&ps); err != nil {
			return st, fmt.Errorf("the state of project %s is corrupt: %w", n, err)
		}
		st.Projects[strings.TrimSuffix(n, ".json")] = ps
	}

	st.ExistingUsers = map[string]bool{}
	if pw, err := fsys.ReadFile(p.Passwd); err == nil {
		for _, line := range strings.Split(string(pw), "\n") {
			f := strings.Split(line, ":")
			if len(f) < 6 || f[0] == "" {
				continue
			}
			// a user already created by an interrupted project create (our settings directory exists) is not treated as foreign:
			// running again continues the work
			if _, managed := st.Projects[f[0]]; !managed && f[5] != "" {
				if _, err := fsys.Stat(f[5] + "/.config/lite-deploy"); err == nil {
					continue
				}
			}
			st.ExistingUsers[f[0]] = true
		}
	}
	return st, nil
}
