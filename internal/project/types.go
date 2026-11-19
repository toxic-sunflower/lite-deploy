// Package project описывает проект (project.json) и проверяет его (SPEC-v0.md, раздел 2).
package project

import "sort"

// SchemaID идентификатор версии схемы описания проекта.
const SchemaID = "lite-deploy/project/v2"

// SchemaIDv1 прежняя версия схемы: описания v1 принимаются как есть (все сервисы это контейнеры Quadlet на этом сервере).
const SchemaIDv1 = "lite-deploy/project/v1"

const (
	// PortMin и PortMax диапазон портов по умолчанию (SPEC-v0.md, раздел 8).
	PortMin = 18000
	PortMax = 18999
)

// Project единственный вход для `project create`.
type Project struct {
	Schema      string             `json:"schema" enum:"lite-deploy/project/v1,lite-deploy/project/v2" doc:"schema version"`
	Name        string             `json:"name" pattern:"^[a-z][a-z0-9-]{1,30}$" doc:"project name; the same as the system user name"`
	Description string             `json:"description,omitempty" maxLen:"200"`
	Adopt       bool               `json:"adopt,omitempty" doc:"take over a project that was set up by hand"`
	Database    *Database          `json:"database,omitempty"`
	Registry    *Registry          `json:"registry,omitempty"`
	Services    map[string]Service `json:"services" keypattern:"^[a-z][a-z0-9-]{0,15}$" minProps:"1" maxProps:"8"`
	Sites       []Site             `json:"sites" minItems:"1" maxItems:"8"`
	DeployKey   *DeployKey         `json:"deploy_key,omitempty"`
}

// Database база проекта на PostgreSQL хоста.
type Database struct {
	Engine           string `json:"engine" enum:"postgres"`
	Server           string `json:"server,omitempty" pattern:"^[a-z][a-z0-9-]{0,30}$" doc:"server from the registry (lite server add) where PostgreSQL runs; local by default"`
	Port             int    `json:"port,omitempty" min:"1" max:"65535" doc:"PostgreSQL port on another server; 5432 by default"`
	PasswordSecret   string `json:"password_secret,omitempty" pattern:"^secret://[a-z][a-z0-9_]{1,63}$" doc:"for a database on another server: the secret with the role password (the role and database are named after the project, hyphens replaced by _)"`
	ConnectionFormat string `json:"connection_format,omitempty" enum:"npgsql,url,libpq" doc:"connection string format, url by default"`
}

// Registry вход в реестр образов.
type Registry struct {
	Host        string `json:"host" pattern:"^[a-z0-9.-]+(:[0-9]{2,5})?$" maxLen:"255"`
	Username    string `json:"username" pattern:"^[A-Za-z0-9][A-Za-z0-9-]{0,38}$"`
	TokenSecret string `json:"token_secret" pattern:"^secret://[a-z][a-z0-9_]{1,63}$" doc:"secret://name reference; a read-only token"`
}

// Виды компонента.
const (
	KindQuadlet = "quadlet" // контейнер podman под systemd (по умолчанию)
	KindUnit    = "unit"    // процесс на хосте под systemd: бинарник, Node, .NET и т.п.
)

// ServerLocal сервер, на котором работает lite; остальные берутся из реестра серверов (R-26).
const ServerLocal = "local"

// Service компонент проекта: контейнер или процесс на хосте (поле kind).
type Service struct {
	Kind          string            `json:"kind,omitempty" enum:"quadlet,unit" doc:"how it runs: quadlet (container, the default) or unit (a process on the host)"`
	Release       *Release          `json:"release,omitempty" doc:"for unit: where the release comes from"`
	Exec          []string          `json:"exec,omitempty" minItems:"1" maxItems:"32" doc:"for unit: start command (program path and arguments); the port is passed in the PORT variable"`
	Stop          []string          `json:"stop,omitempty" minItems:"1" maxItems:"32" doc:"for unit: stop command (ExecStop), for example podman compose down"`
	Data          []string          `json:"data,omitempty" maxItems:"8" doc:"persistent directories of the component (shared by the blue and green slots, they survive deploys): a unit sees them in LITE_DATA_<NAME> variables, a container in /data/<name>"`
	Image         string            `json:"image,omitempty" pattern:"^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+$" maxLen:"255" doc:"image without a tag"`
	ContainerPort int               `json:"container_port,omitempty" min:"1" max:"65535" doc:"for quadlet: the application port inside the container"`
	HostPorts     []int             `json:"host_ports,omitempty" min:"18000" max:"18999" minItems:"2" maxItems:"2" doc:"ports of the blue and green slots"`
	Health        *Health           `json:"health,omitempty"`
	Limits        *Limits           `json:"limits,omitempty"`
	Env           map[string]string `json:"env,omitempty" keypattern:"^[A-Za-z_][A-Za-z0-9_]*$" valpattern:"^[^\\r\\n]*$" valMaxLen:"1024" maxProps:"128"`
	Secrets       map[string]string `json:"secrets,omitempty" keypattern:"^[A-Za-z_][A-Za-z0-9_]*$" valpattern:"^secret://[a-z][a-z0-9_]{1,63}$" maxProps:"128"`
	DatabaseEnv   string            `json:"database_env,omitempty" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" doc:"variable holding the database connection string"`
}

