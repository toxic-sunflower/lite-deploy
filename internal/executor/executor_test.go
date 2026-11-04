package executor

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
	"github.com/toxic-sunflower/lite-deploy/internal/secrets"
)

var now = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

// world подмена сервера: файловая система в памяти и список команд, на которые отвечает «система».
type world struct {
	t         *testing.T
	fsys      *host.MemFS
	runner    *host.FakeRunner
	http      *host.FakeHTTP
	users     map[string]bool
	role, db  bool
	rolePw    string
	validOK   bool
	podmanVer string
	cmds      []string
	pubkey    string
}

func newWorld(t *testing.T) *world {
	w := &world{t: t, fsys: host.NewMemFS(), users: map[string]bool{}, validOK: true, podmanVer: "5.0.3"}
	w.fsys.Now = func() time.Time { return now }
	f := w.fsys
	_ = f.WriteFile(OSRelease, []byte("ID=ubuntu\nVERSION_ID=\"24.04\"\n"), 0o644)
	_ = f.WriteFile(CaddyfilePth, []byte("import /etc/caddy/conf.d/*.caddy\n"), 0o644)
	_ = f.MkdirAll(ConfDir, 0o755)
	_ = f.WriteFile("/etc/postgresql/18/main/pg_hba.conf", []byte("local   all   postgres   peer\nlocal   all   all   peer\nhost all all 127.0.0.1/32 scram-sha-256\n"), 0o640)
	_ = f.Chown("/etc/postgresql/18/main/pg_hba.conf", "postgres", "postgres")
	_ = f.WriteFile("/etc/machine-id", []byte("abc\n"), 0o644)
	pub, _, err := secrets.EnsureKey(f, "/etc/lite-deploy/exec.age", "/etc/lite-deploy/exec.pub")
	if err != nil {
		t.Fatal(err)
	}
	w.pubkey = pub
	w.runner = &host.FakeRunner{Handler: w.handle}
	w.http = &host.FakeHTTP{Handler: w.handleHTTP}
	return w
}

func ok() (host.Result, error) { return host.Result{}, nil }

func (w *world) handle(c host.Cmd) (host.Result, error) {
	line := c.Name + " " + strings.Join(c.Args, " ")
	w.cmds = append(w.cmds, line)
	out := func(s string) (host.Result, error) { return host.Result{Stdout: []byte(s)}, nil }
	switch c.Name {
	case "id":
		if w.users[c.Args[1]] {
			return ok()
		}
		return host.Result{ExitCode: 1}, nil
	case "adduser":
		u := c.Args[len(c.Args)-1]
		w.users[u] = true
		_ = w.fsys.MkdirAll("/home/"+u, 0o755)
		_ = w.fsys.Chown("/home/"+u, u, u)
		old, _ := w.fsys.ReadFile(SubUID)
		_ = w.fsys.WriteFile(SubUID, append(old, []byte(u+":100000:65536\n")...), 0o644)
		old, _ = w.fsys.ReadFile(SubGID)
		_ = w.fsys.WriteFile(SubGID, append(old, []byte(u+":100000:65536\n")...), 0o644)
		return ok()
	case "loginctl":
		if c.Args[0] == "enable-linger" {
			_ = w.fsys.WriteFile(LingerDir+"/"+c.Args[1], nil, 0o644)
		}
		return out("loginctl 255")
	case "podman":
		return out("podman version " + w.podmanVer)
	case "caddy":
		return out("v2.8.4")
	case "visudo":
		return ok()
	case "ufw":
		return out("Status: active")
	case "psql":
		if c.Args[0] == "--version" {
			return out("psql 18.0")
		}
		if c.Env == nil || c.Env[0] != "PGPASSWORD="+w.rolePw {
			return host.Result{ExitCode: 2, Stderr: []byte("password authentication failed")}, nil
		}
		return out("1")
	case "runuser":
		if c.Args[1] == "caddy" {
			if !w.validOK {
				return host.Result{ExitCode: 1, Stderr: []byte("bad config")}, nil
			}
			return ok()
		}
		q := ""
		for i, a := range c.Args {
			if a == "-c" {
				q = c.Args[i+1]
			}
		}
		stdin := string(c.Stdin)
		switch {
		case q == "SHOW hba_file":
			return out("/etc/postgresql/18/main/pg_hba.conf\n")
		case strings.Contains(q, "pg_roles"):
			if w.role {
				return out("1\n")
			}
			return out("")
		case strings.Contains(q, "pg_database"):
			if w.db {
				return out("1\n")
			}
			return out("")
		case strings.HasPrefix(q, "CREATE DATABASE"):
			w.db = true
		case strings.Contains(stdin, "CREATE ROLE"), strings.Contains(stdin, "ALTER ROLE"):
			w.role = true
			w.rolePw = strings.Split(strings.Split(stdin, "'")[1], "'")[0]
		}
		return ok()
	case "systemctl":
		return ok()
	case "systemd-run":
		rest := strings.Join(c.Args, " ")
		switch {
		case strings.Contains(rest, "list-unit-files"):
			var b strings.Builder
			for _, n := range w.fsys.Dirs {
				_ = n
			}
			for p := range w.fsys.Files {
				if strings.Contains(p, "/.config/containers/systemd/") && strings.HasSuffix(p, ".container") {
					b.WriteString(strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], ".container") + ".service generated\n")
				}
			}
			return out(b.String())
		case strings.Contains(rest, "podman login"):
			u := strings.TrimSuffix(strings.SplitN(c.Args[0], "=", 2)[1], "@.host")
			_ = w.fsys.WriteFile("/home/"+u+"/.config/lite-deploy/auth.json", []byte(`{"auths":{"ghcr.io":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("toxic-sunflower:"+string(c.Stdin)))+`"}}}`), 0o644)
			_ = w.fsys.Chown("/home/"+u+"/.config/lite-deploy/auth.json", u, u)
		}
		return ok()
	}
	w.t.Fatalf("неожиданная команда: %s", line)
	return host.Result{}, nil
}

