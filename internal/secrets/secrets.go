// Package secrets: encrypting secret values with the executor key and the value store
// (SPEC-v0.md, section 7; D-10, D-38, D-41).
//
// A secret value in clear text exists only with the owner at the moment of input and with the executor at the moment
// decryption. The ciphertext format: age (X25519 + ChaCha20-Poly1305) in a text armor wrapper. age has no associated
// the data, so a JSON with the project and secret names is encrypted inside, and decryption checks that they match.
package secrets

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
)

// Paths and limits (SPEC-v0.md, sections 7 and 9).
const (
	DefaultKeyPath    = "/etc/lite-deploy/exec.age"
	DefaultPubPath    = "/etc/lite-deploy/exec.pub"
	DefaultStoreDir   = "/var/lib/lite-deploy/secrets"
	MaxValueBytes     = 8192
	MaxCiphertextSize = 16384
	payloadVersion    = 1
)

var (
	projectRe = regexp.MustCompile(`\A([a-z][a-z0-9-]{1,30}|_shared)\z`)
	nameRe    = regexp.MustCompile(`\A_?[a-z][a-z0-9_]{1,63}\z`)
	// ArmorPattern is the ciphertext shape for checks in the plan and in action parameters.
	ArmorPattern = `^-----BEGIN AGE ENCRYPTED FILE-----\n[A-Za-z0-9+/=\n]{20,}-----END AGE ENCRYPTED FILE-----$`
)

// Validation errors.
var (
	ErrInvalid  = errors.New("secrets: invalid value")
	ErrBinding  = errors.New("secrets: the ciphertext belongs to another project or name")
	ErrDecrypt  = errors.New("secrets: ciphertext could not be decrypted")
	ErrNotFound = errors.New("secrets: secret not found")
)

