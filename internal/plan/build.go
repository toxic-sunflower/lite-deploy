package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

var armorRe = regexp.MustCompile(secrets.ArmorPattern)

// Plan building error codes (SPEC-v0.md, 6.3).
const (
	CodeInvalidProject           = "invalid_project"
	CodeSecretMissing            = "secret_missing"
	CodeUnusedSecret             = "unused_secret"
	CodeInvalidSecret            = "invalid_secret"
	CodeUnmanaged                = "project_exists_unmanaged"
	CodeAdoptPassword            = "adopt_password_unknown"
	CodePrecondition             = "precondition_failed"
	CodeQuotaExceeded            = "quota_exceeded"
	DBPasswordSecret             = "_db_password"
	defaultTTL                   = 24 * time.Hour
	defaultMaxProjects           = 50
	defaultPortLo, defaultPortHi = project.PortMin, project.PortMax
)

// BuildError is a plan building error.
type BuildError struct {
	Code    string
	Message string
	Issues  []schema.Issue
	Hint    string
}

func (e *BuildError) Error() string { return e.Code + ": " + e.Message }

// ProjectState is the non-secret state of an already managed project (state/projects/<name>.json).
type ProjectState struct {
	Hash     string        `json:"hash"`
	Ports    actions.Ports `json:"ports"`
	Secrets  []string      `json:"secrets"`
	Deployed bool          `json:"deployed"`
}

// State is everything the plan knows about the server. It is built from the files in state/ (see LoadState), and set directly in tests.
type State struct {
	ServerID       string
	Now            time.Time
	TTL            time.Duration
	NewID          func(now time.Time) (string, error)
	PortRange      [2]int
	MaxProjects    int
	Projects       map[string]ProjectState // managed projects
	ExistingUsers  map[string]bool         // system users that exist on the server
	Ciphertexts    map[string]string       // the ciphertext of the secrets the owner passes in this plan
	RotatePassword bool                    // for adopt: change the password of the existing role
	Servers        map[string]string       // server registry: name → address (lite server add)
}

