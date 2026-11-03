// Package project описывает проект (project.json) и проверяет его (SPEC-v0.md, раздел 2).
package project

// SchemaID идентификатор версии схемы описания проекта.
const SchemaID = "lite-deploy/project/v1"

const (
	// PortMin и PortMax диапазон портов по умолчанию (SPEC-v0.md, раздел 8).
	PortMin = 18000
	PortMax = 18999
)

// Project единственный вход для `project create`.
type Project struct {
	Schema      string             `json:"schema" enum:"lite-deploy/project/v1" doc:"версия схемы"`
	Name        string             `json:"name" pattern:"^[a-z][a-z0-9-]{1,30}$" doc:"имя проекта; совпадает с именем системного пользователя"`
	Description string             `json:"description,omitempty" maxLen:"200"`
	Adopt       bool               `json:"adopt,omitempty" doc:"взять под управление проект, настроенный вручную"`
	Database    *Database          `json:"database,omitempty"`
	Registry    *Registry          `json:"registry,omitempty"`
	Services    map[string]Service `json:"services" keypattern:"^[a-z][a-z0-9-]{0,15}$" minProps:"1" maxProps:"8"`
	Sites       []Site             `json:"sites" minItems:"1" maxItems:"8"`
	DeployKey   *DeployKey         `json:"deploy_key,omitempty"`
}

// Database база проекта на PostgreSQL хоста.
type Database struct {
	Engine           string `json:"engine" enum:"postgres"`
	ConnectionFormat string `json:"connection_format,omitempty" enum:"npgsql,url,libpq" doc:"формат строки подключения, по умолчанию url"`
}

// Registry вход в реестр образов.
type Registry struct {
	Host        string `json:"host" pattern:"^[a-z0-9.-]+(:[0-9]{2,5})?$" maxLen:"255"`
	Username    string `json:"username" pattern:"^[A-Za-z0-9][A-Za-z0-9-]{0,38}$"`
	TokenSecret string `json:"token_secret" pattern:"^secret://[a-z][a-z0-9_]{1,63}$" doc:"ссылка secret://имя; токен только на чтение"`
}

// Service один контейнерный сервис проекта.
type Service struct {
	Image         string            `json:"image" pattern:"^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+$" maxLen:"255" doc:"образ без тега"`
	ContainerPort int               `json:"container_port" min:"1" max:"65535"`
	HostPorts     []int             `json:"host_ports,omitempty" min:"18000" max:"18999" minItems:"2" maxItems:"2" doc:"порты слотов blue и green"`
	Health        *Health           `json:"health,omitempty"`
	Limits        *Limits           `json:"limits,omitempty"`
	Env           map[string]string `json:"env,omitempty" keypattern:"^[A-Za-z_][A-Za-z0-9_]*$" valpattern:"^[^\\r\\n]*$" valMaxLen:"1024" maxProps:"128"`
	Secrets       map[string]string `json:"secrets,omitempty" keypattern:"^[A-Za-z_][A-Za-z0-9_]*$" valpattern:"^secret://[a-z][a-z0-9_]{1,63}$" maxProps:"128"`
	DatabaseEnv   string            `json:"database_env,omitempty" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" doc:"переменная со строкой подключения к базе"`
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
	ListenPort   int     `json:"listen_port,omitempty" min:"18000" max:"18999" doc:"порт Caddy на 127.0.0.1; если нет, выбирается свободный"`
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
	PublicKey string `json:"public_key" pattern:"^ssh-ed25519 AAAAC3NzaC1lZDI1NTE5[A-Za-z0-9+/]{48}( [^\\r\\n]{1,100})?$" doc:"один ключ ed25519 без опций"`
}
