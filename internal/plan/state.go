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

// Пути на сервере (SPEC-v0.md, раздел 9).
const (
	DefaultStateDir  = "/var/lib/lite-deploy/state"
	DefaultMachineID = "/etc/machine-id"
	DefaultPasswd    = "/etc/passwd"
)

// Paths откуда читать состояние; в тестах и клиентском режиме можно подменить.
type Paths struct {
	StateDir  string
	MachineID string
	Passwd    string
}

// DefaultPaths пути по умолчанию.
func DefaultPaths() Paths {
	return Paths{StateDir: DefaultStateDir, MachineID: DefaultMachineID, Passwd: DefaultPasswd}
}

// LoadState читает несекретное состояние сервера: id сервера, управляемые проекты и существующих пользователей.
// Права root не нужны. Отсутствие каталога state/projects означает «проектов ещё нет».
func LoadState(fsys host.FS, p Paths) (State, error) {
	var st State
	mid, err := fsys.ReadFile(p.MachineID)
	if err != nil {
		return st, fmt.Errorf("не удалось прочитать %s: %w", p.MachineID, err)
	}
	if strings.TrimSpace(string(mid)) == "" {
		return st, fmt.Errorf("%s пуст", p.MachineID)
	}
	st.ServerID = ServerID(string(mid))

	st.Projects = map[string]ProjectState{}
	dir := p.StateDir + "/projects"
	names, err := fsys.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return st, fmt.Errorf("не удалось прочитать %s: %w", dir, err)
	}
	for _, n := range names {
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		raw, err := fsys.ReadFile(dir + "/" + n)
		if err != nil {
			return st, fmt.Errorf("не удалось прочитать %s: %w", n, err)
		}
		var ps ProjectState
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err := dec.Decode(&ps); err != nil {
			return st, fmt.Errorf("состояние проекта %s повреждено: %w", n, err)
		}
		st.Projects[strings.TrimSuffix(n, ".json")] = ps
	}

	st.ExistingUsers = map[string]bool{}
	if pw, err := fsys.ReadFile(p.Passwd); err == nil {
		for _, line := range strings.Split(string(pw), "\n") {
			if i := strings.IndexByte(line, ':'); i > 0 {
				st.ExistingUsers[line[:i]] = true
			}
		}
	}
	return st, nil
}
