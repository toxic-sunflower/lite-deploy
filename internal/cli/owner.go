package cli

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io/fs"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/owner"
)

const ownerUsage = "usage: lite-deploy owner list | add PUBLIC_KEY_BASE64 LABEL | remove KEY_ID"

// cmdOwner manages the keys that confirm dangerous actions (package owner). It is run by the executor as root: the agent
// asks for it. The file is public, so the agent can read it; only root changes it.
func cmdOwner(args []string, env Env) result {
	const command = "owner"
	if len(args) == 0 {
		return fail(command, CodeUsage, ownerUsage)
	}
	read := func() []owner.Key {
		b, err := env.FS.ReadFile(owner.KeysFile)
		if err != nil {
			return nil
		}
		return owner.ParseKeys(string(b))
	}
	write := func(keys []owner.Key) *result {
		var lines []string
		for _, k := range keys {
			lines = append(lines, owner.Line(k.Pub, k.Label))
		}
		if err := env.FS.MkdirAll("/etc/lite-deploy", fs.FileMode(0o755)); err != nil {
			r := fail(command, CodeIO, err.Error())
			return &r
		}
		text := strings.Join(lines, "\n")
		if text != "" {
			text += "\n"
		}
		if err := env.FS.WriteFile(owner.KeysFile, []byte(text), fs.FileMode(0o644)); err != nil {
			r := fail(command, CodeIO, err.Error())
			return &r
		}
		return nil
	}
	switch args[0] {
	case "list":
		keys := read()
		items := []map[string]string{}
		var sb strings.Builder
		for _, k := range keys {
			items = append(items, map[string]string{"id": k.ID, "label": k.Label})
			fmt.Fprintf(&sb, "%s  %s\n", k.ID, k.Label)
		}
		if sb.Len() == 0 {
			sb.WriteString("No owner key is registered: dangerous actions are not confirmed with a key.\n")
		}
		return result{command: command, data: map[string]any{"keys": items, "required": len(keys) > 0}, text: sb.String()}
	case "add":
		if !env.root() {
			return fail(command, "permission_denied", "owner add runs as root, through the agent")
		}
		if len(args) != 3 {
			return fail(command, CodeUsage, ownerUsage)
		}
		raw, err := base64.StdEncoding.DecodeString(args[1])
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return fail(command, CodeUsage, "the public key must be 32 bytes in base64")
		}
		pub := ed25519.PublicKey(raw)
		keys := read()
		for _, k := range keys {
			if k.ID == owner.KeyID(pub) {
				return result{command: command, data: map[string]any{"id": k.ID, "added": false}, text: "This key is already registered.\n"}
			}
		}
		keys = append(keys, owner.Key{ID: owner.KeyID(pub), Label: strings.Join(strings.Fields(args[2]), " "), Pub: pub})
		if bad := write(keys); bad != nil {
			return *bad
		}
		return result{command: command, data: map[string]any{"id": owner.KeyID(pub), "added": true},
			text: "The key is registered. From now on dangerous actions need to be confirmed with it, from lite-ui.\n"}
	case "remove":
		if !env.root() {
			return fail(command, "permission_denied", "owner remove runs as root, through the agent")
		}
		if len(args) != 2 {
			return fail(command, CodeUsage, ownerUsage)
		}
		var keep []owner.Key
		found := false
		for _, k := range read() {
			if k.ID == args[1] {
				found = true
				continue
			}
			keep = append(keep, k)
		}
		if !found {
			return fail(command, CodeUsage, "there is no such key")
		}
		if bad := write(keep); bad != nil {
			return *bad
		}
		return result{command: command, data: map[string]any{"removed": true}, text: "The key was removed.\n"}
	}
	return fail(command, CodeUsage, ownerUsage)
}
