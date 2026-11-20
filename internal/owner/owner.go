// Package owner implements confirming dangerous actions with the owner's key.
//
// The owner has an ed25519 key pair on their own workstation (lite-ui keeps the private part). The public part is
// registered on the server. From then on the agent refuses the actions that change the server unless the request is
// signed with a registered key, so someone who only got hold of an administrator account on the server cannot do them:
// they do not have the private key. Without a registered key nothing is required, so the protection is opt-in.
package owner

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// KeysFile holds the registered public keys, one per line: "ID BASE64_PUBLIC_KEY LABEL". It is readable by everyone (the keys
// are public) and writable by root only.
const KeysFile = "/etc/lite-deploy/owner-keys"

// Window is how old a signature may be.
const Window = 5 * time.Minute

// Key is a registered public key.
type Key struct {
	ID    string
	Label string
	Pub   ed25519.PublicKey
}

// KeyID is the short identifier of a public key.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:6])
}

// ParseKeys reads the keys file text; bad lines are ignored.
func ParseKeys(text string) []Key {
	var out []Key
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(f[1])
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		pub := ed25519.PublicKey(raw)
		out = append(out, Key{ID: KeyID(pub), Label: strings.Join(f[2:], " "), Pub: pub})
	}
	return out
}

// Line is the text of a key for the keys file.
func Line(pub ed25519.PublicKey, label string) string {
	label = strings.Join(strings.Fields(label), " ")
	return KeyID(pub) + " " + base64.StdEncoding.EncodeToString(pub) + " " + label
}

// Message is what is signed: the server, the request and a moment, so a signature cannot be reused for another request,
// another server, or later.
func Message(serverID, method, path string, body []byte, ts int64, nonce string) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.Join([]string{"lite-deploy-owner-v1", serverID, method, path, hex.EncodeToString(sum[:]), strconv.FormatInt(ts, 10), nonce}, "\n"))
}

// GenerateKey makes a new key pair.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// Sign makes the value of the signature header.
func Sign(priv ed25519.PrivateKey, serverID, method, path string, body []byte, now time.Time) (string, error) {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return "", errors.New("bad private key")
	}
	nb := make([]byte, 12)
	if _, err := rand.Read(nb); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(nb)
	ts := now.Unix()
	sig := ed25519.Sign(priv, Message(serverID, method, path, body, ts, nonce))
	return fmt.Sprintf("v1.%s.%d.%s.%s", KeyID(pub), ts, nonce, base64.RawURLEncoding.EncodeToString(sig)), nil
}

// Verifier checks signatures and remembers the nonces it has seen, so that a recorded request cannot be replayed.
type Verifier struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// Verify checks the header value against the registered keys.
func (v *Verifier) Verify(keys []Key, serverID, method, path string, body []byte, header string, now time.Time) error {
	p := strings.Split(header, ".")
	if len(p) != 5 || p[0] != "v1" {
		return errors.New("this action must be confirmed with your key")
	}
	ts, err := strconv.ParseInt(p[2], 10, 64)
	if err != nil {
		return errors.New("the confirmation is malformed")
	}
	if d := now.Sub(time.Unix(ts, 0)); d > Window || d < -Window {
		return errors.New("the confirmation has expired: check the clock of your computer and try again")
	}
	sig, err := base64.RawURLEncoding.DecodeString(p[4])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("the confirmation is malformed")
	}
	var key *Key
	for i := range keys {
		if keys[i].ID == p[1] {
			key = &keys[i]
		}
	}
	if key == nil {
		return errors.New("this key is not registered on the server")
	}
	if !ed25519.Verify(key.Pub, Message(serverID, method, path, body, ts, p[3]), sig) {
		return errors.New("the confirmation is not valid")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seen == nil {
		v.seen = map[string]time.Time{}
	}
	for n, t := range v.seen {
		if now.Sub(t) > 2*Window {
			delete(v.seen, n)
		}
	}
	if _, dup := v.seen[p[3]]; dup {
		return errors.New("this confirmation was already used")
	}
	v.seen[p[3]] = now
	return nil
}