// Build builds a project.create plan from a validated description (SPEC-v0.md, section 3).
// It changes nothing on the system and knows no secret values: the input is ciphertext only.
func Build(p *project.Project, st State) (*Plan, *BuildError) {
	if issues := project.Validate(p); len(issues) > 0 {
		return nil, &BuildError{Code: CodeInvalidProject, Message: "the project description did not pass validation", Issues: issues, Hint: "fix the fields listed in details"}
	}
	if st.Now.IsZero() {
		st.Now = time.Now()
	}
	st.Now = st.Now.UTC().Truncate(time.Second)
	if st.TTL == 0 {
		st.TTL = defaultTTL
	}
	if st.PortRange == [2]int{} {
		st.PortRange = [2]int{defaultPortLo, defaultPortHi}
	}
	if st.MaxProjects == 0 {
		st.MaxProjects = defaultMaxProjects
	}
	if st.NewID == nil {
		st.NewID = defaultID
	}
	name := p.Name
	cur, managed := st.Projects[name]

	if !managed {
		if st.ExistingUsers[name] && !p.Adopt {
			return nil, &BuildError{Code: CodeUnmanaged, Message: fmt.Sprintf("user %q already exists, but the project is not managed by lite-deploy", name), Hint: `for a project set up by hand, set "adopt": true`}
		}
		if len(st.Projects) >= st.MaxProjects {
			return nil, &BuildError{Code: CodeQuotaExceeded, Message: fmt.Sprintf("the project limit is reached: %d", st.MaxProjects)}
		}
	}

	if db := p.Database; db != nil && !db.IsLocal() {
		if _, ok := st.Servers[db.Server]; !ok {
			return nil, &BuildError{Code: CodePrecondition, Message: fmt.Sprintf("server %q is not in the registry", db.Server), Hint: "sudo lite server add " + db.Server + " ADDRESS"}
		}
	}

	projHash, err := ProjectHash(p)
	if err != nil {
		return nil, &BuildError{Code: CodePrecondition, Message: err.Error()}
	}
	d := project.WithDefaults(p)

	ports, perr := allocatePorts(d, st, cur)
	if perr != nil {
		return nil, perr
	}

	secretSteps, serr := planSecrets(d, st, cur, managed)
	if serr != nil {
		return nil, serr
	}

	b := &builder{}
	hasContainers := false
	for _, s := range d.Services {
		hasContainers = hasContainers || s.KindOf() == project.KindQuadlet
	}
	b.add(actions.VerifyHostName, actions.VerifyHost{NoPodman: !hasContainers}, "Server check: OS, podman, caddy, postgresql, Caddy rules and ufw")
	b.add(actions.CreateProjectUserName, actions.CreateProjectUser{Project: name}, fmt.Sprintf("User %s without a password and sudo, linger, configuration directories", name))

	// In adopt mode the password of the existing role must be written before provision_postgres.
	for _, s := range secretSteps {
		if s.Name == DBPasswordSecret {
			b.add(actions.SetSecretName, s, "Internal secret _db_password (the existing role password), ciphertext only")
		}
	}
	if d.Database != nil && d.Database.IsLocal() {
		b.add(actions.ProvisionPostgresName, actions.ProvisionPostgres{
			Project: name, ConnectionFormat: d.Database.ConnectionFormat, RotatePassword: p.Adopt && !managed && st.RotatePassword,
		}, fmt.Sprintf("Role and database %s in PostgreSQL, a pg_hba rule for this pair, the password is generated on the server", project.RoleName(name)))
	}
	for _, s := range secretSteps {
		if s.Name != DBPasswordSecret {
			b.add(actions.SetSecretName, s, fmt.Sprintf("Secret %s (write-only, the value in the plan is encrypted)", s.Name))
		}
	}
	if d.Registry != nil {
		b.add(actions.RegistryLoginName, actions.RegistryLogin{
			Project: name, Registry: d.Registry.Host, Username: d.Registry.Username, Secret: secretName(d.Registry.TokenSecret),
		}, fmt.Sprintf("Registry login %s as user %s", d.Registry.Host, name))
	}

	svcNames := sortedKeys(d.Services)
	for _, sn := range svcNames {
		s := d.Services[sn]
		b.add(actions.WriteServiceEnvName, actions.WriteServiceEnv{Project: name, Service: sn, Env: envFor(s, d, st)},
			fmt.Sprintf("Settings file of service %s (mode 600)", sn))
		if s.KindOf() == project.KindUnit && s.Release != nil && s.Release.TokenSecret != "" {
			b.add(actions.WriteReleaseAuthName, actions.WriteReleaseAuth{Project: name, Service: sn, Secret: secretName(s.Release.TokenSecret)},
				fmt.Sprintf("Authorization header for downloading the release %s (mode 600)", sn))
		}
	}
	for _, sn := range svcNames {
		s := d.Services[sn]
		for i, slot := range []string{"blue", "green"} {
			if s.KindOf() == project.KindUnit {
				b.add(actions.InstallUnitName, actions.InstallUnit{
					Project: name, Service: sn, Slot: slot, HostPort: ports.Services[sn][i], Exec: s.Exec, Stop: s.Stop, Data: s.Data,
					Memory: s.Limits.Memory, Pids: s.Limits.Pids,
				}, fmt.Sprintf("systemd unit %s-%s-%s, port 127.0.0.1:%d", name, sn, slot, ports.Services[sn][i]))
				continue
			}
			b.add(actions.InstallQuadletName, actions.InstallQuadlet{
				Project: name, Service: sn, Slot: slot, HostPort: ports.Services[sn][i], ContainerPort: s.ContainerPort,
				Memory: s.Limits.Memory, Pids: s.Limits.Pids, DatabaseSocket: s.DatabaseEnv != "" && d.Database.IsLocal(), Data: s.Data,
			}, fmt.Sprintf("Quadlet unit %s-%s-%s, port 127.0.0.1:%d", name, sn, slot, ports.Services[sn][i]))
		}
	}
	b.add(actions.ReloadUserUnitsName, actions.ReloadUserUnits{Project: name}, "Reload the units of user "+name)

	for _, s := range d.Sites {
		b.add(actions.InstallCaddySiteName, actions.InstallCaddySite{
			Project: name, Site: s.Name, Host: s.Host, ListenPort: ports.Sites[s.Name], UpstreamInitial: "not_deployed",
			TrustedProxy: s.TrustedProxy, MaxBody: s.MaxBody, Routes: siteRoutes(s),
		}, fmt.Sprintf("Site %s: %s on 127.0.0.1:%d, upstream \"not deployed\" until the first deploy", s.Name, s.Host, ports.Sites[s.Name]))
	}
	b.add(actions.InstallReloadRuleName, actions.InstallReloadRule{Project: name}, "sudoers rule: user "+name+" may only run systemctl reload caddy")
	b.add(actions.ReloadCaddyName, actions.ReloadCaddy{}, "Check the Caddy configuration as user caddy and reload")
	expect := "not_deployed"
	if cur.Deployed {
		expect = "deployed"
	}
	for _, s := range d.Sites {
		b.add(actions.ProbeSiteName, actions.ProbeSite{Host: s.Host, ListenPort: ports.Sites[s.Name], Expect: expect},
			fmt.Sprintf("Check: Caddy answers for the name %s (%s)", s.Host, expect))
	}
	if d.DeployKey != nil {
		b.add(actions.InstallDeployKeyName, actions.InstallDeployKey{Project: name, PublicKey: d.DeployKey.PublicKey},
			"CI key with a gatekeeper: only deploy <hash>, rollback, status")
	}
	b.add(actions.RecordProjectName, actions.RecordProject{Project: name, ProjectHash: projHash, Ports: ports}, "Record the management mark and the chosen ports")
	if b.err != nil {
		return nil, &BuildError{Code: CodePrecondition, Message: b.err.Error()}
	}

	id, err := st.NewID(st.Now)
	if err != nil {
		return nil, &BuildError{Code: CodePrecondition, Message: "could not create a plan id: " + err.Error()}
	}
	pl := &Plan{
		Schema: SchemaID, ID: id,
		CreatedAt: st.Now.Format(time.RFC3339), ExpiresAt: st.Now.Add(st.TTL).Format(time.RFC3339),
		ServerID: st.ServerID, Kind: KindProjectCreate, Project: name, ProjectHash: projHash, Steps: b.steps,
	}
	if err := pl.Seal(); err != nil {
		return nil, &BuildError{Code: CodePrecondition, Message: err.Error()}
	}
	return pl, nil
}