// Release откуда приезжает релиз компонента kind unit: архив tar.gz, распаковывается в каталог слота.
type Release struct {
	URL         string `json:"url" pattern:"^https://[A-Za-z0-9.-]+(:[0-9]{2,5})?/[^\\s{}]*\\{sha\\}[^\\s{}]*$" maxLen:"512" doc:"URL of the tar.gz archive; {sha} is replaced with the hash of the version being deployed"`
	TokenSecret string `json:"token_secret,omitempty" pattern:"^secret://[a-z][a-z0-9_]{1,63}$" doc:"secret://name reference; the token is sent as Authorization: Bearer"`
	MinisignKey string `json:"minisign_key" pattern:"^RW[A-Za-z0-9+/]{54}$" doc:"minisign public key that CI signs the archive with; the signature sits next to it: the archive URL plus .minisig"`
}

// Health проверка готовности.
type Health struct {
	Path           string `json:"path,omitempty" pattern:"^/[^\\s]*$" maxLen:"256"`
	VersionField   string `json:"version_field,omitempty" pattern:"^[A-Za-z_][A-Za-z0-9_]*$"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" min:"5" max:"600"`
}

// Limits ограничения контейнера.
type Limits struct {
	Memory string `json:"memory,omitempty" pattern:"^[1-9][0-9]*[mg]$"`
	Pids   int    `json:"pids,omitempty" min:"16" max:"4096"`
}

// Site сайт, который принимает трафик на 127.0.0.1.
type Site struct {
	Name         string  `json:"name" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Host         string  `json:"host" pattern:"^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$" maxLen:"253"`
	Service      string  `json:"service" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	ListenPort   int     `json:"listen_port,omitempty" min:"18000" max:"18999" doc:"Caddy port on 127.0.0.1; if omitted, a free one is chosen"`
	Publish      string  `json:"publish,omitempty" enum:"tunnel"`
	TrustedProxy string  `json:"trusted_proxy,omitempty" enum:"cloudflare,none"`
	MaxBody      string  `json:"max_body,omitempty" pattern:"^[1-9][0-9]*(KB|MB)$"`
	Routes       []Route `json:"routes,omitempty" maxItems:"16"`
}

// Route особый лимит тела запроса для пути.
type Route struct {
	Path    string `json:"path" pattern:"^/[^\\s{}]*$" maxLen:"256"`
	MaxBody string `json:"max_body" pattern:"^[1-9][0-9]*(KB|MB)$"`
}

// DeployKey ключ CI для привратника.
type DeployKey struct {
	PublicKey string `json:"public_key" pattern:"^ssh-ed25519 AAAAC3NzaC1lZDI1NTE5[A-Za-z0-9+/]{48}( [^\\r\\n]{1,100})?$" doc:"a single ed25519 key without options"`
}

// KindOf вид компонента с учётом значения по умолчанию.
func (s Service) KindOf() string {
	if s.Kind == "" {
		return KindQuadlet
	}
	return s.Kind
}

// Requires что проекту нужно на хосте (плагины): их наличие проверяет verify (R-20).
func Requires(p *Project) []string {
	need := map[string]bool{}
	for _, s := range p.Services {
		if s.KindOf() == KindQuadlet {
			need["podman"] = true
		}
	}
	if p.Database != nil && p.Database.IsLocal() {
		need["pg"] = true
	}
	if len(p.Sites) > 0 {
		need["caddy"] = true
	}
	out := make([]string, 0, len(need))
	for n := range need {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// IsLocal истинно, если база на этом же сервере.
func (d Database) IsLocal() bool { return d.Server == "" || d.Server == ServerLocal }
