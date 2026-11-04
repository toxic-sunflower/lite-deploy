package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/actions"
	"github.com/toxic-sunflower/lite-deploy/internal/plan"
	"github.com/toxic-sunflower/lite-deploy/internal/render"
)

func init() {
	handlers[actions.InstallQuadletName] = installQuadlet
	handlers[actions.ReloadUserUnitsName] = reloadUserUnits
	handlers[actions.InstallCaddySiteName] = installCaddySite
	handlers[actions.InstallReloadRuleName] = installReloadRule
	handlers[actions.ReloadCaddyName] = reloadCaddy
	handlers[actions.ProbeSiteName] = probeSite
	handlers[actions.InstallDeployKeyName] = installDeployKey
	handlers[actions.RecordProjectName] = recordProject
}

func installQuadlet(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.InstallQuadlet)
	path := home(p.Project) + "/.config/containers/systemd/" + render.UnitName(p.Project, p.Service, p.Slot) + ".container"
	changed, bak, err := r.ensureFile(fileSpec{path: path, data: []byte(render.Quadlet(*p)), perm: 0o600, owner: p.Project, group: p.Project})
	if err != nil {
		return outcome{}, err
	}
	if changed {
		r.unitsChanged = true
	}
	return outcome{changed: changed, detail: bakNote(bak)}, nil
}

// reloadUserUnits: daemon-reload в сессии пользователя, затем проверка, что юниты видны.
func reloadUserUnits(ctx context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.ReloadUserUnits)
	want := append([]string(nil), r.expectUnits[p.Project]...)
	sort.Strings(want)
	visible := func() (bool, string, error) {
		res, err := r.asUser(ctx, p.Project, nil, "systemctl", "--user", "list-unit-files", "--no-legend", p.Project+"-*.service")
		if err != nil {
			return false, "", err
		}
		out := string(res.Stdout)
		var missing []string
		for _, u := range want {
			if !strings.Contains(out, u) {
				missing = append(missing, u)
			}
		}
		return len(missing) == 0, strings.Join(missing, ", "), nil
	}
	if r.dry {
		return outcome{changed: r.unitsChanged}, nil
	}
	if !r.unitsChanged {
		if ok, _, err := visible(); err == nil && ok {
			return outcome{}, nil
		}
	}
	res, err := r.asUser(ctx, p.Project, nil, "systemctl", "--user", "daemon-reload")
	if err != nil {
		return outcome{}, err
	}
	if res.ExitCode != 0 {
		return outcome{}, fmt.Errorf("daemon-reload: код %d: %s", res.ExitCode, trimErr(res))
	}
	ok, missing, err := visible()
	if err != nil {
		return outcome{}, err
	}
	if !ok {
		return outcome{}, fmt.Errorf("после daemon-reload юниты не видны: %s", missing)
	}
	return outcome{changed: true}, nil
}

func hostLineRe(host string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*(https?://)?` + regexp.QuoteMeta(host) + `(:[0-9]+)?\s*[,{]`)
}

// installCaddySite (SPEC-v0.md, 4.3): файл сайта, upstream, каталог журнала. Не перезагружает Caddy.
func installCaddySite(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.InstallCaddySite)
	var notes []string
	changedAny := false

	if mk, err := r.ensureDir(UpstreamDir, 0o755, "root", "root"); err != nil {
		return outcome{}, err
	} else if mk {
		changedAny = true
	}
	if mk, err := r.ensureDir(CaddyLogDir, 0o755, "caddy", "caddy"); err != nil {
		return outcome{}, err
	} else if mk {
		changedAny = true
	}

	// Старые файлы с тем же адресом сайта (ручная настройка при adopt) заменяются, иначе будут два сайта на один адрес.
	site := ConfDir + "/" + render.SiteFileName(p.Project, p.Site)
	if names, err := r.FS.ReadDir(ConfDir); err == nil {
		managed := r.managedProjects()
		re := hostLineRe(p.Host)
		for _, n := range names {
			full := ConfDir + "/" + n
			if full == site || !strings.HasSuffix(n, ".caddy") {
				continue
			}
			raw, err := r.FS.ReadFile(full)
			if err != nil || !re.Match(raw) {
				continue
			}
			for _, mp := range managed {
				if mp != p.Project && strings.HasPrefix(n, mp+"-") {
					return outcome{}, fmt.Errorf("адрес %s уже занят проектом %s (файл %s)", p.Host, mp, n)
				}
			}
			notes = append(notes, "заменён старый файл "+n)
			changedAny = true
			if !r.dry {
				info, _ := r.FS.Stat(full)
				bak := full + ".bak-" + r.pl.ID
				if err := r.FS.WriteFile(bak, raw, info.Mode); err != nil {
					return outcome{}, err
				}
				r.undo = append(r.undo, siteUndo{path: full, prev: raw, existed: true, mode: info.Mode})
				if err := r.FS.Remove(full); err != nil {
					return outcome{}, err
				}
			}
		}
	}

	data := []byte(render.CaddySite(*p))
	prev, perr := r.FS.ReadFile(site)
	info, _ := r.FS.Stat(site)
	changed, bak, err := r.ensureFile(fileSpec{path: site, data: data, perm: 0o644, owner: "root", group: "root"})
	if err != nil {
		return outcome{}, err
	}
	if changed {
		changedAny = true
		r.sitesChanged = true
		if !r.dry {
			r.undo = append(r.undo, siteUndo{path: site, prev: prev, existed: perr == nil, mode: info.Mode})
		}
	}
	notes = append(notes, bakNote(bak))

	up := render.UpstreamPath(p.Project, p.Site)
	if ok, err := exists(r.FS, up); err != nil {
		return outcome{}, err
	} else if !ok {
		changedAny = true
		if !r.dry {
			if err := r.FS.WriteFile(up, []byte(render.UpstreamNotDeployed), 0o644); err != nil {
				return outcome{}, err
			}
			if err := r.FS.Chown(up, p.Project, p.Project); err != nil {
				return outcome{}, err
			}
		}
	}
	if changedAny && !changed {
		r.sitesChanged = r.sitesChanged || len(notes) > 1
	}
	if r.dry && changedAny {
		r.sitesChanged = true
	}
	return outcome{changed: changedAny, detail: joinDetail(notes...)}, nil
}

