package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/toxic-sunflower/lite-deploy/internal/firewall"
	"github.com/toxic-sunflower/lite-deploy/internal/host"
	"github.com/toxic-sunflower/lite-deploy/internal/project"
)

type ingressProtected struct {
	Paths []string `json:"paths"`
	Users []string `json:"users"`
}

type ingressSite struct {
	Project string `json:"project"`
	Site    string `json:"site"`
	Host    string `json:"host"`
	Service string `json:"service"`
	Publish string `json:"publish"`
	// Protected: the paths (empty: the whole site) and logins of the password asked for in direct mode; nil: none.
	Protected *ingressProtected `json:"protected,omitempty"`
}

// cmdIngress shows how traffic gets to the server: Caddy, the web ports and every domain with the way it is published.
func cmdIngress(env Env) result {
	const command = "ingress"
	ctx := context.Background()
	ok := func(name string, args ...string) bool {
		res, err := env.Runner.Run(ctx, host.Cmd{Name: name, Args: args})
		return err == nil && res.ExitCode == 0
	}
	caddy := map[string]bool{"installed": ok("caddy", "version"), "running": ok("systemctl", "is-active", "--quiet", "caddy")}
	var webOpen any
	if st, e := (&firewall.Manager{Runner: env.Runner}).Status(ctx); e == nil {
		webOpen = !st.Active || (openTo(st, 80) && openTo(st, 443))
	}
	sites := []ingressSite{}
	names := projectNames(env)
	sort.Strings(names)
	for _, name := range names {
		b, err := env.FS.ReadFile("/etc/lite-deploy/projects/" + name + ".json")
		if err != nil {
			continue
		}
		p, issues := project.Parse(b)
		if len(issues) > 0 {
			continue
		}
		for _, s := range p.Sites {
			mode := s.Publish
			if mode == "" {
				mode = "tunnel"
			}
			is := ingressSite{Project: name, Site: s.Name, Host: s.Host, Service: s.Service, Publish: mode}
			if fa := s.FallbackAuth; fa != nil && len(fa.Users) > 0 {
				is.Protected = &ingressProtected{Paths: fa.Paths}
				for _, u := range fa.Users {
					is.Protected.Users = append(is.Protected.Users, u.Name)
				}
			}
			sites = append(sites, is)
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Caddy: installed %v, running %v\n", caddy["installed"], caddy["running"])
	for _, s := range sites {
		fmt.Fprintf(&sb, "%s\t%s/%s\t%s\n", s.Host, s.Project, s.Site, s.Publish)
	}
	if len(sites) == 0 {
		sb.WriteString("No domains yet.\n")
	}
	return result{command: command, data: map[string]any{"caddy": caddy, "web_ports_open": webOpen, "sites": sites}, text: sb.String()}
}