type builder struct {
	steps []Step
	err   error
}

func (b *builder) add(action string, params any, summary string) {
	def, ok := actions.Lookup(action)
	if !ok {
		b.err = fmt.Errorf("unknown action %s", action)
		return
	}
	raw, err := json.Marshal(params)
	if err != nil {
		b.err = err
		return
	}
	b.steps = append(b.steps, Step{ID: fmt.Sprintf("s%02d", len(b.steps)+1), Action: action, Class: def.Class, Params: raw, Summary: summary})
}

// ProjectHash is the hash of the canonical project description.
func ProjectHash(p *project.Project) (string, error) {
	c, err := CanonicalValue(p)
	if err != nil {
		return "", err
	}
	return HashBytes(c), nil
}

func secretName(ref string) string { return strings.TrimPrefix(ref, "secret://") }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func siteRoutes(s project.Site) []actions.SiteRoute {
	var out []actions.SiteRoute
	for _, r := range s.Routes {
		out = append(out, actions.SiteRoute{Path: r.Path, MaxBody: r.MaxBody})
	}
	return out
}

func envFor(s project.Service, d *project.Project, st State) []actions.EnvVar {
	var out []actions.EnvVar
	for _, k := range sortedKeys(s.Env) {
		out = append(out, actions.EnvVar{Name: k, Kind: "literal", Value: s.Env[k]})
	}
	for _, k := range sortedKeys(s.Secrets) {
		out = append(out, actions.EnvVar{Name: k, Kind: "secret", Secret: secretName(s.Secrets[k])})
	}
	if s.DatabaseEnv != "" {
		ev := actions.EnvVar{Name: s.DatabaseEnv, Kind: "database", Value: d.Database.ConnectionFormat}
		if !d.Database.IsLocal() {
			ev.Host, ev.Port, ev.Secret = st.Servers[d.Database.Server], d.Database.Port, secretName(d.Database.PasswordSecret)
		}
		out = append(out, ev)
	}
	return out
}