func (r *run) managedProjects() []string {
	names, err := r.FS.ReadDir(r.StateDir + "/projects")
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range names {
		if strings.HasSuffix(n, ".json") {
			out = append(out, strings.TrimSuffix(n, ".json"))
		}
	}
	return out
}

// installReloadRule: sudoers пишется во временный файл (имя с точкой sudo игнорирует), проверяется visudo -cf, затем ставится.
func installReloadRule(ctx context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.InstallReloadRule)
	final := SudoersDir + "/lite-deploy-" + p.Project
	data := []byte(render.Sudoers(p.Project))
	if cur, err := r.FS.ReadFile(final); err == nil {
		if info, _ := r.FS.Stat(final); bytes.Equal(cur, data) && info.Mode == 0o440 && info.Owner == "root" {
			return outcome{}, nil
		}
	}
	if r.dry {
		return outcome{changed: true}, nil
	}
	tmp := final + ".tmp"
	if err := r.FS.WriteFile(tmp, data, 0o440); err != nil {
		return outcome{}, err
	}
	defer func() { _ = r.FS.Remove(tmp) }()
	if _, err := r.must(ctx, hostCmd("visudo", "-cf", tmp)); err != nil {
		return outcome{}, err
	}
	if err := r.FS.WriteFile(final, data, 0o440); err != nil {
		return outcome{}, err
	}
	return outcome{changed: true}, r.FS.Chown(final, "root", "root")
}

// reloadCaddy: проверка конфигурации от пользователя caddy, затем reload. При ошибке проверки файлы сайтов
// этого применения возвращаются в прежнее состояние. Reload безвреден, поэтому в реальном запуске выполняется всегда
// (иначе прерванное применение оставило бы Caddy без новых файлов); `changed` только если сайты менялись.
func reloadCaddy(ctx context.Context, r *run, _ any) (outcome, error) {
	if r.dry {
		return outcome{changed: r.sitesChanged}, nil
	}
	res, err := r.Runner.Run(ctx, hostCmd("runuser", "-u", "caddy", "--", "caddy", "validate", "--config", CaddyfilePth))
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("caddy validate: код %d: %s", res.ExitCode, trimErr(res))
	}
	if err != nil {
		var back []string
		for i := len(r.undo) - 1; i >= 0; i-- {
			u := r.undo[i]
			if u.existed {
				_ = r.FS.WriteFile(u.path, u.prev, u.mode)
			} else {
				_ = r.FS.Remove(u.path)
			}
			back = append(back, u.path)
		}
		r.undo = nil
		return outcome{}, fmt.Errorf("конфигурация Caddy не прошла проверку, файлы возвращены (%d): %w", len(back), err)
	}
	if _, err := r.must(ctx, hostCmd("systemctl", "reload", "caddy")); err != nil {
		return outcome{}, err
	}
	return outcome{changed: r.sitesChanged}, nil
}

