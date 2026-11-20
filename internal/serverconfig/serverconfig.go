// Package serverconfig is the description of a server itself (not of its projects) as a file: who may log in, the firewall,
// the programs, protection, backups. It can be saved from a running server (`lite config export`) or written by hand, and
// handed to `lite init --config FILE` on a new server.
package serverconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/setup"
)

// Schema is the value of the schema field.
const Schema = "lite-deploy/server/v1"

// Config is a server.
type Config struct {
	Schema   string   `json:"schema"`
	Users    []User   `json:"users,omitempty"`
	Firewall string   `json:"firewall,omitempty"` // keep, off, http or max
	Plugins  []string `json:"plugins,omitempty"`
	Security *bool    `json:"security,omitempty"` // default true
	Backup   *Backup  `json:"backup,omitempty"`
}

// User is an administrator with the public keys that may log in.
type User struct {
	Name    string   `json:"name"`
	SSHKeys []string `json:"ssh_keys,omitempty"`
}

// Backup is the server-wide backup setting. Only public values are here: the key is the PUBLIC age key.
type Backup struct {
	Recipient    string `json:"recipient"`
	EncryptDumps bool   `json:"encrypt_dumps,omitempty"`
	Schedule     bool   `json:"schedule,omitempty"`
}

var (
	userRe      = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	recipientRe = regexp.MustCompile(`^age1[a-z0-9]{50,70}$`)
	pluginNames = map[string]bool{"caddy": true, "podman": true, "pg": true, "redis": true, "cloudflare": true, "backblaze": true, "s3": true, "sftp": true}
)

// Parse reads and checks a configuration; it returns every problem found, one sentence each.
func Parse(b []byte) (*Config, []string) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, []string{"the file is not a valid server configuration: " + err.Error()}
	}
	var bad []string
	if c.Schema != Schema {
		bad = append(bad, fmt.Sprintf("schema must be %q", Schema))
	}
	seen := map[string]bool{}
	for _, u := range c.Users {
		switch {
		case !userRe.MatchString(u.Name):
			bad = append(bad, fmt.Sprintf("user %q: a name is lowercase letters, digits, - and _", u.Name))
		case seen[u.Name]:
			bad = append(bad, fmt.Sprintf("user %q is listed twice", u.Name))
		}
		seen[u.Name] = true
		for _, k := range u.SSHKeys {
			if !setup.ValidKey(k) {
				bad = append(bad, fmt.Sprintf("user %s: %q is not a public SSH key", u.Name, shorten(k)))
			}
		}
	}
	switch c.Firewall {
	case "", "keep", "off", "http", "max":
	default:
		bad = append(bad, `firewall must be one of keep, off, http, max`)
	}
	for _, p := range c.Plugins {
		if !pluginNames[p] {
			bad = append(bad, fmt.Sprintf("plugin %q is not one of caddy, podman, pg, redis, cloudflare, backblaze, s3, sftp", p))
		}
	}
	if c.Backup != nil && !recipientRe.MatchString(c.Backup.Recipient) {
		bad = append(bad, "backup.recipient must be a public age key (age1…); the private part never goes into this file")
	}
	if len(bad) > 0 {
		return nil, bad
	}
	return &c, nil
}

func shorten(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// SecurityOn reports whether protection is on (it is by default).
func (c *Config) SecurityOn() bool { return c.Security == nil || *c.Security }
