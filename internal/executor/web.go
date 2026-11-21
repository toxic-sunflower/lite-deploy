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
	handlers[actions.InstallUnitName] = installUnit
	handlers[actions.ReloadUserUnitsName] = reloadUserUnits
	handlers[actions.InstallCaddySiteName] = installCaddySite
	handlers[actions.InstallReloadRuleName] = installReloadRule
	handlers[actions.ReloadCaddyName] = reloadCaddy
	handlers[actions.ProbeSiteName] = probeSite
	handlers[actions.InstallDeployKeyName] = installDeployKey
	handlers[actions.RecordProjectName] = recordProject
}

// ensureDataDirs creates the persistent directories of a component (shared by the slots).
func (r *run) ensureDataDirs(project string, names []string) (bool, error) {
	changed := false
	for _, n := range names {
		made, err := r.ensureDir(home(project)+"/"+render.DataDir(n), 0o750, project, project)
		if err != nil {
			return changed, err
		}
		changed = changed || made
	}
	return changed, nil
}

func installQuadlet(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.InstallQuadlet)
	if _, err := r.ensureDataDirs(p.Project, p.Data); err != nil {
		return outcome{}, err
	}
	path := home(p.Project) + "/.config/containers/systemd/" + render.UnitName(p.Project, p.Service, p.Slot) + ".container"
	text := render.Quadlet(*p)
	changed, bak, err := r.ensureFile(fileSpec{path: path, data: []byte(text), perm: 0o600, owner: p.Project, group: p.Project})
	if err != nil {
		return outcome{}, err
	}
	if changed {
		r.unitsChanged = true
	}
	return outcome{changed: changed, detail: bakNote(bak), path: path, content: text}, nil
}

func installUnit(_ context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.InstallUnit)
	if _, err := r.ensureDataDirs(p.Project, p.Data); err != nil {
		return outcome{}, err
	}
	path := home(p.Project) + "/.config/systemd/user/" + render.UnitName(p.Project, p.Service, p.Slot) + ".service"
	text := render.Unit(*p)
	changed, bak, err := r.ensureFile(fileSpec{path: path, data: []byte(text), perm: 0o600, owner: p.Project, group: p.Project})
	if err != nil {
		return outcome{}, err
	}
	if changed {
		r.unitsChanged = true
	}
	return outcome{changed: changed, detail: bakNote(bak), path: path, content: text}, nil
}

// reloadUserUnits: daemon-reload in the user session, then a check that the units are visible.
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
		return outcome{}, fmt.Errorf("daemon-reload: code %d: %s", res.ExitCode, trimErr(res))
	}
	ok, missing, err := visible()
	if err != nil {
		return outcome{}, err
	}
	if !ok {
		return outcome{}, fmt.Errorf("after daemon-reload the units are not visible: %s", missing)
	}
	return outcome{changed: true}, nil
}

func hostLineRe(host string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*(https?://)?` + regexp.QuoteMeta(host) + `(:[0-9]+)?\s*[,{]`)
}

// installCaddySite (SPEC-v0.md, 4.3): the site file, the upstream, the log directory. It does not reload Caddy.
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

	// Old files with the same site address (a manual setup under adopt) are replaced, otherwise two sites would share one address.
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
					return outcome{}, fmt.Errorf("address %s is already used by project %s (file %s)", p.Host, mp, n)
				}
			}
			notes = append(notes, "replaced the old file "+n)
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
		}
	}
	if changedAny && !changed {
		r.sitesChanged = r.sitesChanged || len(notes) > 1
	}
	if r.dry && changedAny {
		r.sitesChanged = true
	}
	return outcome{changed: changedAny, detail: joinDetail(notes...), path: site, content: string(data)}, nil
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

// installReloadRule: sudoers is written to a temporary file (sudo ignores names with a dot), checked with visudo -cf, then installed.
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

// reloadCaddy: check the configuration as user caddy, then reload. On a check error the site files
// of this apply are returned to the previous state. A reload is harmless, so in a real run it always happens
// (otherwise an interrupted apply would leave Caddy without the new files); `changed` only if the sites changed.
func reloadCaddy(ctx context.Context, r *run, _ any) (outcome, error) {
	if r.dry {
		return outcome{changed: r.sitesChanged}, nil
	}
	res, err := r.Runner.Run(ctx, hostCmd("runuser", "-u", "caddy", "--", "caddy", "validate", "--config", CaddyfilePth))
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("caddy validate: code %d: %s", res.ExitCode, trimErr(res))
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
		return outcome{}, fmt.Errorf("the Caddy configuration failed validation, the files were restored (%d): %w", len(back), err)
	}
	if _, err := r.must(ctx, hostCmd("systemctl", "reload", "caddy")); err != nil {
		return outcome{}, err
	}
	return outcome{changed: r.sitesChanged}, nil
}

// probeSite: Caddy must answer for the site name (DEPLOY-PATTERN.md, "Caddy did not pick up the name").
func probeSite(ctx context.Context, r *run, v any) (outcome, error) {
	p := v.(*actions.ProbeSite)
	if r.dry && r.sitesChanged {
		return outcome{changed: true, detail: "will be checked after the reload"}, nil
	}
	resp, err := r.HTTP.Get(ctx, fmt.Sprintf("http://127.0.0.1:%d/", p.ListenPort), p.Host)
	if err != nil {
		return outcome{}, fmt.Errorf("request to 127.0.0.1:%d: %w", p.ListenPort, err)
	}
	notDeployed := resp.Status == 503 && strings.Contains(string(resp.Body), "not deployed")
	switch p.Expect {
	case "not_deployed":
		if !notDeployed {
			return outcome{}, fmt.Errorf("expected the answer 503 not deployed, got code %d: Caddy did not pick up the name %s", resp.Status, p.Host)
		}
	case "deployed":
		if notDeployed {
			return outcome{}, fmt.Errorf("site %s answers \"not deployed\" although the project has been deployed", p.Host)
		}
		if resp.Status == 200 && len(resp.Body) == 0 && strings.Contains(resp.Header.Get("Server"), "Caddy") && resp.Header.Get("Strict-Transport-Security") == "" {
			return outcome{}, fmt.Errorf("the Caddy server answers an empty 200 without the site headers: the name %s was not picked up", p.Host)
		}
	}
	return outcome{}, nil
}

// installDeployKey: one line with the mark lite-deploy:<project>, other lines are not touched.
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

// recordProject writes the management mark and the non-secret state (D-49). `deployed` is kept from the previous file.
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
		// The caller (the CLI) has already checked the description hash against the plan project_hash.
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