type payload struct {
	V         int    `json:"v"`
	Project   string `json:"project"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	CreatedAt string `json:"created_at"`
}

// SharedProject is the store of the secrets that several projects use; a project refers to one as secret://shared/NAME.
const (
	SharedProject = "_shared"
	SharedPrefix  = "shared/"
)

// ValidName validates a secret name; internal names with "_" are allowed.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// ValidProject validates a project name.
func ValidProject(p string) bool { return projectRe.MatchString(p) }

// ValidateValue validates a value: not empty, up to 8 KiB, no NUL.
func ValidateValue(v []byte) error {
	switch {
	case len(v) == 0:
		return fmt.Errorf("%w: value is empty", ErrInvalid)
	case len(v) > MaxValueBytes:
		return fmt.Errorf("%w: value is larger than %d bytes", ErrInvalid, MaxValueBytes)
	case bytes.IndexByte(v, 0) >= 0:
		return fmt.Errorf("%w: value contains NUL", ErrInvalid)
	}
	return nil
}

// GenerateIdentity creates an X25519 pair: a private key (AGE-SECRET-KEY-1…) and a public one (age1…).
func GenerateIdentity() (secret, public string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}
	return id.String(), id.Recipient().String(), nil
}

// Encrypt encrypts a value with the executor public key (age1…) and binds it to the project and name.
func Encrypt(pubkey, project, name string, value []byte, now time.Time) (string, error) {
	if !ValidProject(project) {
		return "", fmt.Errorf("%w: project name", ErrInvalid)
	}
	if !ValidName(name) {
		return "", fmt.Errorf("%w: secret name", ErrInvalid)
	}
	if err := ValidateValue(value); err != nil {
		return "", err
	}
	if !utf8Valid(value) {
		return "", fmt.Errorf("%w: value is not UTF-8", ErrInvalid)
	}
	rcpt, err := age.ParseX25519Recipient(strings.TrimSpace(pubkey))
	if err != nil {
		return "", fmt.Errorf("%w: public key: %w", ErrInvalid, err)
	}
	raw, err := json.Marshal(payload{V: payloadVersion, Project: project, Name: name, Value: string(value), CreatedAt: now.UTC().Format(time.RFC3339)})
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	aw := armor.NewWriter(&out)
	w, err := age.Encrypt(aw, rcpt)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(raw); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	if err := aw.Close(); err != nil {
		return "", err
	}
	s := strings.TrimRight(out.String(), "\n")
	if len(s) > MaxCiphertextSize {
		return "", fmt.Errorf("%w: ciphertext is larger than %d bytes", ErrInvalid, MaxCiphertextSize)
	}
	return s, nil
}

func utf8Valid(b []byte) bool { return bytes.Equal(bytes.ToValidUTF8(b, []byte{0xEF, 0xBF, 0xBD}), b) }

// Decrypt decrypts a ciphertext with the private key and checks the binding to the project and name.
func Decrypt(secretKey, ciphertext, project, name string) ([]byte, error) {
	if len(ciphertext) > MaxCiphertextSize {
		return nil, fmt.Errorf("%w: ciphertext is larger than %d bytes", ErrDecrypt, MaxCiphertextSize)
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(secretKey))
	if err != nil {
		return nil, fmt.Errorf("%w: private key", ErrDecrypt)
	}
	r, err := age.Decrypt(armor.NewReader(strings.NewReader(ciphertext)), id)
	if err != nil {
		return nil, ErrDecrypt
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxValueBytes*2+1024))
	if err != nil {
		return nil, ErrDecrypt
	}
	var p payload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || p.V != payloadVersion {
		return nil, ErrDecrypt
	}
	if p.Project != project || p.Name != name {
		return nil, ErrBinding
	}
	v := []byte(p.Value)
	if err := ValidateValue(v); err != nil {
		return nil, err
	}
	return v, nil
}

// Store is a value store: <Dir>/<project>/<name>, files 600, directories 700. Runs as root.
type Store struct {
	FS  host.FS
	Dir string
}

// NewStore is the default store.
func NewStore(fsys host.FS) *Store { return &Store{FS: fsys, Dir: DefaultStoreDir} }

// NewStoreAt is a store in the given directory.
func NewStoreAt(fsys host.FS, dir string) *Store { return &Store{FS: fsys, Dir: dir} }

func (s *Store) path(project, name string) (string, error) {
	if rest, ok := strings.CutPrefix(name, SharedPrefix); ok {
		project, name = SharedProject, rest // a reference to a shared secret reads the shared store
	}
	if !ValidProject(project) || !ValidName(name) {
		return "", fmt.Errorf("%w: project or secret name", ErrInvalid)
	}
	return s.Dir + "/" + project + "/" + name, nil
}

// Put writes a value. It returns true if the value changed (compared by hash, not by text).
func (s *Store) Put(project, name string, value []byte) (bool, error) {
	if err := ValidateValue(value); err != nil {
		return false, err
	}
	p, err := s.path(project, name)
	if err != nil {
		return false, err
	}
	if old, err := s.FS.ReadFile(p); err == nil {
		a, b := sha256.Sum256(old), sha256.Sum256(value)
		if subtle.ConstantTimeCompare(a[:], b[:]) == 1 {
			return false, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := s.FS.MkdirAll(s.Dir, 0o700); err != nil {
		return false, err
	}
	if err := s.FS.MkdirAll(s.Dir+"/"+project, 0o700); err != nil {
		return false, err
	}
	return true, s.FS.WriteFile(p, value, 0o600)
}

// Get reads a value (the executor needs it to write env files; the CLI and API have no such command, D-10).
func (s *Store) Get(project, name string) ([]byte, error) {
	p, err := s.path(project, name)
	if err != nil {
		return nil, err
	}
	b, err := s.FS.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

// Entry is a secret name and the time it was written.
type Entry struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

// List lists names and write times; it does not read values.
func (s *Store) List(project string) ([]Entry, error) {
	if !ValidProject(project) {
		return nil, fmt.Errorf("%w: project name", ErrInvalid)
	}
	names, err := s.FS.ReadDir(s.Dir + "/" + project)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]Entry, 0, len(names))
	for _, n := range names {
		if !ValidName(n) {
			continue
		}
		t, err := s.FS.ModTime(s.Dir + "/" + project + "/" + n)
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Name: n, UpdatedAt: t.UTC().Truncate(time.Second)})
	}
	return out, nil
}

// EnsureKey creates the executor key pair if there is none yet. An existing private key is never overwritten:
// otherwise all previously issued ciphertexts would stop decrypting. It returns the public key and a "created now" flag.
func EnsureKey(fsys host.FS, keyPath, pubPath string) (pub string, created bool, err error) {
	if old, err := fsys.ReadFile(keyPath); err == nil {
		id, perr := age.ParseX25519Identity(strings.TrimSpace(string(old)))
		if perr != nil {
			return "", false, fmt.Errorf("%s exists but could not be parsed: %w", keyPath, perr)
		}
		pub = id.Recipient().String()
		// the public key can be restored from the private one
		if cur, err := fsys.ReadFile(pubPath); err != nil || strings.TrimSpace(string(cur)) != pub {
			if err := fsys.WriteFile(pubPath, []byte(pub+"\n"), 0o644); err != nil {
				return "", false, err
			}
		}
		return pub, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	sec, pub, err := GenerateIdentity()
	if err != nil {
		return "", false, err
	}
	if err := fsys.MkdirAll(dirOf(keyPath), 0o755); err != nil {
		return "", false, err
	}
	if err := fsys.WriteFile(keyPath, []byte(sec+"\n"), 0o600); err != nil {
		return "", false, err
	}
	if err := fsys.WriteFile(pubPath, []byte(pub+"\n"), 0o644); err != nil {
		return "", false, err
	}
	return pub, true, nil
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "/"
}
