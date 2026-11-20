package ui

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/owner"
)

type noSignKey struct{}

// ownerInfo is what the server says about confirming dangerous actions.
type ownerInfo struct {
	Supported bool
	Required  bool
	Mine      bool // the key of this computer is registered
	ServerID  string
	at        time.Time
}

func (s *Server) loadOwnerKey() (ed25519.PrivateKey, bool) {
	if s.OwnerKeyPath == "" {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Clean(s.OwnerKeyPath))
	if err != nil {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, false
	}
	return ed25519.PrivateKey(raw), true
}

func (s *Server) createOwnerKey() (ed25519.PrivateKey, error) {
	if priv, ok := s.loadOwnerKey(); ok {
		return priv, nil
	}
	if s.OwnerKeyPath == "" {
		return nil, errors.New("no place to keep the key on this computer")
	}
	_, priv, err := owner.GenerateKey()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(s.OwnerKeyPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.OwnerKeyPath, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

// ownerState asks the server whether dangerous actions need the owner's signature (cached for a few seconds).
func (s *Server) ownerState(ctx context.Context, target string) ownerInfo {
	s.mu.Lock()
	if s.owners == nil {
		s.owners = map[string]ownerInfo{}
	}
	if c, ok := s.owners[target]; ok && time.Since(c.at) < 10*time.Second {
		s.mu.Unlock()
		return c
	}
	s.mu.Unlock()
	ctx = context.WithValue(ctx, noSignKey{}, true)
	ctx = context.WithValue(ctx, ctxKey{}, target)
	var info ownerInfo
	var list struct {
		OK   bool `json:"ok"`
		Data struct {
			Keys []struct {
				ID string `json:"id"`
			} `json:"keys"`
			Required bool `json:"required"`
		} `json:"data"`
	}
	if json.Unmarshal(s.call(ctx, "GET", "/v1/owner", nil), &list) == nil && list.OK {
		info.Supported, info.Required = true, list.Data.Required
		if priv, ok := s.loadOwnerKey(); ok {
			if pub, ok := priv.Public().(ed25519.PublicKey); ok {
				for _, k := range list.Data.Keys {
					info.Mine = info.Mine || k.ID == owner.KeyID(pub)
				}
			}
		}
		var key struct {
			Data struct {
				PublicKey string `json:"public_key"`
			} `json:"data"`
		}
		if json.Unmarshal(s.call(ctx, "GET", "/v1/project/key", nil), &key) == nil {
			info.ServerID = key.Data.PublicKey
		}
	}
	info.at = time.Now()
	s.mu.Lock()
	s.owners[target] = info
	s.mu.Unlock()
	return info
}

func (s *Server) forgetOwner(target string) {
	s.mu.Lock()
	delete(s.owners, target)
	s.mu.Unlock()
}

// signFor signs a changing request when the server asks for confirmation and this computer has a registered key.
func (s *Server) signFor(ctx context.Context, target, method, path string, body []byte) string {
	if method != http.MethodPost || s.OwnerKeyPath == "" || ctx.Value(noSignKey{}) != nil {
		return ""
	}
	priv, ok := s.loadOwnerKey()
	if !ok {
		return ""
	}
	info := s.ownerState(ctx, target)
	if !info.Supported || !info.Required || info.ServerID == "" {
		return ""
	}
	sig, err := owner.Sign(priv, info.ServerID, method, path, body, time.Now())
	if err != nil {
		return ""
	}
	return sig
}

func (s *Server) ownerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/owner", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		target, _ := r.Context().Value(ctxKey{}).(string)
		if target == "" {
			target = s.Host
		}
		s.forgetOwner(target)
		info := s.ownerState(r.Context(), target)
		_, has := s.loadOwnerKey()
		b, _ := json.Marshal(map[string]any{"ok": true, "data": map[string]any{
			"supported": info.Supported, "required": info.Required, "registered": info.Mine, "has_key": has}})
		return b, 200
	}))
	mux.HandleFunc("/api/owner/pair", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Label string }
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		target, _ := r.Context().Value(ctxKey{}).(string)
		if target == "" {
			target = s.Host
		}
		priv, err := s.createOwnerKey()
		if err != nil {
			return errBody("internal", "could not create your key: "+err.Error()), 500
		}
		pub, _ := priv.Public().(ed25519.PublicKey)
		label := in.Label
		if label == "" || len(label) > 40 {
			label = "my computer"
		}
		s.forgetOwner(target)
		out := s.call(r.Context(), "POST", "/v1/owner/add", map[string]string{"pub": base64.StdEncoding.EncodeToString(pub), "label": label})
		s.forgetOwner(target)
		return out, 200
	}))
	mux.HandleFunc("/api/owner/unpair", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		target, _ := r.Context().Value(ctxKey{}).(string)
		if target == "" {
			target = s.Host
		}
		priv, ok := s.loadOwnerKey()
		if !ok {
			return errBody("usage", "this computer has no key"), 400
		}
		pub, _ := priv.Public().(ed25519.PublicKey)
		out := s.call(r.Context(), "POST", "/v1/owner/remove", map[string]string{"id": owner.KeyID(pub)})
		s.forgetOwner(target)
		return out, 200
	}))
}

// ownerKeyID is the identifier of the public key of a private key (used in tests and for the page).
func ownerKeyID(priv ed25519.PrivateKey) string {
	pub, _ := priv.Public().(ed25519.PublicKey)
	return owner.KeyID(pub)
}
