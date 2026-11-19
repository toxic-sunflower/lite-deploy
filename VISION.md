# Vision: a separate project, "server setup and light deploy"

(written 2026-10-23 from the owner's words)

Server setup does not belong in the application repository. It is separate work and, later, a separate repository.

Three things are to be made out of what was walked through by hand (WALKTHROUGH.md):

1. **Instructions**: what is installed on an empty Ubuntu and why (with the commands explained).
2. **A skill for an agent**: copies a script to the server over SSH, runs it and sorts out errors. Only with the owner's explicit permission for every action.
3. **A small deployment system based on compose**: a light alternative to Kubernetes for one server with several projects, where nothing has to be done by hand. The basis: the pattern from DEPLOY-PATTERN.md (a separate user per project, a restricted SSH key with a gatekeeper, blue-green, Caddy on the host, PostgreSQL on the host, images from CI).

An open question: containers are currently described as Quadlet (systemd units), not as a compose file. It has to be decided whether Quadlet stays inside the system or projects are described by a compose file from which the system makes units itself.

The first thing to do after the vision: create a private git repository for this folder.

## What the tool must be able to do (from the example project decisions, 2026-10-24)

- **Installing the deploy files on the server in a manual-automatic way:** the project deploy script and the Quadlet units are installed by the tool on the owner's command (not sent by the pipeline: otherwise the deploy key would run any code). Modes: scripts 700, units 600.
- **A gatekeeper and a restricted key** for each project: `deploy <hash>`, `rollback`, `status`.
- **The `cloudflared` tunnel** as the way of publishing: the site listens on localhost only, name → local port in the tunnel settings, no direct entry by IP. Cloudflare Access for service names (admin panels).
- **A manual fallback plan** in case Cloudflare fails: a disabled Caddy file with a public certificate, the commands to turn it on and back, DNS without the proxy.
- **Project isolation** (the checklist in DEPLOY-PATTERN.md): a separate user, role and database per project, their own passwords, `pg_hba.conf` without `all`, resource limits, not turning off the Caddy API (`admin off` breaks reload).
- **Administrator access:** tunnel SSH keys (`restrict,port-forwarding,permitopen=...`) or Cloudflare Access.
- **Checks after installation:** a container's access to the local host services, a project role connecting to other databases, permissions on secret files, no secrets in `/etc/environment` and in history.
- The owner types secrets personally (`read -rs`); the tool does not print them and does not accept them in a chat.

## What the tool must be able to do (an addition of 2026-10-25, during the first deploy)

- **The tunnel service, safely:** a separate user without rights, the token in a closed file (`EnvironmentFile`, root, 600); do not use `cloudflared service install <token>` on a shared server. Before creating a route, check whether the name has an old DNS record and report it if so.
- **GitHub branches and environments through `gh api`:** environments restricted by branch, environment secrets (the owner types the values), per-environment switch variables (`DEPLOY_<ENVIRONMENT>_ENABLED`), the default branch. Keep in mind that branch protection is unavailable on the free plan for private repositories; warn the owner and record the decision.
- **Checking images before a deploy:** publishing each part and checking `/health/ready`; the `MSB3277` warning as a sign of a package version conflict.
- **Limits and maintenance:** tracking Actions minutes and GHCR space, cleaning up old images, a reminder that the GHCR token expires.
- **Database migrations:** a `migrate` mode of the image and a separate database role; until then the rule "migrations are backwards compatible" applies, with a description of the failure behaviour (traffic stays on the old version, applied migrations are not rolled back).
- **How to talk to the owner (for the skill):** short steps, one action at a time, every command explained, no allegories, do not ask for secrets in the chat; do not use the refusal wording "I won't", "I can't, because …" is acceptable.

## An addition of 2026-10-25 (evening)

- **Checking settings files after writing:** the set of keys and the length of passwords (per the application requirements), without printing values. A failure of the example project admin (the login line disappeared) was found only through the restart loop and a `502` from outside.
- **Slot state as part of the check:** after a deploy and after a settings change show `list-units` and the last journal lines of the slot; on a restart loop give the cause from the journal at once.
- **Changing a secret without leaks:** hidden input, the value through stdin, replacing one line while keeping the rest, restarting only the running slots, checking the keys.
- **Cloudflare Access:** creating an application and a policy from a list of emails, adding and removing admins; keep in mind that the interface changes, and check the result with a request from outside (the Access page instead of the application form).
- **Checking against the real third-party API (shop) before a deploy:** a mock hides differences (the format of variants, the answer to a repeated creation); the tool and the instructions must require a manual run of every new call at the live address.

## Principles and decisions (the discussion of 2026-10-29)

For now the project is a "fun tool", and the niche is being refined. The decisions below are not final, but further work rests on them.

### What we build

- **A light agent on the server** (Go or Rust, the language is not chosen). It works like a hosting panel without the panel: installs and configures software, creates a project user, role and database, hands out connection strings, writes env files and secrets, deploys and rolls back.
- **One management API of the agent, separate clients:** a CLI, later a web interface if wanted, an MCP server for AI agents. The clients are thin and not part of the core. We do not build a heavy panel.
- **The agent listens on nothing outward.** The management channel: a unix socket through SSH and/or a local interface published through a Cloudflare Tunnel under Cloudflare Access. CI uses a separate channel and can only `deploy`, `rollback`, `status`.
- **State and secrets only on the server, not in GitHub or GitLab.** An encrypted export (a backup) and restoring a server with one command are mandatory.

### Plugins

- Infrastructure: Caddy, Postgres, a container engine (Podman and Docker), Cloudflare Tunnel (the default publishing provider).
- **Postgres in two modes:** on the host (the default: more economical in memory, access over a unix socket, a role and database per project) and in a container.
- **Project description:** a single project model (services, ports, secrets, limits, healthcheck); from it the engine plugin generates compose or Quadlet. Both formats can be mixed in one project (a "how to run" field on a service). Quadlet is for Podman and systemd only.
- **A load balancer abstraction** using the Kubernetes vocabulary but without compatibility with it: `Route`, `Backend`, `Listener`. Caddy by default. We keep the abstraction small.

### Agent security (the main requirement)

- No arbitrary execution. Only a fixed set of typed commands with input validation (for example the hash `^[0-9a-f]{40}$`); strings are not passed to a shell.
- Minimal rights: the root part is narrow, project operations run as the project user.
- Rights and confirmations live in the agent API, not in MCP: dangerous actions (delete a database, show a secret) need the owner's confirmation outside the AI channel, for example through the CLI on their machine. The agent keeps the action journal.
- Installing and updating the agent and plugins only on the owner's command, with a signature check.
- The owner types secrets personally; they do not enter a chat and are not printed.

### Templates and a catalog

- We do not build a catalog of ready applications. We provide a **template format**; the community can make a catalog if the project takes root.
- A template is strictly declarative (a manifest: image, ports, volumes, variables, whether a database is needed), without scripts. The agent turns the manifest into units itself. Before installing, the owner is shown what will be created. Templates should preferably be signed.

### Limits

- One server for now. Several servers are possible later. If automatic load migration is ever needed, the right way is k3s, not our agent.
- Not to repeat: a big catalog of services, a heavy panel, teams, roles, SSO.

### What the comparison with alternatives showed

Details are in `COMPARISON-PLAN.md` (the plan) and in the discussion: Coolify, Dokploy and Kamal. In short:
- Coolify already has an official MCP, so "managing a server through AI" is not a difference by itself.
- Their central component enters the host as root and holds SSH keys (Coolify itself writes that breaking into the panel opens all servers). Our line: a narrow agent without root, rootless containers, tunnel only, Postgres and Caddy on the host.
- Not checked: the Dokploy AI Assistant and API, Dokku, CapRover, Podman in the full documentation text, real memory use.

### The next step

A small prototype of the agent with the command `project create` (a user, a role and database, an env file with the connection string), then `deploy <hash>` with a health check and a rollback.

## Name, license, goal (decided 2026-11-03)

The working name is `lite-deploy` (it can change), the license is MIT. An open tool for myself and for other VDS owners like me, with no obligations on deadlines or support. The priority: AI (MCP) and UI. All decisions with their reasons: `DECISIONS.md`, the scheme: `ARCHITECTURE.md`, the session queue: `PLAN.md`.
