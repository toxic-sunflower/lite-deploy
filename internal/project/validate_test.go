package project

import (
	"os"
	"strings"
	"testing"
)

func load(t *testing.T) *Project {
	t.Helper()
	data, err := os.ReadFile("../../testdata/shop/project.json")
	if err != nil {
		t.Fatal(err)
	}
	p, issues := Parse(data)
	if len(issues) > 0 {
		t.Fatalf("Parse: %v", issues)
	}
	return p
}

func TestExampleIsValid(t *testing.T) {
	if issues := Validate(load(t)); len(issues) > 0 {
		t.Fatalf("expected an empty list, got %v", issues)
	}
}

func hasIssue(issues []string, path, code string) bool {
	for _, i := range issues {
		if i == path+"|"+code {
			return true
		}
	}
	return false
}

func codes(p *Project) []string {
	var out []string
	for _, i := range Validate(p) {
		out = append(out, i.Path+"|"+i.Code)
	}
	return out
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *Project)
		path   string
		code   string
	}{
		{"a name with a capital letter", func(p *Project) { p.Name = "Shop" }, "/name", "pattern"},
		{"the name is too short", func(p *Project) { p.Name = "a" }, "/name", "pattern"},
		{"a name of 31 characters is fine, 32 is not", func(p *Project) { p.Name = "a" + strings.Repeat("b", 31) }, "/name", "pattern"},
		{"a reserved name", func(p *Project) { p.Name = "postgres" }, "/name", "reserved"},
		{"systemd prefix", func(p *Project) { p.Name = "systemd-foo" }, "/name", "reserved"},
		{"an empty name", func(p *Project) { p.Name = "" }, "/name", "required"},
		{"a foreign schema", func(p *Project) { p.Schema = "x" }, "/schema", "enum"},
		{"no services", func(p *Project) { p.Services = nil }, "/services", "minProperties"},
		{"a bad service name", func(p *Project) { p.Services["Bad"] = p.Services["api"] }, "/services/Bad", "pattern"},
		{"an image with a tag", func(p *Project) { s := p.Services["api"]; s.Image = "ghcr.io/a/b:latest"; p.Services["api"] = s }, "/services/api/image", "pattern"},
		{"container port 0", func(p *Project) { s := p.Services["api"]; s.ContainerPort = 0; p.Services["api"] = s }, "/services/api/container_port", "required"},
		{"a container without an image", func(p *Project) { s := p.Services["api"]; s.Image = ""; p.Services["api"] = s }, "/services/api/image", "required"},
		{"unknown kind", func(p *Project) { s := p.Services["api"]; s.Kind = "vm"; p.Services["api"] = s }, "/services/api/kind", "enum"},
		{"unit without exec", func(p *Project) { p.Services["api"] = Service{Kind: KindUnit, HostPorts: []int{18081, 18082}} }, "/services/api/exec", "required"},
		{"unit with an image", func(p *Project) {
			p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"/srv/app"}, Image: "a/b", HostPorts: []int{18081, 18082}}
		}, "/services/api", "not_applicable"},
		{"unit without release", func(p *Project) {
			p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"/srv/app"}, HostPorts: []int{18081, 18082}}
		}, "/services/api/release", "required"},
		{"unit with .. in the path", func(p *Project) {
			p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"../app"}, HostPorts: []int{18081, 18082}, Release: &Release{MinisignKey: "RWQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", URL: "https://example.com/a-{sha}.tar.gz"}}
		}, "/services/api/exec/0", "pattern"},
		{"release over http", func(p *Project) {
			p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"app"}, HostPorts: []int{18081, 18082}, Release: &Release{MinisignKey: "RWQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", URL: "http://example.com/a-{sha}.tar.gz"}}
		}, "/services/api/release/url", "pattern"},
		{"release without {sha}", func(p *Project) {
			p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"app"}, HostPorts: []int{18081, 18082}, Release: &Release{MinisignKey: "RWQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", URL: "https://example.com/latest.tar.gz"}}
		}, "/services/api/release/url", "pattern"},
		{"release without a signing key", func(p *Project) {
			p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"app"}, HostPorts: []int{18081, 18082}, Release: &Release{URL: "https://example.com/a-{sha}.tar.gz"}}
		}, "/services/api/release/minisign_key", "required"},
		{"release with a wrong signing key", func(p *Project) {
			p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"app"}, HostPorts: []int{18081, 18082}, Release: &Release{URL: "https://example.com/a-{sha}.tar.gz", MinisignKey: "abc"}}
		}, "/services/api/release/minisign_key", "pattern"},
		{"release on a container", func(p *Project) {
			s := p.Services["api"]
			s.Release = &Release{MinisignKey: "RWQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", URL: "https://example.com/a-{sha}.tar.gz"}
			p.Services["api"] = s
		}, "/services/api/release", "not_applicable"},
		{"a database on another server without a password", func(p *Project) { p.Database.Server = "db1" }, "/database/password_secret", "required"},
		{"a port on a local database", func(p *Project) { p.Database.Port = 5432 }, "/database/port", "not_applicable"},
		{"a password on a local database", func(p *Project) { p.Database.PasswordSecret = "secret://x_pw" }, "/database/password_secret", "not_applicable"},
		{"unknown schema", func(p *Project) { p.Schema = "x" }, "/schema", "enum"},
		{"container port 70000", func(p *Project) { s := p.Services["api"]; s.ContainerPort = 70000; p.Services["api"] = s }, "/services/api/container_port", "maximum"},
		{"host_ports out of range", func(p *Project) { s := p.Services["api"]; s.HostPorts = []int{80, 18082}; p.Services["api"] = s }, "/services/api/host_ports/0", "minimum"},
		{"host_ports with one port", func(p *Project) { s := p.Services["api"]; s.HostPorts = []int{18081}; p.Services["api"] = s }, "/services/api/host_ports", "minItems"},
		{"memory 17g", func(p *Project) {
			s := p.Services["api"]
			s.Limits = &Limits{Memory: "17g", Pids: 256}
			p.Services["api"] = s
		}, "/services/api/limits/memory", "maximum"},
		{"memory without a unit", func(p *Project) { s := p.Services["api"]; s.Limits = &Limits{Memory: "512"}; p.Services["api"] = s }, "/services/api/limits/memory", "pattern"},
		{"pids 8", func(p *Project) { s := p.Services["api"]; s.Limits = &Limits{Pids: 8}; p.Services["api"] = s }, "/services/api/limits/pids", "minimum"},
		{"an env name with a hyphen", func(p *Project) { s := p.Services["api"]; s.Env = map[string]string{"A-B": "x"}; p.Services["api"] = s }, "/services/api/env/A-B", "pattern"},
		{"an env value with a line break", func(p *Project) {
			s := p.Services["api"]
			s.Env = map[string]string{"A": "x\ny"}
			p.Services["api"] = s
		}, "/services/api/env/A", "pattern"},
		{"a secret value is not a reference", func(p *Project) {
			s := p.Services["api"]
			s.Secrets = map[string]string{"A": "plain"}
			p.Services["api"] = s
		}, "/services/api/secrets/A", "pattern"},
		{"an internal secret with _", func(p *Project) {
			s := p.Services["api"]
			s.Secrets = map[string]string{"A": "secret://_db_password"}
			p.Services["api"] = s
		}, "/services/api/secrets/A", "pattern"},
		{"a variable in both env and secrets", func(p *Project) {
			s := p.Services["api"]
			s.Env = map[string]string{"X": "1"}
			s.Secrets = map[string]string{"X": "secret://x_value"}
			p.Services["api"] = s
		}, "/services/api/secrets/X", "duplicate_env"},
		{"database_env without a database", func(p *Project) { p.Database = nil }, "/services/api/database_env", "no_database"},
		{"a site for an unknown service", func(p *Project) { p.Sites[0].Service = "nope" }, "/sites/0/service", "unknown_service"},
		{"a repeated site name", func(p *Project) { p.Sites[1].Name = "main" }, "/sites/1/name", "duplicate"},
		{"a repeated host", func(p *Project) { p.Sites[1].Host = p.Sites[0].Host }, "/sites/1/host", "duplicate"},
		{"a host with capital letters", func(p *Project) { p.Sites[0].Host = "Example.COM" }, "/sites/0/host", "pattern"},
		{"the site port clashes with a slot port", func(p *Project) { p.Sites[0].ListenPort = 18081 }, "/sites/0/listen_port", "duplicate_port"},
		{"publish is unknown", func(p *Project) { p.Sites[0].Publish = "public" }, "/sites/0/publish", "enum"},
		{"max_body 1001MB", func(p *Project) { p.Sites[0].MaxBody = "1001MB" }, "/sites/0/max_body", "maximum"},
		{"max_body without a unit", func(p *Project) { p.Sites[0].MaxBody = "5" }, "/sites/0/max_body", "pattern"},
		{"a path with a curly brace", func(p *Project) { p.Sites[0].Routes[0].Path = "/a{b}" }, "/sites/0/routes/0/path", "pattern"},
		{"a path with a space", func(p *Project) { p.Sites[0].Routes[0].Path = "/a b" }, "/sites/0/routes/0/path", "pattern"},
		{"a socket outside /run", func(p *Project) { s := p.Services["api"]; s.Sockets = []string{"/etc"}; p.Services["api"] = s }, "/services/api/sockets/0", "pattern"},
		{"a socket path with ..", func(p *Project) { s := p.Services["api"]; s.Sockets = []string{"/run/../etc"}; p.Services["api"] = s }, "/services/api/sockets/0", "pattern"},
		{"the key is not ed25519", func(p *Project) { p.DeployKey.PublicKey = "ssh-rsa AAAAB3NzaC1yc2E x" }, "/deploy_key/public_key", "pattern"},
		{"a key with options", func(p *Project) {
			p.DeployKey.PublicKey = `command="x" ` + p.DeployKey.PublicKey
		}, "/deploy_key/public_key", "pattern"},
		{"the engine is not postgres", func(p *Project) { p.Database.Engine = "mysql" }, "/database/engine", "enum"},
		{"connection format", func(p *Project) { p.Database.ConnectionFormat = "x" }, "/database/connection_format", "enum"},
		{"the registry token is not a reference", func(p *Project) { p.Registry.TokenSecret = "abc" }, "/registry/token_secret", "pattern"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := load(t)
			c.mutate(p)
			got := codes(p)
			if !hasIssue(got, c.path, c.code) {
				t.Fatalf("expected %s|%s, got %v", c.path, c.code, got)
			}
		})
	}
}

