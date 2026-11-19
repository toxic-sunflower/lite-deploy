// Package actions defines the fixed list of executor actions and the validation of their parameters
// (SPEC-v0.md, section 4). The executor accepts only these actions and only typed parameters (D-42).
package actions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// Action classes (D-29).
const (
	ClassSafe      = "safe"
	ClassNormal    = "normal"
	ClassDangerous = "dangerous"
)

// Def describes one action.
type Def struct {
	Name  string
	Class string
	New   func() any
	// Extra checks relations between fields beyond the tags; may be nil.
	Extra func(v any) []schema.Issue
}

var registry = map[string]Def{}

func register(d Def) { registry[d.Name] = d }

// Lookup finds an action by name.
func Lookup(name string) (Def, bool) {
	d, ok := registry[name]
	return d, ok
}

// Names lists all actions in alphabetical order.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Decode strictly parses the step parameters and validates them. An unknown action gives the unknown_action error.
func Decode(name string, raw json.RawMessage) (any, []schema.Issue) {
	d, ok := registry[name]
	if !ok {
		return nil, []schema.Issue{{Path: "/action", Code: "unknown_action", Message: fmt.Sprintf("action %q is not in the executor list", name)}}
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
		return nil, []schema.Issue{{Path: "/params", Code: "syntax", Message: "extra data after the parameters"}}
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

// Action names.
const (
	VerifyHostName        = "verify_host"
	CreateProjectUserName = "create_project_user"
	ProvisionPostgresName = "provision_postgres"
	SetSecretName         = "set_secret"
	RegistryLoginName     = "registry_login"
	WriteServiceEnvName   = "write_service_env"
	InstallQuadletName    = "install_quadlet"
	InstallUnitName       = "install_unit"
	WriteReleaseAuthName  = "write_release_auth"
	ReloadUserUnitsName   = "reload_user_units"
	InstallCaddySiteName  = "install_caddy_site"
	InstallReloadRuleName = "install_reload_rule"
	ReloadCaddyName       = "reload_caddy"
	ProbeSiteName         = "probe_site"
	InstallDeployKeyName  = "install_deploy_key"
	RecordProjectName     = "record_project"
)

// VerifyHost only reads: it checks the server preconditions.
type VerifyHost struct {
	NoPodman bool `json:"no_podman,omitempty" doc:"the project has no containers: podman is not needed"`
}

// CreateProjectUser creates the project system user.
type CreateProjectUser struct {
	Project string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
}

// ProvisionPostgres creates the project role and database.
type ProvisionPostgres struct {
	Project          string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	ConnectionFormat string `json:"connection_format" enum:"npgsql,url,libpq"`
	RotatePassword   bool   `json:"rotate_password,omitempty"`
}

// SetSecret writes a secret value (ciphertext) to the store.
type SetSecret struct {
	Project    string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Name       string `json:"name" pattern:"^_?[a-z][a-z0-9_]{1,63}$"`
	Ciphertext string `json:"ciphertext" pattern:"^-----BEGIN AGE ENCRYPTED FILE-----\\n[A-Za-z0-9+/=\\n]{20,}-----END AGE ENCRYPTED FILE-----$" maxLen:"16384" doc:"age ciphertext (armor)"`
}

// RegistryLogin logs in to the image registry as the project user.
type RegistryLogin struct {
	Project  string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Registry string `json:"registry" pattern:"^[a-z0-9.-]+(:[0-9]{2,5})?$" maxLen:"255"`
	Username string `json:"username" pattern:"^[A-Za-z0-9][A-Za-z0-9-]{0,38}$"`
	Secret   string `json:"secret" pattern:"^[a-z][a-z0-9_]{1,63}$"`
}

// EnvVar is one line of a service env file.
type EnvVar struct {
	Name   string `json:"name" pattern:"^[A-Za-z_][A-Za-z0-9_]*$"`
	Kind   string `json:"kind" enum:"literal,secret,database"`
	Value  string `json:"value,omitempty" pattern:"^[^\\r\\n]*$" maxLen:"1024" doc:"value for literal; connection string format for database"`
	Secret string `json:"secret,omitempty" pattern:"^[a-z][a-z0-9_]{1,63}$" doc:"secret name: for secret the variable value, for database with host the role password"`
	Host   string `json:"host,omitempty" pattern:"^[A-Za-z0-9.:-]{1,253}$" doc:"database only: PostgreSQL address on another server; without it the database is on this server, over a socket"`
	Port   int    `json:"port,omitempty" min:"1" max:"65535" doc:"database with host only"`
}

// WriteServiceEnv builds the service env file.
type WriteServiceEnv struct {
	Project string   `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Service string   `json:"service" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Env     []EnvVar `json:"env" maxItems:"300"`
}

// InstallQuadlet writes the Quadlet unit of one service slot.
type InstallQuadlet struct {
	Project        string   `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Service        string   `json:"service" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Slot           string   `json:"slot" enum:"blue,green"`
	HostPort       int      `json:"host_port" min:"18000" max:"18999"`
	ContainerPort  int      `json:"container_port" min:"1" max:"65535"`
	Memory         string   `json:"memory" pattern:"^[1-9][0-9]*[mg]$"`
	Pids           int      `json:"pids" min:"16" max:"4096"`
	DatabaseSocket bool     `json:"database_socket"`
	Data           []string `json:"data,omitempty" maxItems:"8" pattern:"^[a-z][a-z0-9-]{0,15}$" doc:"persistent directories, mounted at /data/<name>"`
}

// InstallUnit writes the user systemd unit of one slot of a kind unit component (a process on the host).
type InstallUnit struct {
	Project  string   `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Service  string   `json:"service" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Slot     string   `json:"slot" enum:"blue,green"`
	HostPort int      `json:"host_port" min:"18000" max:"18999"`
	Exec     []string `json:"exec" minItems:"1" maxItems:"32" doc:"program (relative to the release directory or an absolute path) and arguments"`
	Stop     []string `json:"stop,omitempty" maxItems:"32" doc:"stop command (ExecStop)"`
	Data     []string `json:"data,omitempty" maxItems:"8" pattern:"^[a-z][a-z0-9-]{0,15}$" doc:"persistent directories, available in LITE_DATA_<NAME>"`
	Memory   string   `json:"memory" pattern:"^[1-9][0-9]*[mg]$"`
	Pids     int      `json:"pids" min:"16" max:"4096"`
}

// WriteReleaseAuth writes the authorization header for downloading a release (a 600 file owned by the project user).
type WriteReleaseAuth struct {
	Project string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Service string `json:"service" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Secret  string `json:"secret" pattern:"^[a-z][a-z0-9_]{1,63}$"`
}

// ReloadUserUnits re-reads the units in the project user's session.
type ReloadUserUnits struct {
	Project string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
}

// SiteRoute is a special body limit for a path.
type SiteRoute struct {
	Path    string `json:"path" pattern:"^/[^\\s{}]*$" maxLen:"256"`
	MaxBody string `json:"max_body" pattern:"^[1-9][0-9]*(KB|MB)$"`
}

// InstallCaddySite writes a Caddy site file and the initial upstream.
type InstallCaddySite struct {
	Project         string      `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	Site            string      `json:"site" pattern:"^[a-z][a-z0-9-]{0,15}$"`
	Host            string      `json:"host" pattern:"^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$" maxLen:"253"`
	ListenPort      int         `json:"listen_port" min:"18000" max:"18999"`
	UpstreamInitial string      `json:"upstream_initial" enum:"not_deployed"`
	TrustedProxy    string      `json:"trusted_proxy" enum:"cloudflare,none"`
	Direct          bool        `json:"direct,omitempty" doc:"also serve the host on 80/443 with an automatic TLS certificate"`
	MaxBody         string      `json:"max_body" pattern:"^[1-9][0-9]*(KB|MB)$"`
	Routes          []SiteRoute `json:"routes,omitempty" maxItems:"16"`
}

// InstallReloadRule installs the sudoers rule for reloading Caddy.
type InstallReloadRule struct {
	Project string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
}

// ReloadCaddy checks the Caddy configuration and reloads it.
type ReloadCaddy struct{}

// ProbeSite checks that Caddy answers for the site name.
type ProbeSite struct {
	Host       string `json:"host" pattern:"^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$" maxLen:"253"`
	ListenPort int    `json:"listen_port" min:"18000" max:"18999"`
	Expect     string `json:"expect" enum:"not_deployed,deployed"`
}

// InstallDeployKey installs the CI key with the gatekeeper.
type InstallDeployKey struct {
	Project   string `json:"project" pattern:"^[a-z][a-z0-9-]{1,30}$"`
	PublicKey string `json:"public_key" pattern:"^ssh-ed25519 AAAAC3NzaC1lZDI1NTE5[A-Za-z0-9+/]{48}( [^\\r\\n]{1,100})?$"`
}

// Ports are the chosen ports of a project.
type Ports struct {
	Services map[string][]int `json:"services" keypattern:"^[a-z][a-z0-9-]{0,15}$" maxProps:"8" min:"18000" max:"18999" minItems:"2" maxItems:"2"`
	Sites    map[string]int   `json:"sites" keypattern:"^[a-z][a-z0-9-]{0,15}$" maxProps:"8" min:"18000" max:"18999"`
}

// RecordProject records the management mark and the state.
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
	register(Def{Name: InstallUnitName, Class: ClassNormal, New: func() any { return &InstallUnit{} }, Extra: checkExec})
	register(Def{Name: WriteReleaseAuthName, Class: ClassNormal, New: func() any { return &WriteReleaseAuth{} }})
	register(Def{Name: ReloadUserUnitsName, Class: ClassNormal, New: func() any { return &ReloadUserUnits{} }})
	register(Def{Name: InstallCaddySiteName, Class: ClassNormal, New: func() any { return &InstallCaddySite{} }})
	register(Def{Name: InstallReloadRuleName, Class: ClassNormal, New: func() any { return &InstallReloadRule{} }})
	register(Def{Name: ReloadCaddyName, Class: ClassNormal, New: func() any { return &ReloadCaddy{} }})
	register(Def{Name: ProbeSiteName, Class: ClassNormal, New: func() any { return &ProbeSite{} }})
	register(Def{Name: InstallDeployKeyName, Class: ClassNormal, New: func() any { return &InstallDeployKey{} }})
	register(Def{Name: RecordProjectName, Class: ClassNormal, New: func() any { return &RecordProject{} }})
}
