package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// MaxBytes is the size limit of a description file (SPEC-v0.md, section 8).
const MaxBytes = 64 * 1024

var dataNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)

var reservedNames = map[string]bool{
	"root": true, "postgres": true, "caddy": true, "cloudflared": true,
	"lite-deploy": true, "www-data": true, "daemon": true, "shared": true,
}

// Reserved reports that a name cannot be used as a project name.
func Reserved(name string) bool {
	return reservedNames[name] || strings.HasPrefix(name, "systemd-")
}

// Parse parses JSON strictly: unknown fields and extra data are rejected (D-33).
// Defaults are not filled in, see WithDefaults.
func Parse(data []byte) (*Project, []schema.Issue) {
	if len(data) > MaxBytes {
		return nil, []schema.Issue{{Path: "/", Code: "too_large", Message: fmt.Sprintf("the file is larger than %d bytes", MaxBytes)}}
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
		return nil, []schema.Issue{{Path: "/", Code: "syntax", Message: "extra data after the project description"}}
	}
	return &p, nil
}

// Validate validates the description: the schema by tags plus relations between fields (SPEC-v0.md, 2.2).
func Validate(p *Project) []schema.Issue {
	issues := schema.Check(p)
	add := func(path, code, format string, args ...any) {
		issues = append(issues, schema.Issue{Path: path, Code: code, Message: fmt.Sprintf(format, args...)})
	}
	if p.Schema != "" && p.Schema != SchemaID && p.Schema != SchemaIDv1 {
		add("/schema", "enum", "unknown schema version %q", p.Schema)
	}
	if d := p.Database; d != nil {
		if d.IsLocal() {
			if d.Port != 0 {
				add("/database/port", "not_applicable", "port is only for a database on another server")
			}
			if d.PasswordSecret != "" {
				add("/database/password_secret", "not_applicable", "for a database on this server the password is generated automatically")
			}
		} else if d.PasswordSecret == "" {
			add("/database/password_secret", "required", "a database on another server needs password_secret")
		}
	}
	if p.Name != "" && Reserved(p.Name) {
		add("/name", "reserved", "the name %q is reserved", p.Name)
	}

	used := map[int]string{}
	usePort := func(port int, path string) {
		if port == 0 {
			return
		}
		if prev, ok := used[port]; ok {
			add(path, "duplicate_port", "port %d is already used in %s", port, prev)
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
		switch s.KindOf() {
		case KindQuadlet:
			if s.Image == "" {
				add(base+"/image", "required", "a container needs image")
			}
			if s.ContainerPort == 0 {
				add(base+"/container_port", "required", "a container needs container_port")
			}
			if len(s.Exec) > 0 {
				add(base+"/exec", "not_applicable", "exec is only allowed for kind unit")
			}
			if len(s.Stop) > 0 {
				add(base+"/stop", "not_applicable", "stop is only allowed for kind unit")
			}
			if s.Release != nil {
				add(base+"/release", "not_applicable", "release is only allowed for kind unit")
			}
		case KindUnit:
			if len(s.Exec) == 0 {
				add(base+"/exec", "required", "kind unit needs a start command exec")
			}
			for i, a := range s.Exec {
				if a == "" || strings.ContainsAny(a, "\r\n\x00") || len(a) > 512 {
					add(base+"/exec/"+strconv.Itoa(i), "pattern", "an argument must be non-empty, without line breaks, up to 512 characters")
				}
			}
			if s.Image != "" || s.ContainerPort != 0 {
				add(base, "not_applicable", "image and container_port belong to containers; kind unit has neither")
			}
			for i, a := range s.Stop {
				if a == "" || strings.ContainsAny(a, "\r\n\x00") || len(a) > 512 {
					add(base+"/stop/"+strconv.Itoa(i), "pattern", "an argument must be non-empty, without line breaks, up to 512 characters")
				}
			}
			if s.Release == nil {
				add(base+"/release", "required", "kind unit needs a release (where to get the archive)")
			}
			if len(s.Exec) > 0 {
				for _, part := range strings.Split(s.Exec[0], "/") {
					if part == ".." {
						add(base+"/exec/0", "pattern", "the program path must not contain \"..\"")
					}
				}
			}
		}
		seenData := map[string]bool{}
		for i, d := range s.Data {
			if !dataNameRe.MatchString(d) {
				add(base+"/data/"+strconv.Itoa(i), "pattern", "directory name: lowercase Latin letters, digits and \"-\", up to 16 characters")
			}
			if seenData[d] {
				add(base+"/data/"+strconv.Itoa(i), "duplicate", "directory %q is repeated", d)
			}
			seenData[d] = true
		}
		for i, hp := range s.HostPorts {
			usePort(hp, base+"/host_ports/"+strconv.Itoa(i))
		}
		if s.DatabaseEnv != "" && p.Database == nil {
			add(base+"/database_env", "no_database", "database_env is set but the project has no database")
		}
		if s.Limits != nil && s.Limits.Memory != "" {
			if mb, ok := MemoryMB(s.Limits.Memory); !ok || mb > 16*1024 {
				add(base+"/limits/memory", "maximum", "memory at most 16g")
			}
		}
		for env := range s.Env {
			if _, dup := s.Secrets[env]; dup {
				add(base+"/secrets/"+env, "duplicate_env", "variable %s is set in both env and secrets", env)
			}
		}
		if s.DatabaseEnv != "" {
			_, inEnv := s.Env[s.DatabaseEnv]
			_, inSec := s.Secrets[s.DatabaseEnv]
			if inEnv || inSec {
				add(base+"/database_env", "duplicate_env", "variable %s is also set in env or secrets", s.DatabaseEnv)
			}
		}
	}

	siteNames := map[string]bool{}
	hosts := map[string]bool{}
	for i, s := range p.Sites {
		base := "/sites/" + strconv.Itoa(i)
		if siteNames[s.Name] {
			add(base+"/name", "duplicate", "the site name %q is repeated", s.Name)
		}
		siteNames[s.Name] = true
		if hosts[s.Host] {
			add(base+"/host", "duplicate", "host %q is repeated", s.Host)
		}
		hosts[s.Host] = true
		if s.Service != "" {
			if _, ok := p.Services[s.Service]; !ok {
				add(base+"/service", "unknown_service", "service %q is not described in services", s.Service)
			}
		}
		usePort(s.ListenPort, base+"/listen_port")
		if s.MaxBody != "" && !sizeWithin(s.MaxBody) {
			add(base+"/max_body", "maximum", "size at most 1000MB")
		}
		for j, r := range s.Routes {
			if r.MaxBody != "" && !sizeWithin(r.MaxBody) {
				add(base+"/routes/"+strconv.Itoa(j)+"/max_body", "maximum", "size at most 1000MB")
			}
		}
	}
	return issues
}

// MemoryMB converts "768m" or "2g" to megabytes.
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

// RoleName is the PostgreSQL role and database name: the project name with "-" → "_".
func RoleName(project string) string { return strings.ReplaceAll(project, "-", "_") }

// WithDefaults returns a copy with the defaults filled in, except ports (the plan chooses those).
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
