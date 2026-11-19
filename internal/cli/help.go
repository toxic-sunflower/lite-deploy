package cli

import (
	"fmt"
	"strings"
)

// helpText is the general help: what lite is, the core commands and how to find the rest.
const helpText = `lite %s: set up a Linux server and deploy projects to it.

Usage: lite <command> [arguments] [--json]

First setup (the only command that runs as root):
  init --user NAME [--ufw keep|off|http|max] [--plugins LIST] [--yes]

Everything else runs as a regular administrator, no sudo needed:
  verify                       check the server (read-only)
  fix [--dry-run]              apply the safe fixes that verify suggests
  info                         plugins and their commands
  project create FILE ...      create or update a project (shows a preview first)
  project plan|validate|list   build a plan, check a description, list projects
  deploy PROJECT HASH          deploy a version (blue-green, no downtime)
  rollback PROJECT             switch traffic back to the previous version
  status [PROJECT]             show projects and slots
  secret encrypt PROJECT NAME  encrypt a secret value read from stdin
  ui                           start the web interface
  version                      show the version

Plugins add their own commands: lite PLUGIN COMMAND [arguments]
  Run "lite info" to see them all (pg, redis, caddy, podman, firewall, security,
  backup, backblaze, s3, sftp, server, package).

Add --json to any command for machine-readable output.
Documentation: https://lite-deploy.prozorov.me
`

func cmdHelp(env Env) result {
	return result{command: "help", data: map[string]any{"version": Version}, text: fmt.Sprintf(helpText, Version)}
}

// isHelp reports whether args ask for help; help works for everyone, including root.
func isHelp(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "help", "--help", "-h":
		return true
	}
	return len(args) == 2 && (args[1] == "--help" || args[1] == "-h") && !strings.HasPrefix(args[0], "-")
}