func (w *world) handleHTTP(url, hostHdr string) (host.HTTPResponse, error) {
	if strings.Contains(url, ":2019") {
		return host.HTTPResponse{Status: 200}, nil
	}
	return host.HTTPResponse{Status: 503, Body: []byte("not deployed")}, nil
}

func (w *world) env() Env {
	n := 0
	return Env{Runner: w.runner, FS: w.fsys, HTTP: w.http, Secrets: secrets.NewStore(w.fsys), KeyPath: "/etc/lite-deploy/exec.age",
		Rand: randReader(&n), Now: func() time.Time { return now }}
}

type counter struct{ n *int }

func (c counter) Read(p []byte) (int, error) {
	for i := range p {
		*c.n++
		p[i] = byte(*c.n * 7)
	}
	return len(p), nil
}

func randReader(n *int) counter { return counter{n} }

// buildPlan строит план для проекта FanVue Games (без adopt, чтобы пароль базы создавал исполнитель).
func (w *world) buildPlan(adopt bool, extra map[string]string) *plan.Plan {
	w.t.Helper()
	data, err := os.ReadFile("../../testdata/fanvue-games/project.json")
	if err != nil {
		w.t.Fatal(err)
	}
	p, issues := project.Parse(data)
	if len(issues) > 0 {
		w.t.Fatal(issues)
	}
	p.Adopt = adopt
	st, err := plan.LoadState(w.fsys, plan.DefaultPaths())
	if err != nil {
		w.t.Fatal(err)
	}
	st.Now = now
	st.Ciphertexts = map[string]string{}
	vals := map[string]string{"ghcr_read_token": "ghp_token", "auth_session_secret": "sess", "fanvue_webhook_secret": "hook", "admin_username": "root-admin", "admin_password": "pa$$word"}
	for k, v := range extra {
		vals[k] = v
	}
	for name, val := range vals {
		ct, err := secrets.Encrypt(w.pubkey, "fanvue-games", name, []byte(val), now)
		if err != nil {
			w.t.Fatal(err)
		}
		st.Ciphertexts[name] = ct
	}
	pl, berr := plan.Build(p, st)
	if berr != nil {
		w.t.Fatalf("Build: %v %v", berr, berr.Issues)
	}
	return pl
}

func states(rep *Report) map[string]int {
	m := map[string]int{}
	for _, s := range rep.Steps {
		m[s.State]++
	}
	return m
}

func (w *world) apply(pl *plan.Plan, dry bool) *Report {
	w.t.Helper()
	rep, err := Apply(context.Background(), w.env(), pl, dry)
	if err != nil {
		w.t.Fatal(err)
	}
	return rep
}

