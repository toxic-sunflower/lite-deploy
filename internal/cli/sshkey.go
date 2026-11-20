package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/setup"
)

const sshKeyUsage = "usage: lite ssh-key list | add \"ssh-ed25519 AAAA… comment\" | remove FINGERPRINT"

// fingerprint is the SHA256 fingerprint of a public key line, as ssh-keygen -l prints it.
func fingerprint(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// cmdSSHKey changes the SSH keys of the user who runs it (their own ~/.ssh/authorized_keys, no root needed).
// A key is replaced in two steps that cannot lock anyone out: add the new key, log in with it, then remove the old one.
func cmdSSHKey(args []string, env Env) result {
	const command = "ssh-key"
	if len(args) == 0 {
		return fail(command, CodeUsage, sshKeyUsage)
	}
	home := ""
	if env.Getenv != nil {
		home = env.Getenv("HOME")
	}
	if home == "" || env.root() {
		return fail(command, CodeUsage, "run this as your own administrator user, not as root")
	}
	dir, file := home+"/.ssh", home+"/.ssh/authorized_keys"
	raw, err := env.FS.ReadFile(file)
	if err != nil && !isNotExist(err) {
		return fail(command, CodeIO, "could not read "+file+": "+err.Error())
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	write := func(list []string) *result {
		if err := env.FS.MkdirAll(dir, fs.FileMode(0o700)); err != nil {
			r := fail(command, CodeIO, err.Error())
			return &r
		}
		if err := env.FS.WriteFile(file, []byte(strings.Join(list, "\n")+"\n"), fs.FileMode(0o600)); err != nil {
			r := fail(command, CodeIO, "could not write "+file+": "+err.Error())
			return &r
		}
		return nil
	}
	switch args[0] {
	case "list":
		var sb strings.Builder
		keys := []map[string]string{}
		for _, l := range lines {
			f := strings.Fields(l)
			if !setup.ValidKey(l) {
				continue // lines with options (for example the CI key) are not shown here
			}
			comment := ""
			if len(f) > 2 {
				comment = strings.Join(f[2:], " ")
			}
			fmt.Fprintf(&sb, "%s  %s  %s\n", f[0], fingerprint(l), comment)
			keys = append(keys, map[string]string{"type": f[0], "fingerprint": fingerprint(l), "comment": comment})
		}
		if sb.Len() == 0 {
			sb.WriteString("No SSH keys are set for you.\n")
		}
		return result{command: command, data: map[string]any{"keys": keys}, text: sb.String()}
	case "add":
		if len(args) != 2 || !setup.ValidKey(args[1]) {
			return fail(command, CodeUsage, sshKeyUsage)
		}
		key := strings.TrimSpace(args[1])
		for _, l := range lines {
			if fingerprint(l) == fingerprint(key) {
				return result{command: command, data: map[string]any{"fingerprint": fingerprint(key), "added": false}, text: "This key is already there.\n"}
			}
		}
		if bad := write(append(lines, key)); bad != nil {
			return *bad
		}
		return result{command: command, data: map[string]any{"fingerprint": fingerprint(key), "added": true},
			text: "The key was added; your old keys still work.\nNext: open a new terminal and log in with the new key. When it works, remove the old key: lite ssh-key list, then lite ssh-key remove FINGERPRINT\n"}
	case "remove":
		if len(args) != 2 {
			return fail(command, CodeUsage, sshKeyUsage)
		}
		var keep []string
		removed, left := 0, 0
		for _, l := range lines {
			if setup.ValidKey(l) && fingerprint(l) == args[1] {
				removed++
				continue
			}
			if setup.ValidKey(l) {
				left++
			}
			keep = append(keep, l)
		}
		switch {
		case removed == 0:
			return fail(command, CodeUsage, "there is no such key: see lite ssh-key list")
		case left == 0:
			return fail(command, "precondition_failed", "this is your only key: removing it would lock you out. Add the new key first: lite ssh-key add \"…\"")
		}
		if bad := write(keep); bad != nil {
			return *bad
		}
		return result{command: command, data: map[string]any{"removed": removed}, text: "The key was removed.\n"}
	}
	return fail(command, CodeUsage, sshKeyUsage)
}

func isNotExist(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such file") || err == fs.ErrNotExist
}