// planSecrets decides which set_secret steps are needed: the value is passed in this plan, or the secret is already on the server.
func planSecrets(d *project.Project, st State, cur ProjectState, managed bool) ([]actions.SetSecret, *BuildError) {
	need := map[string]bool{}
	for _, s := range d.Services {
		for _, ref := range s.Secrets {
			need[secretName(ref)] = true
		}
		if s.Release != nil && s.Release.TokenSecret != "" {
			need[secretName(s.Release.TokenSecret)] = true
		}
	}
	if d.Registry != nil {
		need[secretName(d.Registry.TokenSecret)] = true
	}
	if d.Database != nil && !d.Database.IsLocal() {
		need[secretName(d.Database.PasswordSecret)] = true
	}
	have := map[string]bool{}
	for _, n := range cur.Secrets {
		have[n] = true
	}

	for _, n := range sortedKeys(st.Ciphertexts) {
		if !secrets.ValidName(n) || !armorRe.MatchString(st.Ciphertexts[n]) || len(st.Ciphertexts[n]) > secrets.MaxCiphertextSize {
			return nil, &BuildError{Code: CodeInvalidSecret, Message: "ciphertext of secret " + n + " does not look like `secret encrypt` output (age armor)"}
		}
	}
	var steps []actions.SetSecret
	var missing, unused []string
	for _, n := range sortedKeys(st.Ciphertexts) {
		if n == DBPasswordSecret {
			if !d.Adopt {
				return nil, &BuildError{Code: CodeInvalidProject, Message: "the internal secret _db_password can only be passed in adopt mode"}
			}
			continue
		}
		if !need[n] {
			unused = append(unused, n)
		}
	}
	if len(unused) > 0 {
		return nil, &BuildError{Code: CodeUnusedSecret, Message: "ciphertexts were passed for secrets the project does not use: " + strings.Join(unused, ", ")}
	}
	if ct, ok := st.Ciphertexts[DBPasswordSecret]; ok {
		steps = append(steps, actions.SetSecret{Project: d.Name, Name: DBPasswordSecret, Ciphertext: ct})
	}
	for _, n := range sortedKeys(need) {
		if ct, ok := st.Ciphertexts[n]; ok {
			steps = append(steps, actions.SetSecret{Project: d.Name, Name: n, Ciphertext: ct})
		} else if !have[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return nil, &BuildError{Code: CodeSecretMissing, Message: "missing secrets: " + strings.Join(missing, ", "),
			Hint: "encrypt the values with secret encrypt and pass the ciphertexts into the plan"}
	}
	if d.Adopt && d.Database != nil && d.Database.IsLocal() && !managed && !st.RotatePassword && st.Ciphertexts[DBPasswordSecret] == "" && !have[DBPasswordSecret] {
		return nil, &BuildError{Code: CodeAdoptPassword, Message: "the password of the existing role is unknown",
			Hint: "set the _db_password secret or allow a password change (rotate_password)"}
	}
	return steps, nil
}

// allocatePorts keeps the given ports and picks free ones for the rest; it does not touch other projects' ports.
func allocatePorts(d *project.Project, st State, cur ProjectState) (actions.Ports, *BuildError) {
	lo, hi := st.PortRange[0], st.PortRange[1]
	taken := map[int]string{}
	for pn, ps := range st.Projects {
		if pn == d.Name {
			continue
		}
		for sn, pp := range ps.Ports.Services {
			for _, x := range pp {
				taken[x] = pn + "/" + sn
			}
		}
		for sn, x := range ps.Ports.Sites {
			taken[x] = pn + "/" + sn
		}
	}
	mine := map[int]bool{} // the explicit ports of this project
	for _, s := range d.Services {
		for _, hp := range s.HostPorts {
			mine[hp] = true
		}
	}
	for _, s := range d.Sites {
		if s.ListenPort != 0 {
			mine[s.ListenPort] = true
		}
	}
	for port := range mine {
		if owner, ok := taken[port]; ok {
			return actions.Ports{}, &BuildError{Code: CodePrecondition, Message: fmt.Sprintf("port %d is already used by project %s", port, owner)}
		}
		if port < lo || port > hi {
			return actions.Ports{}, &BuildError{Code: CodePrecondition, Message: fmt.Sprintf("port %d is outside the range %d–%d", port, lo, hi)}
		}
	}
	reserved := map[int]bool{}
	for p := range taken {
		reserved[p] = true
	}
	for p := range mine {
		reserved[p] = true
	}
	next := lo
	pick := func(prefer int) (int, bool) {
		if prefer != 0 && !reserved[prefer] {
			reserved[prefer] = true
			return prefer, true
		}
		for ; next <= hi; next++ {
			if !reserved[next] {
				reserved[next] = true
				return next, true
			}
		}
		return 0, false
	}
	out := actions.Ports{Services: map[string][]int{}, Sites: map[string]int{}}
	exhausted := &BuildError{Code: CodePrecondition, Message: fmt.Sprintf("no free ports in the range %d–%d", lo, hi)}
	for _, sn := range sortedKeys(d.Services) {
		s := d.Services[sn]
		if len(s.HostPorts) == 2 {
			out.Services[sn] = append([]int(nil), s.HostPorts...)
			continue
		}
		old := cur.Ports.Services[sn]
		pair := make([]int, 2)
		for i := range pair {
			prefer := 0
			if len(old) == 2 {
				prefer = old[i]
			}
			x, ok := pick(prefer)
			if !ok {
				return out, exhausted
			}
			pair[i] = x
		}
		out.Services[sn] = pair
	}
	for _, s := range d.Sites {
		if s.ListenPort != 0 {
			out.Sites[s.Name] = s.ListenPort
			continue
		}
		x, ok := pick(cur.Ports.Sites[s.Name])
		if !ok {
			return out, exhausted
		}
		out.Sites[s.Name] = x
	}
	return out, nil
}
