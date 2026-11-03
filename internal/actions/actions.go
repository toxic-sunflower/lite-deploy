// Package actions задаёт фиксированный список действий исполнителя и проверку их параметров
// (SPEC-v0.md, раздел 4). Исполнитель принимает только эти действия и только типизированные параметры (D-42).
package actions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// Классы действий (D-29).
const (
	ClassSafe      = "safe"
	ClassNormal    = "normal"
	ClassDangerous = "dangerous"
)

// Def описывает одно действие.
type Def struct {
	Name  string
	Class string
	New   func() any
	// Extra проверяет связи между полями сверх тегов; может быть nil.
	Extra func(v any) []schema.Issue
}

var registry = map[string]Def{}

func register(d Def) { registry[d.Name] = d }

// Lookup ищет действие по имени.
func Lookup(name string) (Def, bool) {
	d, ok := registry[name]
	return d, ok
}

// Names перечисляет все действия по алфавиту.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Decode строго разбирает параметры шага и проверяет их. Неизвестное действие даёт ошибку unknown_action.
func Decode(name string, raw json.RawMessage) (any, []schema.Issue) {
	d, ok := registry[name]
	if !ok {
		return nil, []schema.Issue{{Path: "/action", Code: "unknown_action", Message: fmt.Sprintf("действие %q не входит в список исполнителя", name)}}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	v := d.New()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return nil, []schema.Issue{{Path: "/params", Code: "syntax", Message: err.Error()}}
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, []schema.Issue{{Path: "/params", Code: "syntax", Message: "лишние данные после параметров"}}
	}
	issues := schema.Check(v)
	if d.Extra != nil {
		issues = append(issues, d.Extra(v)...)
	}
	for i := range issues {
		issues[i].Path = "/params" + issues[i].Path
	}
	return v, issues
}

// Имена действий.
const (
	VerifyHostName        = "verify_host"
	CreateProjectUserName = "create_project_user"
	ProvisionPostgresName = "provision_postgres"
	SetSecretName         = "set_secret"
	RegistryLoginName     = "registry_login"
	WriteServiceEnvName   = "write_service_env"
	InstallQuadletName    = "install_quadlet"
	ReloadUserUnitsName   = "reload_user_units"
	InstallCaddySiteName  = "install_caddy_site"
	InstallReloadRuleName = "install_reload_rule"
	ReloadCaddyName       = "reload_caddy"
	ProbeSiteName         = "probe_site"
	InstallDeployKeyName  = "install_deploy_key"
	RecordProjectName     = "record_project"
)

// VerifyHost только читает: проверяет предусловия сервера.
type VerifyHost struct{}

// CreateProjectUser создаёт системного пользователя проекта.
type CreateProjectUser struct {
	Project string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
}

// ProvisionPostgres создаёт роль и базу проекта.
type ProvisionPostgres struct {
	Project          string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	ConnectionFormat string `json:"connection_format" enum:"npgsql,url,libpq"`
	RotatePassword   bool   `json:"rotate_password,omitempty"`
}

// SetSecret записывает значение секрета (шифртекст) в хранилище.
type SetSecret struct {
	Project    string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Name       string `json:"name" pattern:"^_?[a-z][a-z0-9_]{1,63}$"`
	Ciphertext string `json:"ciphertext" pattern:"^-----BEGIN AGE ENCRYPTED FILE-----\\n[A-Za-z0-9+/=\\n]{20,}-----END AGE ENCRYPTED FILE-----$" maxLen:"16384" doc:"шифртекст age (armor)"`
}

// RegistryLogin входит в реестр образов от имени проектного пользователя.
type RegistryLogin struct {
	Project  string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Registry string `json:"registry" pattern:"^[a-z0-9.-]+(:[0-9]{2,5})?$" maxLen:"255"`
	Username string `json:"username" pattern:"^[A-Za-z0-9][A-Za-z0-9-]{0,38}$"`
	Secret   string `json:"secret" pattern:"^[a-z][a-z0-9_]{1,63}$"`
}

// EnvVar одна строка env-файла сервиса.
type EnvVar struct {
	Name   string `json:"name" pattern:"^[A-Za-z_][A-Za-z0-9_]*$"`
	Kind   string `json:"kind" enum:"literal,secret,database"`
	Value  string `json:"value,omitempty" pattern:"^[^\\r\\n]*$" maxLen:"1024" doc:"значение для literal; формат строки подключения для database"`
	Secret string `json:"secret,omitempty" pattern:"^[a-z][a-z0-9_]{1,63}$" doc:"имя секрета для secret"`
}

// WriteServiceEnv собирает env-файл сервиса.
type WriteServiceEnv struct {
	Project string   `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Service string   `json:"service" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Env     []EnvVar `json:"env" maxItems:"300"`
}

