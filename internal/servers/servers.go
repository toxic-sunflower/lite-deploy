// Package servers is the registry of servers known to this server: a name and an address on the internal network (R-26).
// A project lives on one server; foreign resources (for example a database) are connected by an address from the registry.
package servers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"regexp"
	"sort"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Path is the registry file; any user can read it (addresses are not secret), root writes it.
const Path = "/etc/lite-deploy/servers.json"

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// Local is the reserved name of this server.
const Local = "local"

// ValidName validates a server name.
func ValidName(n string) bool { return nameRe.MatchString(n) && n != Local }

// ValidAddress validates an address: an IP or a host name. 0.0.0.0, loopback and an empty address are not accepted.
func ValidAddress(a string) error {
	if ip, err := netip.ParseAddr(a); err == nil {
		if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			return fmt.Errorf("address %s is not usable from another server", a)
		}
		return nil
	}
	if len(a) > 253 || !hostRe.MatchString(a) {
		return errors.New("address: an IP or a host name")
	}
	return nil
}

// Load reads the registry: name → address. No file: an empty registry.
func Load(f host.FS) (map[string]string, error) {
	b, err := f.ReadFile(Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var j struct {
		Servers map[string]string `json:"servers"`
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("the server registry %s is corrupt: %w", Path, err)
	}
	if j.Servers == nil {
		j.Servers = map[string]string{}
	}
	return j.Servers, nil
}

// Save writes the registry.
func Save(f host.FS, m map[string]string) error {
	b, _ := json.MarshalIndent(map[string]any{"servers": m}, "", "  ")
	if err := f.MkdirAll("/etc/lite-deploy", 0o755); err != nil {
		return err
	}
	return f.WriteFile(Path, append(b, '\n'), 0o644)
}

// Names returns names in alphabetical order.
func Names(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
