// Package minisign checks a minisign signature (https://jedisct1.github.io/minisign/): a public key and a ".minisig" file
// are enough to tell that a file is what the key's owner signed. Releases of lite-deploy sign SHA256SUMS with it.
package minisign

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// PublicKey is a key as written in minisign.pub (the line starting with RW).
type PublicKey struct {
	keyID [8]byte
	key   ed25519.PublicKey
}

// ParsePublicKey reads the base64 line of a public key.
func ParsePublicKey(s string) (PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != 2+8+32 || raw[0] != 'E' || raw[1] != 'd' {
		return PublicKey{}, errors.New("minisign: not a public key")
	}
	var pk PublicKey
	copy(pk.keyID[:], raw[2:10])
	pk.key = ed25519.PublicKey(append([]byte(nil), raw[10:]...))
	return pk, nil
}

// Verify checks sig (the content of a .minisig file) against msg. Both the plain ("Ed") and the pre-hashed ("ED") forms
// are accepted; the trusted comment is covered by the second signature and is checked too.
func (pk PublicKey) Verify(msg, sig []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(sig))
	var lines []string
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "untrusted comment:") || !strings.HasPrefix(lines[2], "trusted comment:") {
		return errors.New("minisign: the signature file is not valid")
	}
	raw, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil || len(raw) != 2+8+64 {
		return errors.New("minisign: the signature is not valid")
	}
	if !bytes.Equal(raw[2:10], pk.keyID[:]) {
		return errors.New("minisign: signed with another key")
	}
	body := msg
	switch string(raw[:2]) {
	case "Ed":
	case "ED":
		h := blake2b.Sum512(msg)
		body = h[:]
	default:
		return errors.New("minisign: unknown signature type")
	}
	sigBytes := raw[10:]
	if !ed25519.Verify(pk.key, body, sigBytes) {
		return errors.New("minisign: the signature does not match the file")
	}
	global, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil || len(global) != 64 {
		return errors.New("minisign: the global signature is not valid")
	}
	trusted := strings.TrimPrefix(lines[2], "trusted comment: ")
	if !ed25519.Verify(pk.key, append(append([]byte(nil), sigBytes...), trusted...), global) {
		return errors.New("minisign: the comment of the signature was changed")
	}
	return nil
}
