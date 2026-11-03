package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/schema"
)

// Коды ошибок построения плана (SPEC-v0.md, 6.3).
const (
	CodeInvalidProject           = "invalid_project"
	CodeSecretMissing            = "secret_missing"
	CodeUnusedSecret             = "unused_secret"
	CodeUnmanaged                = "project_exists_unmanaged"
	CodeAdoptPassword            = "adopt_password_unknown"
	CodePrecondition             = "precondition_failed"
	CodeQuotaExceeded            = "quota_exceeded"
	DBPasswordSecret             = "_db_password"
	defaultTTL                   = 24 * time.Hour
	defaultMaxProjects           = 50
	defaultPortLo, defaultPortHi = project.PortMin, project.PortMax
)

// BuildError ошибка построения плана.
type BuildError struct {
	Code    string
	Message string
	Issues  []schema.Issue
	Hint    string
}

func (e *BuildError) Error() string { return e.Code + ": " + e.Message }

// ProjectState несекретное состояние уже управляемого проекта (state/projects/<имя>.json).
type ProjectState struct {
	Hash     string        `json:"hash"`
	Ports    actions.Ports `json:"ports"`
	Secrets  []string      `json:"secrets"`
	Deployed bool          `json:"deployed"`
}

// State всё, что план знает о сервере. Строится из файлов в state/ (см. LoadState), в тестах задаётся напрямую.
type State struct {
	ServerID       string
	Now            time.Time
	TTL            time.Duration
	NewID          func(now time.Time) (string, error)
	PortRange      [2]int
	MaxProjects    int
	Projects       map[string]ProjectState // управляемые проекты
	ExistingUsers  map[string]bool         // системные пользователи, существующие на сервере
	Ciphertexts    map[string]string       // шифртекст секретов, которые владелец передаёт в этом плане
	RotatePassword bool                    // для adopt: сменить пароль существующей роли
}

// Build строит план project.create из проверенного описания (SPEC-v0.md, раздел 3).
// Ничего на системе не меняет и значений секретов не знает: на входе только шифртекст.
func Build(p *project.Project, st State) (*Plan, *BuildError) {
	if issues := project.Validate(p); len(issues) > 0 {
		return nil, &BuildError{Code: CodeInvalidProject, Message: "описание проекта не прошло проверку", Issues: issues, Hint: "исправьте поля из details"}
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
			return nil, &BuildError{Code: CodeUnmanaged, Message: fmt.Sprintf("пользователь %q уже есть, но проект не под управлением lite-deploy", name), Hint: `для проекта, настроенного вручную, задайте "adopt": true`}
		}
		if len(st.Projects) >= st.MaxProjects {
			return nil, &BuildError{Code: CodeQuotaExceeded, Message: fmt.Sprintf("достигнут предел проектов: %d", st.MaxProjects)}
		}
	}

	projHash, err := projectHash(p)
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
	b.add(actions.VerifyHostName, actions.VerifyHost{}, "Проверка сервера: ОС, podman, caddy, postgresql, правила Caddy и ufw")
	b.add(actions.CreateProjectUserName, actions.CreateProjectUser{Project: name}, fmt.Sprintf("Пользователь %s без пароля и sudo, linger, каталоги конфигурации", name))

	// В режиме adopt пароль существующей роли должен быть записан до provision_postgres.
	for _, s := range secretSteps {
		if s.Name == DBPasswordSecret {
			b.add(actions.SetSecretName, s, "Служебный секрет _db_password (существующий пароль роли), только шифртекст")
		}
	}
	if d.Database != nil {
		b.add(actions.ProvisionPostgresName, actions.ProvisionPostgres{
			Project: name, ConnectionFormat: d.Database.ConnectionFormat, RotatePassword: p.Adopt && !managed && st.RotatePassword,
		}, fmt.Sprintf("Роль и база %s в PostgreSQL, правило pg_hba для этой пары, пароль создаётся на сервере", project.RoleName(name)))
	}
	for _, s := range secretSteps {
		if s.Name != DBPasswordSecret {
			b.add(actions.SetSecretName, s, fmt.Sprintf("Секрет %s (только запись, значение в плане зашифровано)", s.Name))
		}
	}
	if d.Registry != nil {
		b.add(actions.RegistryLoginName, actions.RegistryLogin{
			Project: name, Registry: d.Registry.Host, Username: d.Registry.Username, Secret: secretName(d.Registry.TokenSecret),
		}, fmt.Sprintf("Вход в реестр %s от пользователя %s", d.Registry.Host, name))
	}

	svcNames := sortedKeys(d.Services)
	for _, sn := range svcNames {
		s := d.Services[sn]
		b.add(actions.WriteServiceEnvName, actions.WriteServiceEnv{Project: name, Service: sn, Env: envFor(s, d)},
			fmt.Sprintf("Файл настроек сервиса %s (права 600)", sn))
	}
	for _, sn := range svcNames {
		s := d.Services[sn]
		for i, slot := range []string{"blue", "green"} {
			b.add(actions.InstallQuadletName, actions.InstallQuadlet{
				Project: name, Service: sn, Slot: slot, HostPort: ports.Services[sn][i], ContainerPort: s.ContainerPort,
				Memory: s.Limits.Memory, Pids: s.Limits.Pids, DatabaseSocket: s.DatabaseEnv != "",
			}, fmt.Sprintf("Юнит Quadlet %s-%s-%s, порт 127.0.0.1:%d", name, sn, slot, ports.Services[sn][i]))
		}
	}
	b.add(actions.ReloadUserUnitsName, actions.ReloadUserUnits{Project: name}, "Перечитать юниты пользователя "+name)

	for _, s := range d.Sites {
		b.add(actions.InstallCaddySiteName, actions.InstallCaddySite{
			Project: name, Site: s.Name, Host: s.Host, ListenPort: ports.Sites[s.Name], UpstreamInitial: "not_deployed",
			TrustedProxy: s.TrustedProxy, MaxBody: s.MaxBody, Routes: siteRoutes(s),
		}, fmt.Sprintf("Сайт %s: %s на 127.0.0.1:%d, upstream «not deployed» до первой выкатки", s.Name, s.Host, ports.Sites[s.Name]))
	}
	b.add(actions.InstallReloadRuleName, actions.InstallReloadRule{Project: name}, "Правило sudoers: пользователь "+name+" может только systemctl reload caddy")
	b.add(actions.ReloadCaddyName, actions.ReloadCaddy{}, "Проверка конфигурации Caddy от пользователя caddy и reload")
	expect := "not_deployed"
	if cur.Deployed {
		expect = "deployed"
	}
	for _, s := range d.Sites {
		b.add(actions.ProbeSiteName, actions.ProbeSite{Host: s.Host, ListenPort: ports.Sites[s.Name], Expect: expect},
			fmt.Sprintf("Проверка: Caddy отвечает на имя %s (%s)", s.Host, expect))
	}
	if d.DeployKey != nil {
		b.add(actions.InstallDeployKeyName, actions.InstallDeployKey{Project: name, PublicKey: d.DeployKey.PublicKey},
			"Ключ CI с привратником: только deploy <хэш>, rollback, status")
	}
	b.add(actions.RecordProjectName, actions.RecordProject{Project: name, ProjectHash: projHash, Ports: ports}, "Запись метки управления и выбранных портов")
	if b.err != nil {
		return nil, &BuildError{Code: CodePrecondition, Message: b.err.Error()}
	}

	id, err := st.NewID(st.Now)
	if err != nil {
		return nil, &BuildError{Code: CodePrecondition, Message: "не удалось создать id плана: " + err.Error()}
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
		b.err = fmt.Errorf("неизвестное действие %s", action)
		return
	}
	raw, err := json.Marshal(params)
	if err != nil {
		b.err = err
		return
	}
	b.steps = append(b.steps, Step{ID: fmt.Sprintf("s%02d", len(b.steps)+1), Action: action, Class: def.Class, Params: raw, Summary: summary})
}