// probeSite: Caddy должен отвечать на имя сайта (DEPLOY-PATTERN.md, «Caddy не подхватил имя»).
func probeSite(ctx context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.ProbeSite)
	if r.dry && r.sitesChanged {
		return outcome{changed: true, detail: "будет проверено после reload"}, nil
	}
	resp, err := r.HTTP.Get(ctx, fmt.Sprintf("http://127.0.0.1:%d/", p.ListenPort), p.Host)
	if err != nil {
		return outcome{}, fmt.Errorf("запрос к 127.0.0.1:%d: %w", p.ListenPort, err)
	}
	notDeployed := resp.Status == 503 && strings.Contains(string(resp.Body), "not deployed")
	switch p.Expect {
	case "not_deployed":
		if !notDeployed {
			return outcome{}, fmt.Errorf("ожидался ответ 503 not deployed, получен код %d: Caddy не подхватил имя %s", resp.Status, p.Host)
		}
	case "deployed":
		if notDeployed {
			return outcome{}, fmt.Errorf("сайт %s отвечает «not deployed», хотя проект уже выкатывали", p.Host)
		}
		if resp.Status == 200 && len(resp.Body) == 0 && strings.Contains(resp.Header.Get("Server"), "Caddy") && resp.Header.Get("Strict-Transport-Security") == "" {
			return outcome{}, fmt.Errorf("сервер Caddy отвечает пустым 200 без заголовков сайта: имя %s не подхвачено", p.Host)
		}
	}
	return outcome{}, nil
}

// installDeployKey: одна строка с меткой lite-deploy:<проект>, чужие строки не трогаются.
func installDeployKey(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.InstallDeployKey)
	dir := home(p.Project) + "/.ssh"
	mk, err := r.ensureDir(dir, 0o700, p.Project, p.Project)
	if err != nil {
		return outcome{}, err
	}
	path := dir + "/authorized_keys"
	line := strings.TrimRight(render.AuthorizedKey(*p), "\n")
	marker := " " + render.KeyMarker(p.Project)
	var keep []string
	if raw, err := r.FS.ReadFile(path); err == nil {
		for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			if l != "" && !strings.HasSuffix(strings.TrimSpace(l), marker) {
				keep = append(keep, l)
			}
		}
	}
	data := strings.Join(append(keep, line), "\n") + "\n"
	changed, bak, err := r.ensureFile(fileSpec{path: path, data: []byte(data), perm: 0o600, owner: p.Project, group: p.Project})
	if err != nil {
		return outcome{}, err
	}
	return outcome{changed: changed || mk, detail: bakNote(bak)}, nil
}

// recordProject пишет метку управления и несекретное состояние (D-49). `deployed` сохраняется из прежнего файла.
func recordProject(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.RecordProject)
	path := r.StateDir + "/projects/" + p.Project + ".json"
	st := plan.ProjectState{Hash: p.ProjectHash, Ports: p.Ports, Secrets: []string{}}
	if raw, err := r.FS.ReadFile(path); err == nil {
		var old plan.ProjectState
		if json.Unmarshal(raw, &old) == nil {
			st.Deployed = old.Deployed
		}
	}
	if ents, err := r.Secrets.List(p.Project); err == nil {
		for _, e := range ents {
			st.Secrets = append(st.Secrets, e.Name)
		}
	}
	for k := range r.wouldSet {
		if n, ok := strings.CutPrefix(k, p.Project+"/"); ok && r.dry && !contains(st.Secrets, n) {
			st.Secrets = append(st.Secrets, n)
		}
	}
	sort.Strings(st.Secrets)
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return outcome{}, err
	}
	changed := false
	if r.dry {
		cur, rerr := r.FS.ReadFile(path)
		changed = rerr != nil || !bytes.Equal(bytes.TrimSpace(cur), b)
		return outcome{changed: changed}, nil
	}
	if err := r.FS.MkdirAll(r.StateDir+"/projects", 0o755); err != nil {
		return outcome{}, err
	}
	changed, _, err = r.ensureFile(fileSpec{path: path, data: append(b, '\n'), perm: 0o644, owner: "root", group: "root"})
	if err != nil {
		return outcome{}, err
	}
	if len(r.ProjectJSON) > 0 {
		// Вызывающий (CLI) уже сверил хэш описания с project_hash плана.
		if err := r.FS.MkdirAll(ProjectsCopy, 0o755); err != nil {
			return outcome{}, err
		}
		c, _, err := r.ensureFile(fileSpec{path: ProjectsCopy + "/" + p.Project + ".json", data: r.ProjectJSON, perm: 0o644, owner: "root", group: "root"})
		if err != nil {
			return outcome{}, err
		}
		changed = changed || c
	}
	return outcome{changed: changed}, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