// InstallQuadlet пишет юнит Quadlet одного слота сервиса.
type InstallQuadlet struct {
	Project        string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Service        string `json:"service" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Slot           string `json:"slot" enum:"blue,green"`
	HostPort       int    `json:"host_port" min:"18000" max:"18999"`
	ContainerPort  int    `json:"container_port" min:"1" max:"65535"`
	Memory         string `json:"memory" pattern:"^[1-9][0-9]*[mg]$"`
	Pids           int    `json:"pids" min:"16" max:"4096"`
	DatabaseSocket bool   `json:"database_socket"`
}

// ReloadUserUnits перечитывает юниты в сессии проектного пользователя.
type ReloadUserUnits struct {
	Project string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
}

// SiteRoute особый лимит тела для пути.
type SiteRoute struct {
	Path    string `json:"path" pattern:"^/[^\\s{}]*$" maxLen:"256"`
	MaxBody string `json:"max_body" pattern:"^[1-9][0-9]*(KB|MB)$"`
}

// InstallCaddySite пишет файл сайта Caddy и начальный upstream.
type InstallCaddySite struct {
	Project         string      `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Site            string      `json:"site" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Host            string      `json:"host" pattern:"^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$" maxLen:"253"`
	ListenPort      int         `json:"listen_port" min:"18000" max:"18999"`
	UpstreamInitial string      `json:"upstream_initial" enum:"not_deployed"`
	TrustedProxy    string      `json:"trusted_proxy" enum:"cloudflare,none"`
	MaxBody         string      `json:"max_body" pattern:"^[1-9][0-9]*(KB|MB)$"`
	Routes          []SiteRoute `json:"routes,omitempty" maxItems:"16"`
}

// InstallReloadRule ставит правило sudoers на reload Caddy.
type InstallReloadRule struct {
	Project string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
}

// ReloadCaddy проверяет конфигурацию Caddy и перезагружает его.
type ReloadCaddy struct{}

// ProbeSite проверяет, что Caddy отвечает на имя сайта.
type ProbeSite struct {
	Host       string `json:"host" pattern:"^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$" maxLen:"253"`
	ListenPort int    `json:"listen_port" min:"18000" max:"18999"`
	Expect     string `json:"expect" enum:"not_deployed,deployed"`
}

// InstallDeployKey ставит ключ CI с привратником.
type InstallDeployKey struct {
	Project   string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	PublicKey string `json:"public_key" pattern:"^ssh-ed25519 AAAAC3NzaC1lZDI1NTE5[A-Za-z0-9+/]{48}( [^\\r\\n]{1,100})?$"`
}

// Ports выбранные порты проекта.
type Ports struct {
	Services map[string][]int `json:"services" keypattern:"^[a-z][a-z0-9-]{0,15}$" maxProps:"8" min:"18000" max:"18999" minItems:"2" maxItems:"2"`
	Sites    map[string]int   `json:"sites" keypattern:"^[a-z][a-z0-9-]{0,15}$" maxProps:"8" min:"18000" max:"18999"`
}

// RecordProject записывает метку управления и состояние.
type RecordProject struct {
	Project     string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	ProjectHash string `json:"project_hash" pattern:"^sha256:[0-9a-f]{64}$"`
	Ports       Ports  `json:"ports"`
}

func init() {
	register(Def{Name: VerifyHostName, Class: ClassNormal, New: func() any { return &VerifyHost{} }})
	register(Def{Name: CreateProjectUserName, Class: ClassNormal, New: func() any { return &CreateProjectUser{} }, Extra: reservedProject})
	register(Def{Name: ProvisionPostgresName, Class: ClassNormal, New: func() any { return &ProvisionPostgres{} }})
	register(Def{Name: SetSecretName, Class: ClassNormal, New: func() any { return &SetSecret{} }})
	register(Def{Name: RegistryLoginName, Class: ClassNormal, New: func() any { return &RegistryLogin{} }})
	register(Def{Name: WriteServiceEnvName, Class: ClassNormal, New: func() any { return &WriteServiceEnv{} }, Extra: checkEnv})
	register(Def{Name: InstallQuadletName, Class: ClassNormal, New: func() any { return &InstallQuadlet{} }})
	register(Def{Name: ReloadUserUnitsName, Class: ClassNormal, New: func() any { return &ReloadUserUnits{} }})
	register(Def{Name: InstallCaddySiteName, Class: ClassNormal, New: func() any { return &InstallCaddySite{} }})
	register(Def{Name: InstallReloadRuleName, Class: ClassNormal, New: func() any { return &InstallReloadRule{} }})
	register(Def{Name: ReloadCaddyName, Class: ClassNormal, New: func() any { return &ReloadCaddy{} }})
	register(Def{Name: ProbeSiteName, Class: ClassNormal, New: func() any { return &ProbeSite{} }})
	register(Def{Name: InstallDeployKeyName, Class: ClassNormal, New: func() any { return &InstallDeployKey{} }})
	register(Def{Name: RecordProjectName, Class: ClassNormal, New: func() any { return &RecordProject{} }})
}
