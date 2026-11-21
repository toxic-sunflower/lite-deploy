package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/agent"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

// projectIn is what the page sends: the project description and the secret values in plain text.
// The values are encrypted here, on the owner workstation, with the executor public key and go to the server only encrypted.
type projectIn struct {
	Project    json.RawMessage   `json:"project"`
	Secrets    map[string]string `json:"secrets"`
	ExpectHash string            `json:"expect_project_hash"`
}

func readLarge(r *http.Request, v any) int {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return http.StatusUnsupportedMediaType
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, agent.MaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return http.StatusBadRequest
	}
	return 0
}

// serverKey asks the agent for the executor public key. On an error it returns the agent answer as is.
func (s *Server) serverKey(r *http.Request) (string, []byte) {
	raw := s.call(r.Context(), "GET", "/v1/project/key", nil)
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			PublicKey string `json:"public_key"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil || !env.OK || env.Data.PublicKey == "" {
		return "", raw
	}
	return env.Data.PublicKey, nil
}

// projectCall prepares the bundle (encrypts secrets) and sends the agent a preview or a project creation.
func (s *Server) projectCall(action string) func(*http.Request) ([]byte, int) {
	return func(r *http.Request) ([]byte, int) {
		var in projectIn
		if code := readLarge(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		var head struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(in.Project, &head) != nil || !projectRe.MatchString(head.Name) {
			return errBody("usage", "the project description needs a valid name"), 400
		}
		pub, bad := s.serverKey(r)
		if bad != nil {
			return bad, 200
		}
		cts := make(map[string]string, len(in.Secrets))
		for name, val := range in.Secrets {
			ct, err := secrets.Encrypt(pub, head.Name, name, []byte(val), time.Now())
			if err != nil {
				return errBody("invalid_secret", "secret "+name+": "+err.Error()), 400
			}
			cts[name] = ct
		}
		bundle := map[string]any{"project": in.Project, "ciphertexts": cts}
		if in.ExpectHash != "" {
			bundle["expect_project_hash"] = in.ExpectHash
		}
		return s.call(r.Context(), "POST", "/v1/project/"+action, bundle), 200
	}
}

func (s *Server) projectRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/project/preview", s.api(http.MethodPost, s.projectCall("preview")))
	mux.HandleFunc("/api/project/create", s.api(http.MethodPost, s.projectCall("create")))
}
