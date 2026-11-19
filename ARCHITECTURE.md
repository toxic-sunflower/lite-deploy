# How it works: lite-deploy (a draft, 2026-11-03)

The name `lite-deploy` is a working one. The license is MIT. All decisions taken, with numbers: `DECISIONS.md`.

This file collects decisions from the discussions. The principles and limits of the project: `VISION.md`. What is not decided is listed at the end.

## 1. What it is in one sentence

A small agent runs on the server. Through it the owner, CI and an AI helper set up the server and deploy projects without getting full access to the server.

## 2. What it consists of

1. **The agent.** A systemd service that listens only on a local socket (it opens nothing to the internet). It accepts requests, checks who asks and whether they may, builds a plan, applies it and writes a journal.
2. **The executor.** A small privileged part (root) that **does not hang in the background**: it has its own socket `/run/lite-deploy/exec.sock` (available only to the agent user) owned by systemd; a request arrives, systemd starts the executor, it performs the step and exits. It accepts only typed requests from a fixed list (for example `{"action":"create_project_user","name":"shop"}`), validates the parameters itself and runs the given commands without a shell. It is restricted by systemd (a set of capabilities and directories). It writes a journal to a file the agent cannot change. In mode A the same executor is started by hand: `sudo lite-deploy apply plan.json`. Decision D-31.
3. **Plugins.** Each is responsible for one part: `base`, `postgres`, `caddy`, `podman`, `docker`, `cloudflare-tunnel`, secret stores, confirmation methods. A plugin does not change the system itself but returns a **plan**.
4. **The project description.** A small JSON file with a schema (JSON Schema): the name, the domain, the image, the database, limits, references to secrets. A plan is built from it (JSON too, with a version and a hash). UI forms and MCP tool descriptions come from the schema.
5. **State on the server.** Projects, settings, the journal. Secrets in the project files (mode 600). None of this is in GitHub or GitLab, so an encrypted backup and a restore are needed.
6. **Clients.** The CLI, the UI, MCP for AI. Thin; the agent decides everything.

The language: **Go** (tentatively confirmed). No `unsafe` and no `cgo`, tests with `-race`, automatic service restart. The executor can be rewritten in Rust later if wanted.

## 3. Modes of work

Both modes are equal and are chosen per server. The code and the plugins are the same.

- **Mode A: "built, applied by hand".** A plan is built through the CLI or the UI. You log in over SSH and apply it with one command as root. There is no permanent program with root on the server.
- **Mode B: "the agent does it itself".** The agent works all the time and applies changes without you, within the executor's list of actions. Dangerous actions need confirmation.

## 4. Security

| Level | How it works | Security | Convenience |
|---|---|---|---|
| 0 | Everything by hand | the maximum | none |
| 1 | Mode A: the owner applies the root actions | very high | medium |
| 2 | Mode B: an agent without rights + an executor with root and a fixed list of actions | high | high |
| 3 | One program with root does everything (competitors) | weak | high |

The difference from competitors: whoever breaks into their panel gets any command as root. With us someone who breaks into the agent can ask for only what is in the executor list, and it is visible in the plan. The residual risk: a bug in the executor, which is why it is small and does not parse complex input.

Who may do what:

| Who | How | What is allowed |
|---|---|---|
| The owner | SSH, CLI, UI | Everything |
| CI | A restricted SSH key | `deploy <hash>`, `rollback`, `status` of one project |
| An AI helper | MCP, a restricted token | Safe actions; dangerous ones wait for confirmation |
| A project user | Locally | Only their own project |

### The threat model (in short, D-30)

- **We protect against:** a hacked project (container), a stolen CI key, a harmful or hacked plugin, a deceived AI client (prompt injection), a hacked orchestrator or SaaS, a hacked agent, a hacked executor, a leaked repository.
- **We do not protect against:** a break-in to the host itself with root, a malicious hoster, physical access, a kernel vulnerability.
- **If the agent is hacked (it has no root):** the attacker can do only what the agent does without confirmation (status, deploy, rollback, creating a project). They cannot confirm dangerous actions: the signature is made with a private key that is not on the server. Arbitrary commands as root and other people's secrets are out of reach. The agent does not store secret values (the client encrypts them with the executor key and the agent passes the ciphertext), it cannot change the journal, and the number of actions is limited by quotas.
- **Action classes (D-29):** safe (status, logs), ordinary (`deploy`, `rollback`, changing a secret value, creating a project), dangerous (deleting a project, a database or a route; changing trusted keys and the confirmation method; installing and updating the agent and plugins; reading a secret as root) need a key signature.

## 5. Secrets

- The project config holds references (`secret://name/path`), not values.
- At deploy time the agent receives the value and writes a file with mode 600 for the project user. Passing it into the container is through `EnvironmentFile` or Podman secrets, not through command arguments.
- **Secrets are write-only**, as in GitHub: you can set, replace and use them, but there is no "show the value" command. The UI, the CLI and MCP only report that a secret is set and when it changed. It can be read only on the server as root, and that is written to the journal.
- Store plugins: local files on the server (the default, enough for one server), Vault or OpenBao, Infisical, Doppler, cloud managers. The store token is kept as a 600 secret with minimal read rights.

