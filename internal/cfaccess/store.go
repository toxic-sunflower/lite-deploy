package cfaccess

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
)

// Saved is a token the person chose to keep on this workstation.
type Saved struct {
	Token   string `json:"token"`
	Account string `json:"account,omitempty"`
}

// Store keeps tokens in a file only the current user can read. Nothing here ever goes to a server.
type Store struct {
	Path string
	mu   sync.Mutex
}

var labelRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,20}$`)

// ValidLabel tells whether a name for an account is acceptable.
func ValidLabel(s string) bool { return labelRe.MatchString(s) }

func (s *Store) load() (map[string]Saved, error) {
	m := map[string]Saved{}
	if s.Path == "" {
		return m, nil
	}
	b, err := os.ReadFile(filepath.Clean(s.Path))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

// Labels returns the names of the saved accounts (never the tokens).
func (s *Store) Labels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, _ := s.load()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get returns a saved token.
func (s *Store) Get(label string) (Saved, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, _ := s.load()
	v, ok := m[label]
	return v, ok
}

// Put saves a token (replacing the one of the same name).
func (s *Store) Put(label string, v Saved) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Path == "" {
		return errors.New("there is no place to keep settings on this workstation")
	}
	m, err := s.load()
	if err != nil {
		return err
	}
	m[label] = v
	return s.write(m)
}

// Delete forgets a token.
func (s *Store) Delete(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	delete(m, label)
	return s.write(m)
}

func (s *Store) write(m map[string]Saved) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(m)
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}
