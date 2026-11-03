package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// MaxBytes предел размера файла описания (SPEC-v0.md, раздел 8).
const MaxBytes = 64 * 1024

var reservedNames = map[string]bool{
	"root": true, "postgres": true, "caddy": true, "cloudflared": true,
	"lite-deploy": true, "www-data": true, "daemon": true,
}

// Reserved сообщает, что имя нельзя использовать как имя проекта.
func Reserved(name string) bool {
	return reservedNames[name] || strings.HasPrefix(name, "systemd-")
}

// Parse разбирает JSON строго: неизвестные поля и лишние данные отклоняются (D-33).
// Значения по умолчанию не подставляются, см. WithDefaults.
func Parse(data []byte) (*Project, []schema.Issue) {
	if len(data) > MaxBytes {
		return nil, []schema.Issue{{Path: "/", Code: "too_large", Message: fmt.Sprintf("файл больше %d байт", MaxBytes)}}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Project
	if err := dec.Decode(&p); err != nil {
		code := "syntax"
		if strings.Contains(err.Error(), "unknown field") {
			code = "unknown_field"
		} else if strings.Contains(err.Error(), "cannot unmarshal") {
			code = "type"
		}
		return nil, []schema.Issue{{Path: "/", Code: code, Message: err.Error()}}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, []schema.Issue{{Path: "/", Code: "syntax", Message: "после описания проекта есть лишние данные"}}
	}
	return &p, nil
}

// Validate проверяет описание: схема по тегам плюс связи между полями (SPEC-v0.md, 2.2).
func Validate(p *Project) []schema.Issue {
	issues := schema.Check(p)
	add := func(path, code, format string, args ...any) {
		issues = append(issues, schema.Issue{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
	}
	if p.Name != "" && Reserved(p.Name) {
		add("/name", "reserved", "имя %q зарезервировано", p.Name)
	}

	used := map[int]string{}
	usePort := func(port int, path string) {
		if port == 0 {
			return
		}
		if prev, ok := used[port]; ok {
			add(path, "duplicate_port", "порт %d уже занят в %s", port, prev)
			return
		}
		used[port] = path
	}

	svcNames := make([]string, 0, len(p.Services))
	for n := range p.Services {
		svcNames = append(svcNames, n)
	}
	sort.Strings(svcNames)
	for _, n := range svcNames {
		s := p.Services[n]
		base := "/services/" + n
		for i, hp := range s.HostPorts {
			usePort(hp, base+"/host_ports/"+strconv.Itoa(i))
		}
		if s.DatabaseEnv != "" && p.Database == nil {
			add(base+"/database_env", "no_database", "database_env указан, но в проекте нет database")
		}
		if s.Limits != nil && s.Limits.Memory != "" {
			if mb, ok := MemoryMB(s.Limits.Memory); !ok || mb > 16*1024 {
				add(base+"/limits/memory", "maximum", "память не больше 16g")
			}
		}
		for env := range s.Env {
			if _, dup := s.Secrets[env]; dup {
				add(base+"/secrets/"+env, "duplicate_env", "переменная %s задана и в env, и в secrets", env)
			}
		}
		if s.DatabaseEnv != "" {
			_, inEnv := s.Env[s.DatabaseEnv]
			_, inSec := s.Secrets[s.DatabaseEnv]
			if inEnv || inSec {
				add(base+"/database_env", "duplicate_env", "переменная %s задана ещё и в env или secrets", s.DatabaseEnv)
			}
		}
	}

	siteNames := map[string]bool{}
	hosts := map[string]bool{}
	for i, s := range p.Sites {
		base := "/sites/" + strconv.Itoa(i)
		if siteNames[s.Name] {
			add(base+"/name", "duplicate", "имя сайта %q повторяется", s.Name)
		}
		siteNames[s.Name] = true
		if hosts[s.Host] {
			add(base+"/host", "duplicate", "хост %q повторяется", s.Host)
		}
		hosts[s.Host] = true
		if s.Service != "" {
			if _, ok := p.Services[s.Service]; !ok {
				add(base+"/service", "unknown_service", "сервис %q не описан в services", s.Service)
			}
		}
		usePort(s.ListenPort, base+"/listen_port")
		if s.MaxBody != "" && !sizeWithin(s.MaxBody) {
			add(base+"/max_body", "maximum", "размер не больше 1000MB")
		}
		for j, r := range s.Routes {
			if r.MaxBody != "" && !sizeWithin(r.MaxBody) {
				add(base+"/routes/"+strconv.Itoa(j)+"/max_body", "maximum", "размер не больше 1000MB")
			}
		}
	}
	return issues
}

// MemoryMB переводит «768m» или «2g» в мегабайты.
func MemoryMB(s string) (int, bool) {
	if len(s) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, false
	}
	switch s[len(s)-1] {
	case 'm':
		return n, true
	case 'g':
		return n * 1024, true
	}
	return 0, false
}

func sizeWithin(s string) bool {
	if len(s) < 3 {
		return false
	}
	n, err := strconv.Atoi(s[:len(s)-2])
	if err != nil {
		return false
	}
	if s[len(s)-2:] == "KB" {
		return n <= 1000*1024
	}
	return n <= 1000
}

// RoleName имя роли и базы PostgreSQL: имя проекта с «-» → «_».
func RoleName(project string) string { return strings.ReplaceAll(project, "-", "_") }

// WithDefaults возвращает копию с подставленными значениями по умолчанию, кроме портов (их выбирает план).
func WithDefaults(p *Project) *Project {
	c := *p
	if c.Database != nil {
		d := *c.Database
		if d.ConnectionFormat == "" {
			d.ConnectionFormat = "url"
		}
		c.Database = &d
	}
	c.Services = make(map[string]Service, len(p.Services))
	for n, s := range p.Services {
		h := Health{Path: "/health/ready", VersionField: "version", TimeoutSeconds: 90}
		if s.Health != nil {
			if s.Health.Path != "" {
				h.Path = s.Health.Path
			}
			if s.Health.VersionField != "" {
				h.VersionField = s.Health.VersionField
			}
			if s.Health.TimeoutSeconds != 0 {
				h.TimeoutSeconds = s.Health.TimeoutSeconds
			}
		}
		l := Limits{Memory: "512m", Pids: 256}
		if s.Limits != nil {
			if s.Limits.Memory != "" {
				l.Memory = s.Limits.Memory
			}
			if s.Limits.Pids != 0 {
				l.Pids = s.Limits.Pids
			}
		}
		s.Health, s.Limits = &h, &l
		c.Services[n] = s
	}
	c.Sites = make([]Site, len(p.Sites))
	for i, s := range p.Sites {
		if s.Publish == "" {
			s.Publish = "tunnel"
		}
		if s.TrustedProxy == "" {
			s.TrustedProxy = "none"
		}
		if s.MaxBody == "" {
			s.MaxBody = "5MB"
		}
		c.Sites[i] = s
	}
	return &c
}
