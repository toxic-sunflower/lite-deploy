// Package secrets: шифрование значений секретов ключом исполнителя и хранилище значений
// (SPEC-v0.md, раздел 7; D-10, D-38, D-41).
//
// Значение секрета в открытом виде существует только у владельца в момент ввода и у исполнителя в момент
// расшифровки. Формат шифртекста: age (X25519 + ChaCha20-Poly1305) в текстовой обёртке armor. В age нет связанных
// данных, поэтому внутрь шифруется JSON с именем проекта и секрета, и расшифровка проверяет совпадение.
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

// Пути и пределы (SPEC-v0.md, разделы 7 и 9).
const (
	DefaultKeyPath    = "/etc/lite-deploy/exec.age"
	DefaultPubPath    = "/etc/lite-deploy/exec.pub"
	DefaultStoreDir   = "/var/lib/lite-deploy/secrets"
	MaxValueBytes     = 8192
	MaxCiphertextSize = 16384
	payloadVersion    = 1
)

var (
	projectRe = regexp.MustCompile(`\A[a-z][a-z0-9-]{1,30}\z`)
	nameRe    = regexp.MustCompile(`\A_?[a-z][a-z0-9_]{1,63}\z`)
	// ArmorPattern форма шифртекста для проверки в плане и параметрах действий.
	ArmorPattern = `^-----BEGIN AGE ENCRYPTED FILE-----\n[A-Za-z0-9+/=\n]{20,}-----END AGE ENCRYPTED FILE-----$`
)

// Ошибки проверки.
var (
	ErrInvalid  = errors.New("secrets: недопустимое значение")
	ErrBinding  = errors.New("secrets: шифртекст относится к другому проекту или имени")
	ErrDecrypt  = errors.New("secrets: шифртекст не расшифрован")
	ErrNotFound = errors.New("secrets: секрет не найден")
)

type payload struct {
	V         int    `json:"v"`
	Project   string `json:"project"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	CreatedAt string `json:"created_at"`
}

// ValidName проверяет имя секрета; служебные имена с «_» разрешены.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// ValidProject проверяет имя проекта.
func ValidProject(p string) bool { return projectRe.MatchString(p) }

// ValidateValue проверяет значение: не пусто, до 8 КиБ, без NUL.
func ValidateValue(v []byte) error {
	switch {
	case len(v) == 0:
		return fmt.Errorf("%w: значение пусто", ErrInvalid)
	case len(v) > MaxValueBytes:
		return fmt.Errorf("%w: значение больше %d байт", ErrInvalid, MaxValueBytes)
	case bytes.IndexByte(v, 0) >= 0:
		return fmt.Errorf("%w: значение содержит NUL", ErrInvalid)
	}
	return nil
}

// GenerateIdentity создаёт пару X25519: закрытый ключ (AGE-SECRET-KEY-1…) и открытый (age1…).
func GenerateIdentity() (secret, public string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}
	return id.String(), id.Recipient().String(), nil
}

// Encrypt шифрует значение открытым ключом исполнителя (age1…) и привязывает его к проекту и имени.
func Encrypt(pubkey, project, name string, value []byte, now time.Time) (string, error) {
	if !ValidProject(project) {
		return "", fmt.Errorf("%w: имя проекта", ErrInvalid)
	}
	if !ValidName(name) {
		return "", fmt.Errorf("%w: имя секрета", ErrInvalid)
	}
	if err := ValidateValue(value); err != nil {
		return "", err
	}
	if !utf8Valid(value) {
		return "", fmt.Errorf("%w: значение не UTF-8", ErrInvalid)
	}
	rcpt, err := age.ParseX25519Recipient(strings.TrimSpace(pubkey))
	if err != nil {
		return "", fmt.Errorf("%w: открытый ключ: %w", ErrInvalid, err)
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
		return "", fmt.Errorf("%w: шифртекст больше %d байт", ErrInvalid, MaxCiphertextSize)
	}
	return s, nil
}

func utf8Valid(b []byte) bool { return bytes.Equal(bytes.ToValidUTF8(b, []byte{0xEF, 0xBF, 0xBD}), b) }

// Decrypt расшифровывает шифртекст закрытым ключом и проверяет привязку к проекту и имени.
func Decrypt(secretKey, ciphertext, project, name string) ([]byte, error) {
	if len(ciphertext) > MaxCiphertextSize {
		return nil, fmt.Errorf("%w: шифртекст больше %d байт", ErrDecrypt, MaxCiphertextSize)
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(secretKey))
	if err != nil {
		return nil, fmt.Errorf("%w: закрытый ключ", ErrDecrypt)
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

// Store хранилище значений: <Dir>/<проект>/<имя>, файлы 600, каталоги 700. Работает от root.
type Store struct {
	FS  host.FS
	Dir string
}

// NewStore хранилище по умолчанию.
func NewStore(fsys host.FS) *Store { return &Store{FS: fsys, Dir: DefaultStoreDir} }

// NewStoreAt хранилище в заданном каталоге.
func NewStoreAt(fsys host.FS, dir string) *Store { return &Store{FS: fsys, Dir: dir} }

func (s *Store) path(project, name string) (string, error) {
	if !ValidProject(project) || !ValidName(name) {
		return "", fmt.Errorf("%w: имя проекта или секрета", ErrInvalid)
	}
	return s.Dir + "/" + project + "/" + name, nil
}

// Put записывает значение. Возвращает true, если значение изменилось (сравнение по хэшу, а не по тексту).
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

// Get читает значение (нужно исполнителю при записи env-файлов; в CLI и API такой команды нет, D-10).
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

// Entry имя секрета и время записи.
type Entry struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

// List перечисляет имена и время записи; значений не читает.
func (s *Store) List(project string) ([]Entry, error) {
	if !ValidProject(project) {
		return nil, fmt.Errorf("%w: имя проекта", ErrInvalid)
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

// EnsureKey создаёт пару ключей исполнителя, если её ещё нет. Существующий закрытый ключ не перезаписывается никогда:
// иначе все ранее выданные шифртексты перестали бы расшифровываться. Возвращает открытый ключ и признак «создан сейчас».
func EnsureKey(fsys host.FS, keyPath, pubPath string) (pub string, created bool, err error) {
	if old, err := fsys.ReadFile(keyPath); err == nil {
		id, perr := age.ParseX25519Identity(strings.TrimSpace(string(old)))
		if perr != nil {
			return "", false, fmt.Errorf("%s есть, но не разобран: %w", keyPath, perr)
		}
		pub = id.Recipient().String()
		// открытый ключ можно восстановить из закрытого
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
