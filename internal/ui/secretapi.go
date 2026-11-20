package ui

import (
	"net/http"
	"regexp"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

var secretNameRe = regexp.MustCompile(`\A[a-z][a-z0-9_]{1,63}\z`)

// secretRoutes: the secrets of a project (names only), changing one (the value is encrypted here, on the person's
// computer, with the executor key of the server), removing one, and exporting a project description without secrets.
func (s *Server) secretRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/server/config", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		return s.call(r.Context(), "GET", "/v1/server/config", nil), 200
	}))
	mux.HandleFunc("/api/secrets", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		p := r.URL.Query().Get("project")
		if !projectRe.MatchString(p) {
			return errBody("usage", "invalid project name"), 400
		}
		return s.call(r.Context(), "GET", "/v1/secrets?project="+p, nil), 200
	}))
	mux.HandleFunc("/api/project/export", s.api(http.MethodGet, func(r *http.Request) ([]byte, int) {
		p := r.URL.Query().Get("project")
		if !projectRe.MatchString(p) {
			return errBody("usage", "invalid project name"), 400
		}
		return s.call(r.Context(), "GET", "/v1/project/export?project="+p, nil), 200
	}))
	mux.HandleFunc("/api/secret/set", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Project, Name, Value string }
		if code := readLarge(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		if !projectRe.MatchString(in.Project) || !secretNameRe.MatchString(in.Name) || in.Value == "" {
			return errBody("usage", "a project, a secret name (lowercase letters, digits, _) and a value are required"), 400
		}
		pub, bad := s.serverKey(r)
		if bad != nil {
			return bad, 200
		}
		bind := in.Project
		if bind == "shared" {
			bind = secrets.SharedProject // the ciphertext of a shared secret is bound to the shared store
		}
		ct, err := secrets.Encrypt(pub, bind, in.Name, []byte(in.Value), time.Now())
		if err != nil {
			return errBody("invalid_secret", err.Error()), 400
		}
		return s.call(r.Context(), "POST", "/v1/secret", map[string]string{"project": in.Project, "name": in.Name, "ciphertext": ct}), 200
	}))
	mux.HandleFunc("/api/secret/remove", s.api(http.MethodPost, func(r *http.Request) ([]byte, int) {
		var in struct{ Project, Name string }
		if code := readBody(r, &in); code != 0 {
			return errBody("usage", "invalid request"), code
		}
		if !projectRe.MatchString(in.Project) || !secretNameRe.MatchString(in.Name) {
			return errBody("usage", "invalid project or secret name"), 400
		}
		return s.call(r.Context(), "POST", "/v1/secret/remove", map[string]string{"project": in.Project, "name": in.Name}), 200
	}))
}
