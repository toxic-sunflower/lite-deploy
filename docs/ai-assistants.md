# Use an AI assistant

`lite-ui` can also speak the Model Context Protocol (MCP), so an AI assistant on your computer can look at your servers and do the safe things: read the state, deploy and roll back. It is the same program you already installed ([Your first project](first-project.md)), started in a different mode: `lite-ui mcp`.

It uses your list of servers and your SSH key, over the same single connection as the page. It does **not** have a tool for changes that can lock you out or open the server (ports, SSH, backups, publishing, installing programs). Those stay in the page, where you can confirm them with your key ([Protect the server](protect-the-server.md)).

## Claude Desktop: install the extension (no files to edit)

Every release has one ready extension for all systems: [`lite-deploy.mcpb`](https://github.com/toxic-sunflower/lite-deploy/releases/latest/download/lite-deploy.mcpb). It does not carry a program of its own: it starts the `lite-ui` installed on your workstation, so it always matches the page you open and follows its updates; you install the extension once. If `lite-ui` is not installed yet, install it first (see the [Quickstart](quickstart.html)). Download the file, then in Claude Desktop open **Settings, Extensions, Install Extension…** (or double-click the file). It asks for your server as `user@address`.

The extension runs on your computer, so that computer needs: the OpenSSH client (it is in Windows 10 and later and on Macs), and a **key login to the server that works without a password** (`ssh user@address` in a terminal). On Windows the key must be in `%USERPROFILE%\.ssh`; a key that lives only inside WSL is not visible to it. If your `lite-ui` and key are in WSL, use the configuration below instead.

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
| `list_servers` | The servers you added |
| `verify` | The check of a server, read only |
| `project_status` | Projects, the active slot, versions, history |
| `ingress` | Domains and how each is published |
| `plugins` | What is installed and what can be added |
| `update_check` | Whether a newer lite exists |
| `deploy` | Deploy a version (full commit hash); traffic switches only when it is healthy |
| `rollback` | Back to the previous version |

A ready-made skill for assistants that support skills is in the repository: `skill/lite-deploy/SKILL.md`. The API the page and the tools talk to is described in [`schemas/agent.openapi.json`](https://github.com/toxic-sunflower/lite-deploy/blob/main/schemas/agent.openapi.json).