func projectHash(p *project.Project) (string, error) {
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

func envFor(s project.Service, d *project.Project) []actions.EnvVar {
	var out []actions.EnvVar
	for _, k := range sortedKeys(s.Env) {
		out = append(out, actions.EnvVar{Name: k, Kind: "literal", Value: s.Env[k]})
	}
	for _, k := range sortedKeys(s.Secrets) {
		out = append(out, actions.EnvVar{Name: k, Kind: "secret", Secret: secretName(s.Secrets[k])})
	}
	if s.DatabaseEnv != "" {
		out = append(out, actions.EnvVar{Name: s.DatabaseEnv, Kind: "database", Value: d.Database.ConnectionFormat})
	}
	return out
}

// planSecrets решает, какие шаги set_secret нужны: значение передано в этом плане, либо секрет уже есть на сервере.
func planSecrets(d *project.Project, st State, cur ProjectState, managed bool) ([]actions.SetSecret, *BuildError) {
	need := map[string]bool{}
	for _, s := range d.Services {
		for _, ref := range s.Secrets {
			need[secretName(ref)] = true
		}
	}
	if d.Registry != nil {
		need[secretName(d.Registry.TokenSecret)] = true
	}
	have := map[string]bool{}
	for _, n := range cur.Secrets {
		have[n] = true
	}

	var steps []actions.SetSecret
	var missing, unused []string
	for _, n := range sortedKeys(st.Ciphertexts) {
		if n == DBPasswordSecret {
			if !d.Adopt {
				return nil, &BuildError{Code: CodeInvalidProject, Message: "служебный секрет _db_password можно передать только в режиме adopt"}
			}
			continue
		}
		if !need[n] {
			unused = append(unused, n)
		}
	}
	if len(unused) > 0 {
		return nil, &BuildError{Code: CodeUnusedSecret, Message: "переданы шифртексты секретов, которых проект не использует: " + strings.Join(unused, ", ")}
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
		return nil, &BuildError{Code: CodeSecretMissing, Message: "не хватает секретов: " + strings.Join(missing, ", "),
			Hint: "зашифруйте значения командой secret encrypt и передайте шифртексты в план"}
	}
	if d.Adopt && d.Database != nil && !managed && !st.RotatePassword && st.Ciphertexts[DBPasswordSecret] == "" && !have[DBPasswordSecret] {
		return nil, &BuildError{Code: CodeAdoptPassword, Message: "пароль существующей роли неизвестен",
			Hint: "задайте секрет _db_password или разрешите смену пароля (rotate_password)"}
	}
	return steps, nil
}

// allocatePorts оставляет заданные порты и выбирает свободные для остальных; чужие порты не трогает.
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
	mine := map[int]bool{} // явные порты этого проекта
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
			return actions.Ports{}, &BuildError{Code: CodePrecondition, Message: fmt.Sprintf("порт %d уже занят проектом %s", port, owner)}
		}
		if port < lo || port > hi {
			return actions.Ports{}, &BuildError{Code: CodePrecondition, Message: fmt.Sprintf("порт %d вне диапазона %d–%d", port, lo, hi)}
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
	exhausted := &BuildError{Code: CodePrecondition, Message: fmt.Sprintf("нет свободных портов в диапазоне %d–%d", lo, hi)}
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
