# Use an AI assistant

`lite-ui` can also speak the Model Context Protocol (MCP), so an AI assistant on your workstation can look at your servers and do what the page does: read the state, deploy, create projects, set secrets, publish sites, manage the firewall, backups and Cloudflare. It is the same program you already installed ([Your first project](first-project.md)), started in a different mode: `lite-ui mcp`.

It uses your list of servers and your SSH key, over the same single connection as the page. **Claude asks you before every change** (the tools that only read are marked as such; Claude Desktop shows a permission question for the others). It never signs with your owner key: if you turned on “Confirm with my key” ([Protect the server](protect-the-server.md)), changes that need your key can only be made in the page, and Claude tells you so.

## Claude Desktop: install the extension (no files to edit)

Every release has one ready extension for all systems: [`lite-deploy.mcpb`](https://github.com/toxic-sunflower/lite-deploy/releases/latest/download/lite-deploy.mcpb). It does not carry a program of its own: it starts the `lite-ui` installed on your workstation, so it always matches the page you open and follows its updates; you install the extension once. On Windows the extension also finds a `lite-ui` installed only inside WSL and runs it through `wsl.exe` (it then uses the SSH keys of WSL). If `lite-ui` is not installed yet, install it first (see the [Quickstart](quickstart.html)). Download the file, then in Claude Desktop open **Settings, Extensions, Install Extension…** (or double-click the file). The server field is optional: leave it empty to use the servers of `lite-ui`, or just ask Claude to “add the server user@address”. Several servers work side by side; say which one you mean.

The extension runs on your workstation, so that workstation needs: the OpenSSH client (it is in Windows 10 and later and on Macs), and a **key login to the server that works without a password** (`ssh user@address` in a terminal). On Windows the key must be in `%USERPROFILE%\.ssh`; a key that lives only inside WSL is not visible to it. If your `lite-ui` and key are in WSL, use the configuration below instead.

## Connect it by hand

**Claude Code**

```sh
claude mcp add lite-deploy -- lite-ui mcp
```

**Claude Desktop** (and other assistants that read a JSON configuration):

```json
{ "mcpServers": { "lite-deploy": { "command": "lite-ui", "args": ["mcp"] } } }
```

Add your servers once in the page (`lite-ui`, “Add”). Then ask, for example: “Is my server fine?”, “What version of shop is live?”, “Deploy 3f2c… to shop”.

## What it can do

| Tool | What it does |
| --- | --- |
| `list_servers`, `add_server`, `remove_server` | The servers of your list, and changing the list |
| `verify`, `fix` | The check of a server, and fixing what is safe to fix |
| `project_status`, `project_export` | Projects, slots, versions, history; a project description without secrets |
| `project_preview`, `project_create` | What creating or changing a project would do, and doing it |
| `deploy`, `rollback` | A version (full commit hash) with traffic switched only when healthy; back to the previous one |
| `secrets_list`, `secret_set`, `secret_remove` | Names of secrets (never values), setting and deleting one |
| `ingress`, `publish`, `fallback_password` | Domains, tunnel or direct publishing, the password of direct mode |
| `connect_cloudflare_tunnel`, `cloudflare_access` | Tunnels, and closing a domain with Cloudflare Access |
| `firewall_status`, `plugins`, `run_plugin` | The firewall; the plugins; any command of a plugin (firewall, security, backup, pg, caddy…) |
| `server_config`, `update_check`, `update_server` | The server as a file; whether a newer lite exists; installing it |

Secrets, passwords and tokens given to Claude pass through the chat: for those prefer typing them in the page. Making the backup key is only in the page (the file is saved on your workstation).

A ready-made skill for assistants that support skills is in the repository: `skill/lite-deploy/SKILL.md`. The API the page and the tools talk to is described in [`schemas/agent.openapi.json`](https://github.com/toxic-sunflower/lite-deploy/blob/main/schemas/agent.openapi.json).