func TestApplyFromScratchThenIdempotent(t *testing.T) {
	w := newWorld(t)
	pl := w.buildPlan(false, nil)

	filesBefore, dirsBefore := len(w.fsys.Files), len(w.fsys.Dirs)
	dry := w.apply(pl, true)
	if dry.Failed != "" {
		t.Fatalf("dry-run упал: %+v", dry.Steps)
	}
	if n := states(dry)[StateWouldChange]; n < 10 {
		t.Fatalf("dry-run должен обещать изменения: %v", states(dry))
	}
	if w.users["fanvue-games"] || w.role || len(w.fsys.Files) != filesBefore || len(w.fsys.Dirs) != dirsBefore {
		t.Fatal("dry-run изменил систему")
	}
	for _, c := range w.cmds {
		if strings.HasPrefix(c, "adduser") || strings.Contains(c, "daemon-reload") || strings.Contains(c, "reload caddy") {
			t.Fatalf("dry-run выполнил %q", c)
		}
	}

	rep := w.apply(pl, false)
	if rep.Failed != "" {
		for _, s := range rep.Steps {
			t.Logf("%s %s %s %s %s", s.ID, s.Action, s.State, s.Detail, s.Error)
		}
		t.Fatalf("apply упал на %s", rep.Failed)
	}
	if states(rep)[StateChanged] == 0 {
		t.Fatalf("ничего не изменено: %v", states(rep))
	}

	// файлы и права
	check := func(path string, perm uint32, owner string) {
		t.Helper()
		info, err := w.fsys.Stat(path)
		if err != nil {
			t.Fatalf("нет %s: %v", path, err)
		}
		if uint32(info.Mode) != perm || info.Owner != owner {
			t.Errorf("%s: права %o владелец %s, ожидались %o %s", path, info.Mode, info.Owner, perm, owner)
		}
	}
	check("/home/fanvue-games/.config/lite-deploy/env/api.env", 0o600, "fanvue-games")
	check("/home/fanvue-games/.config/containers/systemd/fanvue-games-api-blue.container", 0o600, "fanvue-games")
	check("/etc/caddy/conf.d/fanvue-games-main.caddy", 0o644, "root")
	check("/etc/caddy/upstreams/fanvue-games-main.caddy", 0o644, "fanvue-games")
	check("/etc/sudoers.d/lite-deploy-fanvue-games", 0o440, "root")
	check("/home/fanvue-games/.ssh/authorized_keys", 0o600, "fanvue-games")
	check("/var/lib/lite-deploy/secrets/fanvue-games/_db_password", 0o600, "root")
	check("/var/lib/lite-deploy/state/projects/fanvue-games.json", 0o644, "root")
	if ok, _ := exists(w.fsys, "/etc/sudoers.d/lite-deploy-fanvue-games.tmp"); ok {
		t.Error("временный файл sudoers остался")
	}
	env, _ := w.fsys.ReadFile("/home/fanvue-games/.config/lite-deploy/env/api.env")
	if !strings.Contains(string(env), "ConnectionStrings__Default=Host=/var/run/postgresql;Database=fanvue_games;Username=fanvue_games;Password="+w.rolePw) {
		t.Errorf("env без строки подключения:\n%s", env)
	}
	hba, _ := w.fsys.ReadFile("/etc/postgresql/18/main/pg_hba.conf")
	if i, j := strings.Index(string(hba), "lite-deploy:begin fanvue_games"), strings.Index(string(hba), "local   all   all"); i < 0 || i > j {
		t.Errorf("правило pg_hba не выше общего:\n%s", hba)
	}
	if info, _ := w.fsys.Stat("/etc/postgresql/18/main/pg_hba.conf"); info.Owner != "postgres" || info.Mode != 0o640 {
		t.Errorf("pg_hba потерял владельца или права: %+v", info)
	}
	// секреты не в журнале и не в выводе
	j, _ := w.fsys.ReadFile(JournalPath)
	for _, bad := range []string{"pa$$word", "ghp_token", w.rolePw} {
		if strings.Contains(string(j), bad) {
			t.Fatalf("значение секрета попало в журнал: %s", bad)
		}
	}
	for _, s := range rep.Steps {
		if strings.Contains(s.Detail+s.Error, "pa$$word") || strings.Contains(s.Detail+s.Error, w.rolePw) {
			t.Fatalf("значение секрета в выводе шага %s", s.ID)
		}
	}
	for _, c := range w.cmds {
		if strings.Contains(c, w.rolePw) || strings.Contains(c, "ghp_token") {
			t.Fatalf("секрет в аргументах команды: %s", c)
		}
	}

	// повторное применение: везде unchanged (кроме reload_caddy и probe, которые ничего не меняют)
	again := w.apply(pl, false)
	if again.Failed != "" {
		t.Fatalf("повтор упал: %+v", again.Steps)
	}
	for _, s := range again.Steps {
		if s.State != StateUnchanged {
			t.Errorf("повтор: %s %s = %s (%s)", s.ID, s.Action, s.State, s.Detail)
		}
	}
	dry2 := w.apply(pl, true)
	for _, s := range dry2.Steps {
		if s.State != StateUnchanged {
			t.Errorf("dry-run после apply: %s %s = %s", s.ID, s.Action, s.State)
		}
	}
}

