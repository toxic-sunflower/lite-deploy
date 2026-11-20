package cli

import (
	"github.com/toxic-sunflower/lite-deploy/internal/serverconfig"
	"github.com/toxic-sunflower/lite-deploy/internal/setup"
)

// applyServerConfig puts a server configuration into the options of init. A flag given on the command line wins over the file.
func applyServerConfig(o setup.Options, c *serverconfig.Config, given map[string]bool, noSecurityFlag bool) setup.Options {
	if !given["user"] && !given["all-users"] && len(c.Users) > 0 {
		o.Users = nil
		for _, u := range c.Users {
			o.Users = append(o.Users, u.Name)
		}
	}
	o.Keys = map[string][]string{}
	for _, u := range c.Users {
		if len(u.SSHKeys) > 0 {
			o.Keys[u.Name] = u.SSHKeys
		}
	}
	if !given["ufw"] && c.Firewall != "" {
		o.Firewall = c.Firewall
	}
	if !given["plugins"] {
		o.Plugins = append([]string{}, c.Plugins...)
	}
	skip := noSecurityFlag
	if !given["no-security"] {
		skip = !c.SecurityOn()
	}
	o.Plugins = withSecurity(without(o.Plugins, "security"), skip)
	return o
}

func without(list []string, drop string) []string {
	var out []string
	for _, x := range list {
		if x != drop {
			out = append(out, x)
		}
	}
	return out
}
