package cli

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/toxic-sunflower/lite-deploy/internal/deploy"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
)

const publishUsage = "usage: lite-deploy publish <project> <site> [tunnel|direct] [--yes]"

// cmdPublish shows or switches how a site reaches the internet: through a tunnel (Caddy on 127.0.0.1 only) or directly (Caddy serves 80/443 with its own certificate).
// It changes the stored project description and applies it like `project create`.
func cmdPublish(args []string, env Env) result {
	const command = "publish"
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) < 2 || len(pos) > 3 {
		return fail(command, CodeUsage, publishUsage)
	}
	name, siteName := pos[0], pos[1]
	doc, sites, idx, bad := loadSite(command, env, name, siteName)
	if bad != nil {
		return *bad
	}
	cur := "tunnel"
	if v, ok := sites[idx]["publish"]; ok {
		_ = json.Unmarshal(v, &cur)
	}
	if len(pos) == 2 {
		return result{command: command, data: map[string]any{"project": name, "site": siteName, "publish": cur},
			text: fmt.Sprintf("Site %s of %s: %s\n", siteName, name, cur)}
	}
	mode := pos[2]
	if mode != "tunnel" && mode != "direct" {
		return fail(command, CodeUsage, publishUsage)
	}
	if mode == cur {
		return result{command: command, data: map[string]any{"project": name, "site": siteName, "publish": cur, "changed": false},
			text: fmt.Sprintf("Site %s of %s is already %s.\n", siteName, name, cur)}
	}
	sites[idx]["publish"], _ = json.Marshal(mode)
	r := applySiteChange(command, env, doc, sites, *yes)
	if r.err == nil {
		r.text += publishNote(mode)
	}
	return r
}

func publishNote(mode string) string {
	if mode == "direct" {
		return "Caddy now serves the site on ports 80 and 443 and gets the TLS certificate itself. Point the DNS A/AAAA record at this server and open the ports: lite firewall mode http.\n"
	}
	return "The site is served on 127.0.0.1 only. Route the host through the Cloudflare Tunnel; ports 80 and 443 can be closed: lite firewall mode max.\n"
}

// loadSite reads the stored description of a project and finds one site in it.
func loadSite(command string, env Env, name, siteName string) (map[string]json.RawMessage, []map[string]json.RawMessage, int, *result) {
	raw, err := env.FS.ReadFile(deploy.DefaultProjectsDir + "/" + name + ".json")
	if err != nil {
		r := fail(command, CodeIO, "project "+name+" is not managed by lite-deploy on this server")
		return nil, nil, 0, &r
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		r := fail(command, CodeIO, "the stored description of "+name+" is not valid JSON")
		return nil, nil, 0, &r
	}
	var sites []map[string]json.RawMessage
	if err := json.Unmarshal(doc["sites"], &sites); err != nil {
		r := fail(command, CodeIO, "the stored description of "+name+" has no sites")
		return nil, nil, 0, &r
	}
	for i, s := range sites {
		var n string
		_ = json.Unmarshal(s["name"], &n)
		if n == siteName {
			return doc, sites, i, nil
		}
	}
	r := fail(command, CodeUsage, "project "+name+" has no site "+siteName)
	return nil, nil, 0, &r
}

// applySiteChange stores the changed description and applies it like `project create`.
func applySiteChange(command string, env Env, doc map[string]json.RawMessage, sites []map[string]json.RawMessage, yes bool) result {
	doc["sites"], _ = json.Marshal(sites)
	data, _ := json.MarshalIndent(doc, "", "  ")
	if !env.root() {
		return forwardProjectData(env, command, data, yes, false, nil)
	}
	p, issues := project.Parse(data)
	if len(issues) == 0 {
		issues = project.Validate(p)
	}
	if len(issues) > 0 {
		return fail(command, CodeUsage, "the changed description is invalid: "+issues[0].Message)
	}
	pr, bad := buildPlan(command, p, "", false, nil, env)
	if bad != nil {
		return *bad
	}
	planBytes, e := json.Marshal(pr.pl)
	if e != nil {
		return fail(command, CodeInternal, e.Error())
	}
	return applyPlan(command, env, planBytes, false, yes, data)
}