func TestStopsOnFirstFailure(t *testing.T) {
	w := newWorld(t)
	w.validOK = false
	pl := w.buildPlan(false, nil)
	rep := w.apply(pl, false)
	if rep.Failed == "" {
		t.Fatal("ожидалась ошибка проверки Caddy")
	}
	st := states(rep)
	if st[StateFailed] != 1 || st[StateSkipped] == 0 {
		t.Fatalf("ожидался один failed и пропущенные шаги: %v", st)
	}
	// файлы сайтов этого применения возвращены (новые удалены)
	if ok, _ := exists(w.fsys, "/etc/caddy/conf.d/fanvue-games-main.caddy"); ok {
		t.Error("файл сайта остался после неудачной проверки Caddy")
	}
	for _, c := range w.cmds {
		if c == "systemctl reload caddy" {
			t.Error("reload выполнен после неудачной проверки")
		}
	}
	// уже выполненные шаги остаются
	if !w.users["fanvue-games"] {
		t.Error("пользователь должен остаться")
	}
}

func TestProbeCatchesUnknownHost(t *testing.T) {
	w := newWorld(t)
	w.http.Handler = func(url, h string) (host.HTTPResponse, error) {
		if strings.Contains(url, ":2019") {
			return host.HTTPResponse{Status: 200}, nil
		}
		return host.HTTPResponse{Status: 200, Header: map[string][]string{"Server": {"Caddy"}}}, nil
	}
	rep := w.apply(w.buildPlan(false, nil), false)
	var failed *StepResult
	for i := range rep.Steps {
		if rep.Steps[i].State == StateFailed {
			failed = &rep.Steps[i]
		}
	}
	if failed == nil || failed.Action != "probe_site" || !strings.Contains(failed.Error, "не подхватил") {
		t.Fatalf("probe_site должен ловить пустой ответ Caddy: %+v", failed)
	}
}

func TestAdoptKeepsExistingAndBacksUp(t *testing.T) {
	w := newWorld(t)
	// «ручная» настройка: пользователь, роль, пароль известен владельцу, старый файл сайта и env
	w.users["fanvue-games"] = true
	_ = w.fsys.MkdirAll("/home/fanvue-games", 0o750)
	_ = w.fsys.Chown("/home/fanvue-games", "fanvue-games", "fanvue-games")
	_ = w.fsys.WriteFile(SubUID, []byte("fanvue-games:100000:65536\n"), 0o644)
	_ = w.fsys.WriteFile(SubGID, []byte("fanvue-games:100000:65536\n"), 0o644)
	_ = w.fsys.WriteFile(LingerDir+"/fanvue-games", nil, 0o644)
	w.role, w.db, w.rolePw = true, true, "oldpassword"
	old := "http://fanvue-games.prozorov.site:18080 {\n\trespond \"old\"\n}\n"
	_ = w.fsys.WriteFile(ConfDir+"/fanvue-games.caddy", []byte(old), 0o644)
	_ = w.fsys.MkdirAll("/home/fanvue-games/.config/lite-deploy/env", 0o700)
	_ = w.fsys.WriteFile("/home/fanvue-games/.config/lite-deploy/env/api.env", []byte("OLD=1\n"), 0o600)
	_ = w.fsys.Chown("/home/fanvue-games/.config/lite-deploy/env/api.env", "fanvue-games", "fanvue-games")

	pl := w.buildPlan(true, map[string]string{"_db_password": "oldpassword"})
	rep := w.apply(pl, false)
	if rep.Failed != "" {
		for _, s := range rep.Steps {
			t.Logf("%s %s %s %s %s", s.ID, s.Action, s.State, s.Detail, s.Error)
		}
		t.Fatalf("adopt упал на %s", rep.Failed)
	}
	for _, c := range w.cmds {
		if strings.HasPrefix(c, "adduser") || strings.Contains(c, "CREATE") {
			t.Errorf("adopt пересоздаёт существующее: %s", c)
		}
	}
	if b, err := w.fsys.ReadFile("/home/fanvue-games/.config/lite-deploy/env/api.env.bak-" + pl.ID); err != nil || string(b) != "OLD=1\n" {
		t.Errorf("старый env не сохранён: %v %q", err, b)
	}
	if b, err := w.fsys.ReadFile(ConfDir + "/fanvue-games.caddy.bak-" + pl.ID); err != nil || string(b) != old {
		t.Errorf("старый файл сайта не сохранён: %v", err)
	}
	if ok, _ := exists(w.fsys, ConfDir+"/fanvue-games.caddy"); ok {
		t.Error("старый файл сайта должен быть заменён: иначе два сайта на один адрес")
	}
	if w.rolePw != "oldpassword" {
		t.Error("пароль существующей роли изменён без rotate_password")
	}
}

