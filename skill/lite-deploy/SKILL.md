---
name: lite-deploy
description: Look at and operate servers managed by lite-deploy (state of projects, checks, deploys, rollbacks, publishing, backups) through the lite-ui MCP tools. Use when the person asks about their server, a project's version, whether something is healthy, or wants to deploy or roll back.
---

# Working with lite-deploy

lite-deploy sets up a Linux server and deploys projects to it. You reach the person's servers through the MCP server `lite-deploy` (`lite-ui mcp` on their computer). It uses their list of servers and their SSH key; you never see a password or a key.

## Tools

- `list_servers`: the servers (user@address). Call it first; if there is one, the others use it by default.
- `verify`: a read-only check. Findings have a level `ok`, `warn` or `fail`. Explain them in plain words; do not paste raw command lines at a person who is not technical.
- `project_status`: projects, the active slot (blue or green), whether each service answers, the version, the history.
- `ingress`: Caddy, the web ports and every domain with its way of publishing (`tunnel` through a Cloudflare Tunnel, or `direct`).
- `plugins`: what is installed on the server and what can be added.
- `update_check`: whether a newer lite exists (installing it is done by the person in the interface).
- `deploy` and `rollback`: change what runs.

## Rules

1. **Look before you act.** Run `project_status` (and `verify` when something is wrong) before suggesting a change.
2. **Ask before `deploy` and `rollback`**, and say which project, which version (a deploy needs the full 40-character commit hash) and what will happen: traffic switches to the new version only after it answers its health check, and stays on the old one if it does not; a rollback switches back to the previous version.
3. **Never invent a commit hash.** If the person did not give one, ask, or read it from the history that `project_status` returns.
4. **Dangerous changes are not yours.** Opening ports, SSH settings, backups, publishing, installing programs and similar changes are made by the person in the lite-ui page, which can confirm them with their key. You have no tool for them on purpose. Say where to click instead.
5. **Secrets.** Never ask for, print or store passwords, tokens or keys. If a task needs one, tell the person to enter it in the page.
6. If a tool returns an error, show its message and hint to the person in plain words; do not retry blindly. An error about the connection means the person should check that `ssh user@address` works in a terminal.

## Typical requests

- “Is my server fine?” → `verify`, then explain the warnings that matter.
- “What version is live?” → `project_status`.
- “Deploy abc123… to shop” → confirm, then `deploy`; afterwards `project_status` to show the result.
- “Something broke after the deploy” → `project_status`, then offer `rollback` and ask.
