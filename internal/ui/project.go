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

// projectIn то, что присылает страница: описание проекта и значения секретов открытым текстом.
// Значения шифруются здесь, на компьютере владельца, открытым ключом исполнителя и уходят на сервер только зашифрованными.
type projectIn struct {
	Project        json.RawMessage   `json:"project"`
	Secrets        map[string]string `json:"secrets"`
	RotatePassword bool              `json:"rotate_password"`
	ExpectHash     string            `json:"expect_project_hash"`
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

// serverKey запрашивает у агента открытый ключ исполнителя. При ошибке возвращает ответ агента как есть.
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

// projectCall готовит пакет (шифрует секреты) и отправляет агенту предпросмотр или создание проекта.
func (s *Server) projectCall(action string) func(*http.Request) ([]byte, int) {
	return func(r *http.Request) ([]byte, int) {
		var in projectIn
		if code := readLarge(r, &in); code != 0 {
			return errBody("usage", "неверный запрос"), code
		}
		var head struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(in.Project, &head) != nil || !projectRe.MatchString(head.Name) {
			return errBody("usage", "в описании проекта нужно корректное имя"), 400
		}
		pub, bad := s.serverKey(r)
		if bad != nil {
			return bad, 200
		}
		cts := make(map[string]string, len(in.Secrets))
		for name, val := range in.Secrets {
			ct, err := secrets.Encrypt(pub, head.Name, name, []byte(val), time.Now())
			if err != nil {
				return errBody("invalid_secret", "секрет "+name+": "+err.Error()), 400
			}
			cts[name] = ct
		}
		bundle := map[string]any{"project": in.Project, "ciphertexts": cts, "rotate_password": in.RotatePassword}
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