func TestAdoptPasswordUnknown(t *testing.T) {
	w := newWorld(t)
	w.users["fanvue-games"] = true
	w.role, w.db = true, true
	pl := w.buildPlan(true, map[string]string{"_db_password": "x"})
	// убираем шаг с известным паролем: роль есть, секрета нет
	rep := w.apply(&plan.Plan{ID: pl.ID, Hash: pl.Hash, Project: pl.Project, Steps: stepsOnly(pl, "provision_postgres")}, false)
	if rep.Failed == "" || !strings.Contains(rep.Steps[0].Error, "adopt_password_unknown") {
		t.Fatalf("ожидалась adopt_password_unknown: %+v", rep.Steps)
	}
}

func stepsOnly(pl *plan.Plan, action string) []plan.Step {
	var out []plan.Step
	for _, s := range pl.Steps {
		if s.Action == action {
			out = append(out, s)
		}
	}
	return out
}

func TestSecretBoundToName(t *testing.T) {
	w := newWorld(t)
	pl := w.buildPlan(false, nil)
	// подменяем шифртекст одного секрета шифртекстом другого имени
	var a, b *plan.Step
	for i := range pl.Steps {
		if pl.Steps[i].Action == "set_secret" {
			if a == nil {
				a = &pl.Steps[i]
			} else if b == nil {
				b = &pl.Steps[i]
			}
		}
	}
	a.Params, b.Params = swapCipher(a.Params, b.Params)
	rep := w.apply(&plan.Plan{ID: pl.ID, Hash: pl.Hash, Project: pl.Project, Steps: []plan.Step{*a, *b}}, false)
	if rep.Failed == "" {
		t.Fatal("шифртекст другого имени должен быть отвергнут")
	}
}

func TestJournalFailureStopsBeforeWork(t *testing.T) {
	w := newWorld(t)
	pl := w.buildPlan(false, nil)
	e := w.env()
	e.FS = failingAppend{w.fsys}
	if _, err := Apply(context.Background(), e, pl, false); err == nil {
		t.Fatal("без журнала применение не должно начинаться")
	}
	if len(w.cmds) != 0 {
		t.Fatalf("команды выполнены без журнала: %v", w.cmds)
	}
}

type failingAppend struct{ *host.MemFS }

func (failingAppend) Append(string, []byte, os.FileMode) error { return os.ErrPermission }

func swapCipher(a, b []byte) ([]byte, []byte) {
	ca, cb := cipherOf(a), cipherOf(b)
	return []byte(strings.Replace(string(a), ca, cb, 1)), []byte(strings.Replace(string(b), cb, ca, 1))
}

func cipherOf(p []byte) string {
	s := string(p)
	i := strings.Index(s, `"ciphertext":"`) + len(`"ciphertext":"`)
	j := strings.Index(s[i:], `"`)
	return s[i : i+j]
}

func TestPodmanVersionPolicy(t *testing.T) {
	cases := []struct {
		ver        string
		wantFail   bool
		wantWarned bool
	}{{"5.7.0", false, false}, {"4.9.3", false, true}, {"4.4.1", false, true}, {"4.3.1", true, false}, {"3.4.4", true, false}}
	for _, c := range cases {
		w := newWorld(t)
		w.podmanVer = c.ver
		pl := w.buildPlan(false, nil)
		rep := w.apply(&plan.Plan{ID: pl.ID, Hash: pl.Hash, Project: pl.Project, Steps: stepsOnly(pl, "verify_host")}, true)
		s := rep.Steps[0]
		if failed := s.State == StateWouldFail; failed != c.wantFail {
			t.Errorf("podman %s: состояние %s (%s)", c.ver, s.State, s.Error)
		}
		if warned := strings.Contains(s.Detail, "проверено только на 5.x"); warned != c.wantWarned {
			t.Errorf("podman %s: предупреждение=%v, detail %q", c.ver, warned, s.Detail)
		}
	}
}