func TestEightServicesLimit(t *testing.T) {
	p := load(t)
	for i := 0; i < 8; i++ {
		p.Services["s"+string(rune('a'+i))] = p.Services["api"]
	}
	if !hasIssue(codes(p), "/services", "maxProperties") {
		t.Fatalf("no maxProperties: %v", codes(p))
	}
}

func TestParseStrict(t *testing.T) {
	cases := map[string]string{
		"unknown field":                       `{"schema":"lite-deploy/project/v1","evil":1}`,
		"extra data":                          `{} {}`,
		"not JSON":                            `name: x`,
		"an invalid type":                     `{"name": 5}`,
		"an unknown field in a nested object": `{"services":{"a":{"privileged":true}}}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if p, issues := Parse([]byte(in)); p != nil || len(issues) == 0 {
				t.Fatalf("expected an error, got %v %v", p, issues)
			}
		})
	}
	if _, issues := Parse(make([]byte, MaxBytes+1)); len(issues) == 0 || issues[0].Code != "too_large" {
		t.Fatalf("expected too_large: %v", issues)
	}
}

func TestDefaults(t *testing.T) {
	p := load(t)
	p.Database.ConnectionFormat = ""
	s := p.Services["admin"]
	s.Limits, s.Health = nil, nil
	p.Services["admin"] = s
	p.Sites[1].MaxBody = ""
	d := WithDefaults(p)
	if d.Database.ConnectionFormat != "url" {
		t.Error("the connection format is url by default")
	}
	a := d.Services["admin"]
	if a.Limits.Memory != "512m" || a.Limits.Pids != 256 || a.Health.Path != "/health/ready" || a.Health.TimeoutSeconds != 90 || a.Health.VersionField != "version" {
		t.Errorf("defaults: %+v %+v", a.Limits, a.Health)
	}
	if d.Sites[1].MaxBody != "5MB" {
		t.Error("max_body default 5MB")
	}
	if p.Services["admin"].Limits != nil {
		t.Error("WithDefaults must not change the source project")
	}
}

func TestRoleName(t *testing.T) {
	if RoleName("shop") != "shop" {
		t.Fatal("role")
	}
}

func TestRequires(t *testing.T) {
	p := load(t)
	got := strings.Join(Requires(p), ",")
	if got != "caddy,pg,podman" {
		t.Fatalf("Requires = %s", got)
	}
	p.Database = nil
	for n, s := range p.Services {
		s.Kind = KindUnit
		p.Services[n] = s
	}
	if got := strings.Join(Requires(p), ","); got != "caddy" {
		t.Fatalf("without a database and containers: %s", got)
	}
}

func TestV1SchemaStillAccepted(t *testing.T) {
	p := load(t)
	p.Schema = SchemaIDv1
	if issues := Validate(p); len(issues) != 0 {
		t.Fatal(issues)
	}
}

func TestUnitWithReleaseIsValid(t *testing.T) {
	p := load(t)
	p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"./bin/app"}, HostPorts: []int{18081, 18082},
		Release: &Release{MinisignKey: "RWQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", URL: "https://example.com/dl/app-{sha}.tar.gz", TokenSecret: "secret://rel_token"}}
	if issues := Validate(p); len(issues) != 0 {
		t.Fatal(issues)
	}
}

func TestRemoteDatabaseIsValidAndNeedsNoLocalPG(t *testing.T) {
	p := load(t)
	p.Database.Server = "db1"
	p.Database.PasswordSecret = "secret://db_password"
	if issues := Validate(p); len(issues) != 0 {
		t.Fatal(issues)
	}
	if got := strings.Join(Requires(p), ","); got != "caddy,podman" {
		t.Fatalf("Requires = %s", got)
	}
}

func TestDataAndStopValidation(t *testing.T) {
	p := load(t)
	s := p.Services["api"]
	s.Data = []string{"uploads", "Bad", "uploads"}
	p.Services["api"] = s
	got := codes(p)
	if !hasIssue(got, "/services/api/data/1", "pattern") || !hasIssue(got, "/services/api/data/2", "duplicate") {
		t.Fatalf("%v", got)
	}
	s.Data, s.Stop = nil, []string{"x"}
	p.Services["api"] = s
	if !hasIssue(codes(p), "/services/api/stop", "not_applicable") {
		t.Fatal("stop on a container must be rejected")
	}
	p.Services["api"] = Service{Kind: KindUnit, Exec: []string{"/usr/bin/podman", "compose", "up"}, Stop: []string{"/usr/bin/podman", "compose", "down"},
		Data: []string{"uploads"}, HostPorts: []int{18081, 18082}, Release: &Release{MinisignKey: "RWQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", URL: "https://example.com/a-{sha}.tar.gz"}}
	if issues := Validate(p); len(issues) != 0 {
		t.Fatal(issues)
	}
}
