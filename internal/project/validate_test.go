package project

import (
	"os"
	"strings"
	"testing"
)

func load(t *testing.T) *Project {
	t.Helper()
	data, err := os.ReadFile("../../testdata/fanvue-games/project.json")
	if err != nil {
		t.Fatal(err)
	}
	p, issues := Parse(data)
	if len(issues) > 0 {
		t.Fatalf("Parse: %v", issues)
	}
	return p
}

func TestFanvueIsValid(t *testing.T) {
	if issues := Validate(load(t)); len(issues) > 0 {
		t.Fatalf("ожидали пустой список, получили %v", issues)
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
		{"имя с заглавной", func(p *Project) { p.Name = "Shop" }, "/name", "pattern"},
		{"имя слишком короткое", func(p *Project) { p.Name = "a" }, "/name", "pattern"},
		{"имя 31 символ ок, 32 нет", func(p *Project) { p.Name = "a" + strings.Repeat("b", 31) }, "/name", "pattern"},
		{"зарезервированное имя", func(p *Project) { p.Name = "postgres" }, "/name", "reserved"},
		{"systemd-префикс", func(p *Project) { p.Name = "systemd-foo" }, "/name", "reserved"},
		{"пустое имя", func(p *Project) { p.Name = "" }, "/name", "required"},
		{"чужая схема", func(p *Project) { p.Schema = "x" }, "/schema", "enum"},
		{"нет сервисов", func(p *Project) { p.Services = nil }, "/services", "minProperties"},
		{"плохое имя сервиса", func(p *Project) { p.Services["Bad"] = p.Services["api"] }, "/services/Bad", "pattern"},
		{"образ с тегом", func(p *Project) { s := p.Services["api"]; s.Image = "ghcr.io/a/b:latest"; p.Services["api"] = s }, "/services/api/image", "pattern"},
		{"порт контейнера 0", func(p *Project) { s := p.Services["api"]; s.ContainerPort = 0; p.Services["api"] = s }, "/services/api/container_port", "minimum"},
		{"порт контейнера 70000", func(p *Project) { s := p.Services["api"]; s.ContainerPort = 70000; p.Services["api"] = s }, "/services/api/container_port", "maximum"},
		{"host_ports вне диапазона", func(p *Project) { s := p.Services["api"]; s.HostPorts = []int{80, 18082}; p.Services["api"] = s }, "/services/api/host_ports/0", "minimum"},
		{"host_ports один", func(p *Project) { s := p.Services["api"]; s.HostPorts = []int{18081}; p.Services["api"] = s }, "/services/api/host_ports", "minItems"},
		{"память 17g", func(p *Project) {
			s := p.Services["api"]
			s.Limits = &Limits{Memory: "17g", Pids: 256}
			p.Services["api"] = s
		}, "/services/api/limits/memory", "maximum"},
		{"память без единицы", func(p *Project) { s := p.Services["api"]; s.Limits = &Limits{Memory: "512"}; p.Services["api"] = s }, "/services/api/limits/memory", "pattern"},
		{"pids 8", func(p *Project) { s := p.Services["api"]; s.Limits = &Limits{Pids: 8}; p.Services["api"] = s }, "/services/api/limits/pids", "minimum"},
		{"имя env с дефисом", func(p *Project) { s := p.Services["api"]; s.Env = map[string]string{"A-B": "x"}; p.Services["api"] = s }, "/services/api/env/A-B", "pattern"},
		{"значение env с переводом строки", func(p *Project) {
			s := p.Services["api"]
			s.Env = map[string]string{"A": "x\ny"}
			p.Services["api"] = s
		}, "/services/api/env/A", "pattern"},
		{"значение секрета не ссылка", func(p *Project) {
			s := p.Services["api"]
			s.Secrets = map[string]string{"A": "plain"}
			p.Services["api"] = s
		}, "/services/api/secrets/A", "pattern"},
		{"служебный секрет с _", func(p *Project) {
			s := p.Services["api"]
			s.Secrets = map[string]string{"A": "secret://_db_password"}
			p.Services["api"] = s
		}, "/services/api/secrets/A", "pattern"},
		{"переменная и в env и в secrets", func(p *Project) {
			s := p.Services["api"]
			s.Env = map[string]string{"X": "1"}
			s.Secrets = map[string]string{"X": "secret://x_value"}
			p.Services["api"] = s
		}, "/services/api/secrets/X", "duplicate_env"},
		{"database_env без базы", func(p *Project) { p.Database = nil }, "/services/api/database_env", "no_database"},
		{"сайт на неизвестный сервис", func(p *Project) { p.Sites[0].Service = "nope" }, "/sites/0/service", "unknown_service"},
		{"повтор имени сайта", func(p *Project) { p.Sites[1].Name = "main" }, "/sites/1/name", "duplicate"},
		{"повтор хоста", func(p *Project) { p.Sites[1].Host = p.Sites[0].Host }, "/sites/1/host", "duplicate"},
		{"хост с заглавными", func(p *Project) { p.Sites[0].Host = "Example.COM" }, "/sites/0/host", "pattern"},
		{"порт сайта совпал с портом слота", func(p *Project) { p.Sites[0].ListenPort = 18081 }, "/sites/0/listen_port", "duplicate_port"},
		{"publish не tunnel", func(p *Project) { p.Sites[0].Publish = "direct" }, "/sites/0/publish", "enum"},
		{"max_body 1001MB", func(p *Project) { p.Sites[0].MaxBody = "1001MB" }, "/sites/0/max_body", "maximum"},
		{"max_body без единицы", func(p *Project) { p.Sites[0].MaxBody = "5" }, "/sites/0/max_body", "pattern"},
		{"путь с фигурной скобкой", func(p *Project) { p.Sites[0].Routes[0].Path = "/a{b}" }, "/sites/0/routes/0/path", "pattern"},
		{"путь с пробелом", func(p *Project) { p.Sites[0].Routes[0].Path = "/a b" }, "/sites/0/routes/0/path", "pattern"},
		{"ключ не ed25519", func(p *Project) { p.DeployKey.PublicKey = "ssh-rsa AAAAB3NzaC1yc2E x" }, "/deploy_key/public_key", "pattern"},
		{"ключ с опциями", func(p *Project) {
			p.DeployKey.PublicKey = `command="x" ` + p.DeployKey.PublicKey
		}, "/deploy_key/public_key", "pattern"},
		{"движок не postgres", func(p *Project) { p.Database.Engine = "mysql" }, "/database/engine", "enum"},
		{"формат подключения", func(p *Project) { p.Database.ConnectionFormat = "x" }, "/database/connection_format", "enum"},
		{"токен реестра не ссылка", func(p *Project) { p.Registry.TokenSecret = "abc" }, "/registry/token_secret", "pattern"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := load(t)
			c.mutate(p)
			got := codes(p)
			if !hasIssue(got, c.path, c.code) {
				t.Fatalf("ожидали %s|%s, получили %v", c.path, c.code, got)
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
		t.Fatalf("нет maxProperties: %v", codes(p))
	}
}

func TestParseStrict(t *testing.T) {
	cases := map[string]string{
		"неизвестное поле":         `{"schema":"lite-deploy/project/v1","evil":1}`,
		"лишние данные":            `{} {}`,
		"не JSON":                  `name: x`,
		"неверный тип":             `{"name": 5}`,
		"неизвестное во вложенном": `{"services":{"a":{"privileged":true}}}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if p, issues := Parse([]byte(in)); p != nil || len(issues) == 0 {
				t.Fatalf("ожидали ошибку, получили %v %v", p, issues)
			}
		})
	}
	if _, issues := Parse(make([]byte, MaxBytes+1)); len(issues) == 0 || issues[0].Code != "too_large" {
		t.Fatalf("ожидали too_large: %v", issues)
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
		t.Error("формат подключения по умолчанию url")
	}
	a := d.Services["admin"]
	if a.Limits.Memory != "512m" || a.Limits.Pids != 256 || a.Health.Path != "/health/ready" || a.Health.TimeoutSeconds != 90 || a.Health.VersionField != "version" {
		t.Errorf("значения по умолчанию: %+v %+v", a.Limits, a.Health)
	}
	if d.Sites[1].MaxBody != "5MB" {
		t.Error("max_body по умолчанию 5MB")
	}
	if p.Services["admin"].Limits != nil {
		t.Error("WithDefaults не должна менять исходный проект")
	}
}

func TestRoleName(t *testing.T) {
	if RoleName("fanvue-games") != "fanvue_games" {
		t.Fatal("роль")
	}
}