## 6. Confirming dangerous actions

Needed in mode B. The confirmation methods are plugins, and the strictness is set per action type (for example deleting a database needs confirmation, an ordinary deploy does not).

| Method | Reliability |
|---|---|
| **A key signature** (the default) | The strongest: only the public key is on the server |
| An interactive SSH session | Good, simple |
| Cloudflare Access (web, two-factor check) | Good, convenient from a phone |
| An OTP code | Medium: the shared secret lies on the server |
| A messenger message | Weaker, we trust the channel |

A key signature: the owner has a private key (SSH, YubiKey, passkey), the agent has the public one. The agent creates a request ("delete database `shop`, no. 17, 5 minutes, number N"), the client signs exactly this text, the agent checks the signature. There is no secret on the server that could be stolen, and substituting an action or a replay is impossible.

## 7. Plugins

The main rule: **a plugin never changes the system itself, it returns a plan**, and only the executor carries it out within its actions. A harmful plugin can ask for only what the executor can do, and the owner sees it in the plan.

| Category | What it is | How it works |
|---|---|---|
| Templates | Data | Nothing is executed |
| Providers (secrets, confirmations, notifications) | Code with a narrow task | A sandbox without root, only the allowed network and files |
| Infrastructure (Caddy, Postgres, engines) | They issue a plan | The executor executes it |

The order of introduction:
1. **The first version:** all plugins are built into the binary but already return a plan.
2. **The second:** a sandbox for community plugins, tentatively WASM (in Go without C code, wazero). A plugin has a permission manifest ("I want to talk to vault.example.com and write to `/etc/caddy/conf.d/`"), and the owner sees and agrees. Plugin signing and trust levels: official, verified, unverified (explicit confirmation).

Application templates: a declarative manifest without scripts. The catalog is made by the community.

(Since v0.6 the built-in plugins also run commands themselves through the executor's fixed list of actions and through `host.Runner`; see `README.md`, "Plugins".)

## 8. UI and orchestrator

- **The first UI:** local, started on the owner's computer with a command like `ctl ui` on `127.0.0.1`, reaching the agents over SSH. The list of servers is kept by the owner; the scheme is the same for one and for several servers.
- **The second:** a UI on the server through a Cloudflare tunnel under Access, for access from a phone or someone else's computer.
- **The orchestrator** (optional, later): knows the desired state of projects and servers and hands it to the agents. It is needed for teamwork and a permanently running center. It has a UI too. It can work as a **SaaS** or be deployed by yourself. The idea and the limits:
  - the agents connect to it outward themselves, the server has no incoming ports;
  - it cannot confirm dangerous actions: a key signature from the owner's device is needed;
  - secret values cannot be read through it; when a secret is set, the value is encrypted in the browser with the agent's public key and the orchestrator passes only ciphertext;
  - compromising the orchestrator gives no more than is allowed in the agent API without confirmation.

## 9. What is decided

- A light agent, one API, separate clients, no heavy panel.
- Two modes A and B, equal.
- Go.
- Secrets: references in the config, write-only, local files by default, plugins for external stores.
- Confirmation: a key signature by default, other methods configurable.
- Plugins return a plan, the executor executes; built-in first, a sandbox for the community later.
- Plugins: Caddy, Postgres (on the host by default or in a container), Podman (Docker is forbidden, R-11), Cloudflare Tunnel.
- A project can be described through compose or Quadlet, both can be mixed in one project (since v0.6: `quadlet` and `unit` kinds, compose through `unit`).
- The load balancer: `Route`, `Backend`, `Listener`, Caddy by default.
- No catalog of services, a template format is provided for.
- One server for now, several later.
- The name `lite-deploy`, the license MIT, the goal: an open tool with no obligations on deadlines.
- The first version: mode A, `project create`, `deploy`, `rollback`, `status`, the CLI with JSON as the base API, thin UI and MCP over SSH (D-28).
- The agent API: HTTP + JSON over a unix socket, `/v1`, OpenAPI (D-32). The format of a project and a plan: JSON + JSON Schema (D-33, D-34).
- The project priority: AI (MCP) and the UI; dry-run for everything, confirmations in the agent (D-37).
- The orchestrator and SaaS are postponed until v1 (D-35).

## 10. What is not decided

The formats and actions of v0 are described in `SPEC-v0.md` (session S2), the pure logic was implemented in S3 (the code is in `internal/`). What remains:

1. A way to run commands in the session of a project user for rootless podman (checked in S4: `systemd-run --machine=USER@.host --user` works).
2. The format of the application template manifest (after v0).
4. Signing confirmations, the list of trusted keys, moving the Caddy switch to the executor socket (S6).
5. The orchestrator and SaaS: everything related to them comes after v1.

## 11. Glossary

- **A daemon, a service** — a program that works in the background all the time.
- **A socket** — a "file socket" on the server for programs to talk to each other. Not reachable from the internet.
- **Plan and apply** — first it is shown what will change, then the change, then a check.
- **A declarative config** — a description of what must be, not a sequence of commands.
- **MCP** — a way to connect an AI helper to a tool.
- **WASM** — a format of safe plugins that run in a sandbox without access to the system.
