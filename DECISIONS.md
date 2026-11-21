# Decision log

In brief: what was decided, why, the status. Details and schemes: `ARCHITECTURE.md`, principles: `VISION.md`. Dates: 2026-10-23 .. 2026-11-20. Status: **accepted** (the owner agreed) or **tentative** (accepted in principle, details not decided).

## The idea and the niche

**D-01. The project is separate from the application.** Server setup and deploy live in their own repository. Status: accepted (2026-10-23).

**D-02. For now the project is a "fun tool", the niche is being refined.** Not a startup, no need to prove demand in advance. Status: accepted.

**D-03. We do not compete with Coolify and Dokploy on a service catalog and a heavy panel.** The reason: they need 2 GB or more for the panel, root access over SSH, a big catalog. Our place: security by design, lightness, secrets and infrastructure description only on the server, management of databases and connection strings, AI as a client. Status: accepted.

**D-04. We aim at one owner with one server.** Several servers later. If automatic load migration is ever needed, the right way is k3s, not our agent. Kubernetes compatibility is not needed. Status: accepted.

## The agent and security

**D-05. The central part: a light agent on the server (a service), without a built-in interface.** All interfaces are separate clients of one API. Status: accepted.

**D-06. The agent listens on nothing outward.** Only a local socket. CI uses a separate channel (a restricted SSH key + the gatekeeper) and can only `deploy <hash>`, `rollback`, `status` of one project. Status: accepted.

**D-07. Two equal modes.** Mode A: the plan is built by the client and the owner applies it over SSH with one command as root (there is no permanent program with root). Mode B: the agent works all the time and applies changes itself. The choice is per server. Status: accepted.

**D-08. In mode B an unprivileged agent + a small privileged executor.** The executor can do only a fixed list of actions and does not run arbitrary commands. The difference from competitors: for them a break into the panel gives any command as root. The scheme and the design: D-31. Status: accepted.

**D-09. State and secrets only on the server, not in GitHub and GitLab.** An encrypted backup and a restore with one command are mandatory. Status: accepted.

**D-10. Secrets.** The config has references (`secret://...`), not values. Secrets are write-only, as in GitHub: there is no "show" command. By default local files with mode 600 owned by the project user (enough for one server). Plugins for external stores (Vault and OpenBao, Infisical, Doppler, cloud managers). Passing into a container through a file, not through arguments. Status: accepted.

**D-11. Confirming dangerous actions.** A signature with the owner's key (SSH, YubiKey, passkey) by default. Other methods are configurable: an interactive SSH session, Cloudflare Access, OTP, a messenger. The strictness per action type. Which actions are dangerous: D-29. Status: accepted.

**D-12. Project isolation.** A separate user without a password and sudo per project, rootless containers, a Postgres role and database per project, `pg_hba.conf` without `all`, resource limits, secrets that do not intersect. The checklist is in `DEPLOY-PATTERN.md`. Status: accepted.

## Plugins and infrastructure

**D-13. The plugin architecture.** A plugin does not change the system itself but returns a plan; only the executor executes. The categories: templates (data), providers (secrets, confirmations, notifications; a sandbox without root), infrastructure (Caddy, Postgres, engines; a plan). Status: accepted. (Since v0.6 built-in plugins run their own commands through `host.Runner`, and external ones are directories with a manifest, R-19 and R-25 in `REQUIREMENTS.md`.)

**D-14. The order of introducing plugins.** Version 1: all plugins are built into the binary. Version 2: a sandbox for community plugins (tentatively WASM, a permission manifest, a signature, trust levels). Status: accepted.

**D-15. The default set of plugins.** Caddy, Postgres, Podman, Cloudflare Tunnel (the default publishing provider), `base` (server protection). Status: accepted. Cloudflare as a whole: D-85. (Docker was dropped from the set by R-11, 2026-11-19.)

**D-16. Describing containers: compose and Quadlet, can be mixed in one project.** Quadlet is for Podman and systemd only. A single project model; the engine plugin generates the needed format from it. Status: accepted.

**D-17. The load balancer: the abstraction `Route`, `Backend`, `Listener` using the Kubernetes vocabulary, without compatibility with it.** Caddy by default. Keep the abstraction small. Status: accepted.

**D-18. Postgres in two modes.** On the host (the default: more economical in memory, access over a unix socket, a role and database per project) and in a container. We do not consider containerized databases bad practice. Status: accepted.

**D-19. Application templates: a declarative manifest without scripts.** No catalog; the format is provided for and the community may make a catalog. Status: accepted.

## Interfaces

**D-20. Clients: the CLI, the UI, MCP for AI.** The UI and MCP are the main priority of the project (D-37). The UI is mandatory. The first UI is local (`ctl ui` on the owner's computer, on `127.0.0.1`, reaching the agents over SSH). The second: a UI on the server through a tunnel under Cloudflare Access. Status: accepted.

**D-21. The orchestrator (optional, later) with its own UI.** It can be a SaaS or deployed by yourself. The agents connect to it outward, it cannot confirm dangerous actions, and secret values cannot be read through it (they are encrypted in the browser with the agent's key). Status: tentative.

**D-22. The mode "the agent fetches its config from an external store" is part of the orchestrator.** The config is data: known fields, every one checked, a signature. Secrets are not put into it. Status: tentative.

## Implementation

**D-23. The language: Go.** No `unsafe` and no `cgo`, tests with `-race`, automatic service restart. The executor can be rewritten in Rust later if wanted. Status: accepted.

**D-24. The first goal: mode A, the `project create` command.** It does what was done by hand. Then `deploy`, `rollback`, `status`. The composition of the first version: D-28. Status: accepted.

## The final decisions of session S1 (2026-11-03)

**D-25. The name: `lite-deploy`.** A working one, it can change at any moment. Status: accepted.

**D-26. License: MIT** for all code. Apache-2.0 differs by explicit patent protection; in practice for a small tool the difference is small. AGPL is postponed: possibly later for the orchestrator and SaaS (the agent stays MIT), so that a closed copy cannot be run as a service. Status: accepted.

**D-27. The goal of the project.** An open tool for myself and for other VDS owners like me, with no obligations on deadlines and support, no monetization planned. Status: accepted.

**D-28. The first version (v0).** Mode A. The commands `project create`, `deploy`, `rollback`, `status`. The CLI with JSON output is the base API; thin UI and MCP work over the same commands through SSH, without a daemon. The readiness criterion: a reference project is installed and deployed through lite-deploy instead of manual scripts. v0 has no: daemon, confirmations, Docker, orchestrator, community plugins. Status: accepted.

**D-29. Action classes.**
- Safe (any client may read): `status`, logs, the list of projects.
- Ordinary (CI and an AI client may): `deploy`, `rollback`, changing a secret value, creating a project.
- Dangerous (confirmation with a key signature): deleting a project, a database or a route; changing the list of trusted keys or the confirmation method; installing, updating or removing the agent and plugins; reading a secret on the server as root (apart from this the value is not in the API at all).

Status: accepted.

**D-30. The threat model.** We protect against: a hacked project (container), a stolen CI key, a harmful or hacked plugin, a deceived AI client (prompt injection), a hacked orchestrator or SaaS, a hacked agent, a hacked executor (the damage is limited by the fixed list of actions and the systemd sandbox), a leaked repository. We do not protect against: a break-in to the host itself with root, a malicious hoster, physical access, a kernel vulnerability. Status: accepted.

**D-31. The agent and the executor.** The agent: a permanent service under a separate user without rights. It listens on `/run/lite-deploy/api.sock`, identifies the caller by `SO_PEERCRED`, checks rights, builds a plan, keeps state, waits for confirmations. It cannot change the system. The executor: root, but not hanging in the background; a second socket `/run/lite-deploy/exec.sock`, available only to the agent user and owned by systemd (socket activation): a request arrives, systemd starts the executor, which performs the step and exits. It accepts only typed requests from a fixed list, validates the parameters itself (for example the project name by the pattern `^[a-z][a-z0-9-]{1,30}$`), runs commands without a shell, and is restricted by systemd (a set of capabilities and directories). In mode A the same executor is started by hand: `sudo lite-deploy apply plan.json`. Status: accepted.

**D-32. The agent API.** HTTP with JSON over a unix socket, the version in the path (`/v1/...`), described in OpenAPI. Status: accepted.

**D-33. The project description format: JSON + JSON Schema.** The schema gives UI forms, MCP tool descriptions and strict validation (unknown fields are rejected). YAML input can be allowed later for hand editing. Status: accepted.

**D-34. The plan format.** A JSON file with a list of steps; a step is one action from the executor list with parameters. A plan has a version and a hash that is visible when applying. Status: accepted.

**D-35. The orchestrator and SaaS are postponed until v1.** The agent API is designed so that it can become a client of the orchestrator. Not discussed before v1. Status: accepted.

**D-36. The repository.** The deployment system lives in a separate private repository `toxic-sunflower/lite-deploy` (created 2026-11-12), which stayed private until v0 was ready (it is public now, see `RELEASING.md`). Status: done (it replaced the original plan to rename).

**D-37. The priority: AI (MCP) and the UI.** The consequences: JSON Schema and OpenAPI are the source of truth for forms and tools; any action first in the "show the plan" (dry-run) mode in a readable form; confirmations live in the agent (protection from a deceived AI); operations are idempotent. Status: accepted.

**D-38. Secrets and the journal if the agent is hacked.** Secret values are encrypted on the client side with the executor key; the agent passes ciphertext and does not store values. The journal is written by the executor to a file the agent cannot change. Quotas on the number of actions, so that the agent cannot flood the executor with requests. Status: accepted.

## Decisions of session S2 (2026-11-03, `SPEC-v0.md`)

They follow from D-25…D-38; accepted by the owner on 2026-11-03.

**D-39. The gatekeeper is the subcommand `lite-deploy gate`, not a script in the project home directory.** The binary in `/usr/local/bin` belongs to root; the earlier scripts (700, owned by the project user) could be overwritten by a hacked project. The gatekeeper calls the deploy code in the same process, without a shell. Status: accepted.

**D-40. The `adopt` mode.** A project set up by hand (a reference project) is taken under management without recreating it: the existing user, role and database are not broken, files that differ are saved as `.bak`. The password of an existing role: set the internal secret `_db_password` or `rotate_password`. Status: accepted.

**D-41. Secret encryption: `age` (X25519 + ChaCha20-Poly1305), the executor public key `/etc/lite-deploy/exec.pub`.** The value is wrapped in JSON with `project` and `name` (a binding, age has no associated data). We do not invent our own format. Status: accepted.

**D-42. The executor receives typed parameters, not the content of files.** It builds the units, Caddy sites, sudoers and `authorized_keys` itself from built-in templates; it computes paths from validated names. A client cannot make it write an arbitrary file. Status: accepted.

**D-43. The plan is built on the server (`project plan`), the hash is over canonical JSON (RFC 8785); a plan has a lifetime (24 h) and a `server_id`.** Applying: `sudo lite-deploy apply plan.json` with dry-run by default. Steps are idempotent, there is no rollback on an error, a repeat continues. Status: accepted.

**D-44. The CLI `--json` answers: the envelope `{ok, api_version, command, data|error}` and the exit code table.** The same data will become the body of `/v1`. The client mode `--host` runs commands over SSH. Status: accepted.

**D-45. Default quotas and limits** (section 8 of `SPEC-v0.md`): a plan of up to 256 KiB and 200 steps, 10 deploys in 10 minutes per project, 50 projects, ports 18000–18999. Status: accepted.

**D-46. In v0 `deploy`, `rollback`, `status` run as the project user through the gatekeeper with a sudoers rule for `systemctl reload caddy`; a `flock` lock per project.** In S6 (the daemon) the Caddy switch moves to the executor socket and sudoers disappears. All services of a project are deployed together (one hash). Status: accepted.

## The product and the order of work (2026-11-03)

**D-47. The product is v1, not v0.** v1: an agent daemon (mode B) with an on-request executor, a UI, MCP, write-only secrets with confirmation by a signature, installation on an empty server with one command. v0 (the commands `project create`, `deploy`, `rollback`, `status` in mode A) is the first step for checking the core on a live project, not a release. The order: the core (S3–S5), then at once the daemon (S6), then the tunnel, secrets, the UI, MCP, installation. The UI and MCP are built on the daemon API already. Status: accepted.

## Decisions of session S4 (2026-11-04)

**D-53. Commands in a project user's session are run through `systemd-run --machine=<U>@.host --user --pipe --wait --quiet --collect -- <command>`** (it closes question 1 of section 13 of `SPEC-v0.md`). This is how `daemon-reload`, the unit check and `podman login` work; the token goes to standard input (`--pipe`). Files in the project home directory are written by root, which at once hands them over with `chown` to the project user. Status: accepted by the owner as a working hypothesis (2026-11-04); **confirmed on 2026-11-19** in an Ubuntu 24.04 container and on a test WSL (it needs `systemd-run`, `loginctl enable-linger`, rootless podman and the packages `dbus-user-session` and `libpam-systemd`).

**D-54. The executor lives in `internal/executor`; access to the system only through `host.Runner`, `host.FS`, `host.HTTPClient`.** `Stat`, `Chown`, `Chmod`, `Remove`, `Append` were added to `host.FS`; writing a file through `WriteFile` creates a root-owned file, so `Chown` always follows. Status: accepted by the owner (2026-11-04).

**D-55. The behaviour of applying.** The journal `/var/lib/lite-deploy/journal.jsonl` is written before and after each step; if the journal is unavailable, applying does not start. The first error stops `apply`, the other steps are `skipped`; `dry-run` does not stop and shows all `would_fail`. An old file is replaced with a copy `.bak-<plan id>` only while the project has no management mark (`record_project` has not run yet), and the copy has the same mode and owner. `reload_caddy` in a real run always reloads (it is harmless, and an interrupted apply would otherwise leave Caddy without the new files), but reports `changed` only if the sites changed. With `adopt` a site file with the same address but another name is replaced (a `.bak` copy); if the address is taken by a file of another managed project, the step fails. A copy of `project.json` in `/etc/lite-deploy/projects` is written if the description was passed (`project create` or `apply --project-file`) and its hash equals the plan `project_hash`. Status: accepted by the owner (2026-11-04).

**D-56. `apply` and `project create` need root (and `--dry-run` too); without a terminal `--yes` is needed, otherwise `confirmation_required` (code 4).** The quota `apply_per_hour` does not work yet (counting from the journal will appear in S6). Status: accepted by the owner (2026-11-04). Since v0.6 an administrator runs `lite project create` without root: the CLI hands the preview and the creation to the agent (D-64).

## Wishes (not decisions, after v0)

- **User hooks** (`pre_deploy`, `post_deploy`): running a script in a deploy. Only as the project user, declared in `project.json`; installing and changing them is a dangerous action (a signature), so that the CI key cannot run arbitrary code.
- **Deploys on a schedule or on a new image from lite-deploy itself** (mode B). Now the trigger lives in CI (GitHub Actions), and lite-deploy receives `deploy <hash>`.

## Decisions of session S3 (2026-11-03)

Clarifications of `SPEC-v0.md` that were needed in the implementation; accepted by the owner on 2026-11-04.

**D-48. In `adopt` mode the `set_secret` step for `_db_password` goes before `provision_postgres`** (in `SPEC-v0.md` the order was the opposite). Otherwise there is nothing to check the existing role password with. Without `adopt` `_db_password` must not be passed into a plan. Status: accepted by the owner (2026-11-04).

**D-49. The non-secret project state (`state/projects/<name>.json`) holds `hash`, `ports`, `secrets` (names, not values) and `deployed`.** It is needed because `plan` works without root and cannot read the secrets directory (700) and the project home directory. `record_project` fills `secrets` on the server as root, `deployed` is set by a deploy (S5). Status: accepted by the owner (2026-11-04).

**D-50. All secret ciphertexts are passed to `project plan` with the flags `--ciphertext name=file`; an extra ciphertext (a name the project does not use) is the error `unused_secret`.** The `secret encrypt` command appears in S8. Status: accepted by the owner (2026-11-04).

**D-51. The order of plan verification: schema and size, hash, expiry, server, then the steps** (as in `SPEC-v0.md`, section 3). A step is checked against the action list, the parameters strictly, and `project` in every step equals the plan project. Before S4 `apply --dry-run` only validated the plan and showed the steps with the status `pending`. Status: accepted by the owner (2026-11-04).

**D-52. A ciphertext in a plan must be the output of `secret encrypt` (age armor), otherwise the plan is rejected (`invalid_secret`, both when building and when verifying).** A secret value: UTF-8, 1…8192 bytes, no NUL; a ciphertext up to 16 KiB. `lite-deploy init` creates the executor key `/etc/lite-deploy/exec.age` (600) and `exec.pub` (644) and never overwrites an existing private key. The commands `secret encrypt` and `secret set` read the value only from standard input or hidden from the terminal; `secret list` without root takes names from the project state (D-49). Status: accepted by the owner (2026-11-04).

**D-57. The composition of the system: a minimum at install, the rest are plugins.** The installer installs only `lite`, `minisign` (the signature check) and the agent. Everything else is plugins installed with `lite init --plugins` or from the interface: `caddy` (incoming traffic, mandatory for project sites), `podman`, `postgres`, `firewall` and on. Clarified by the owner on 2026-11-17: the earlier wording made Caddy a core dependency and installed it at install time. Status: accepted by the owner.

**D-58. The container engine: podman by default, Docker as a fallback.** A project may turn containers off completely. Status: accepted by the owner (2026-11-13). **Changed by D-65 (2026-11-19): Docker is forbidden.**

**D-59. A project consists of components of different kinds.** A component can be a container (`podman` under systemd, Quadlet) or a plain systemd service (a daemon on the host, not in a container); both kinds are allowed together in one project. A project without containers is allowed. Status: accepted by the owner (2026-11-13); implemented in v0.6 as `kind: quadlet|unit` (R-12, R-27).

**D-60. Shared resources of projects.** Several projects may share files, directories and sockets. The access model (who is allowed what and how it agrees with project isolation in `DEPLOY-PATTERN.md`) is not decided. Status: the owner's requirement (2026-11-13); postponed by the owner on 2026-11-19 (needs rootless id checks on a server); persistent directories inside one project (`data`) exist.

**D-61. System dependencies of a project.** A project declares what it needs at the host level: a system service (for example Redis without a container), a package or a library. These dependencies are installed through a plan and the executor, not by hand. Which actions are allowed is not decided. Status: the owner's requirement (2026-11-13); `lite verify` checks that the plugins a project needs are present (R-20).

**D-62. The firewall is a plugin, managed from the UI and the CLI.** The commands `lite-deploy firewall status|allow|remove|enable` (`deny` rules are not needed: incoming is closed by default), the "Firewall" section in the UI. The plugin returns a plan, and the executor changes `ufw` (the rule of section 7 of `ARCHITECTURE.md`). The SSH, 80 and 443 ports cannot be closed (the rules for them cannot be deleted); any change needs `--yes` in the CLI and a confirmation in the browser. It was made simpler than in `ARCHITECTURE.md` (section 7): the plugin calls `ufw` itself, without a plan and the executor. Status: accepted by the owner (2026-11-13). Since v0.6 it is a built-in plugin on the common mechanism.

**D-63. The agent: the first version (S6).** An unprivileged agent and an on-request executor per D-31 and D-32, but without a key-signature confirmation, quotas and OpenAPI (they are in S6, the next in the queue). System administrators (the group `sudo`, on RHEL `wheel`; there is no separate group: one list of administrators, not two) can use the agent. The installer installs the agent (not a separate command); the agent socket is 0660, the executor socket is 0600 for the `lite-deploy` user only; both sockets are created by systemd. The executor performs only the actions `status`, `deploy`, `rollback`, `firewall.*` (later also plugin commands, project creation, `fix`) and validates the parameters itself; it performs them with the same `lite-deploy` program in the same process (not through a shell). The UI reaches the agent with the command `lite-deploy call` (locally or over SSH, without `sudo`). The CI project still goes through the `gate` gatekeeper as the project user. Until there is a key signature, any member of the group can perform any action from the list, including the firewall. Status: accepted by the owner (2026-11-13).

## Decisions of 2026-11-19 and 2026-11-20

**D-64. Working without root.** A privileged command run by an administrator (group `sudo` or `wheel`) is handed to the agent and the executor; plugin commands, `fix`, `project create` (preview and creation), `verify`, `deploy`, `rollback` and `status` work this way. Passwords and keys travel through stdin. Root is needed only for `lite init` and `liteup`. Status: accepted by the owner (2026-11-19).

**D-65. Docker is forbidden.** Access to the Docker daemon is root on the server and breaks project isolation. `lite package` refuses Docker packages, `lite verify` warns if Docker is installed. Containers are podman and Quadlet only. Status: accepted by the owner (2026-11-19).

**D-66. Schema v2 and `unit` components.** `project/v2` adds `kind` (`quadlet` or `unit`), `exec`, `stop`, `data`, `release` and a database on another server; v1 descriptions are still accepted. A `unit` release is a tar.gz archive over https with a mandatory minisign signature (`minisign_key`). Status: accepted by the owner (2026-11-19).

**D-67. Servers.** A project lives on one server; foreign resources (a database) are connected by an address from the server registry; there is no scheduling, moving or balancing, and ingress is a separate server. Status: accepted by the owner (2026-11-19).

**D-68. Backups and secrets.** Project secrets enter a backup only encrypted with the owner's age key (the server keeps only the public part); database dumps are encrypted optionally; storage locations (Backblaze B2, S3, SFTP through rclone) are pluggable and upload with `copy`. The secret store is local; pluggable backends (an encrypted git, Vault) are postponed. Status: accepted by the owner (2026-11-19).

**D-69. `liteup`.** The installer and manager is `liteup` (install, update, reinstall, uninstall, status), as `rustup` is for Rust; it is part of every signed release, and the agent restarts automatically on install and update. `install.sh` is the bootstrap, like `sh.rustup.rs`: it downloads only `liteup` from the latest release into `/usr/local/bin` and stops; the person then runs `sudo liteup install` (owner, 2026-11-20); the documentation points the one-liner at `install.sh`, not at `liteup` (owner, 2026-11-20). `liteup uninstall --everything` takes the server back to bare and keeps `liteup`. Status: accepted by the owner (2026-11-20).

**D-70. English everywhere.** All user-visible text, code comments and documentation are in English. Status: accepted by the owner (2026-11-20).

## Rejected

- A central component with root that runs any command (as in Coolify, Dokploy, Kamal).
- The Quadlet format as the only one.
- Keeping the infrastructure description and secrets in GitHub or GitLab.
- A catalog of ready applications.
- A heavy web panel, teams, roles, SSO.
- Compatibility with Kubernetes and the Gateway API.
- YAML as the main configuration format (JSON was chosen, D-33) and gRPC for the API (HTTP/JSON was chosen, D-32).
- A permanently running executor process with root (a start on request was chosen, D-31).
- Docker (D-65).
- Testing the idea with "scripts for vibe coders": they do not need scripts, they need an interface (a UI or an AI client).

## Grounds (facts from the comparison with alternatives)

Taken from the documentation of the projects themselves, 2026-10-29:
- Coolify needs root or a user with sudo, stores SSH keys, and itself writes that a break into the panel opens all servers ([Security model](https://coolify.io/docs/core/security-model)); there is an official MCP with permission separation ([MCP](https://coolify.io/docs/mcp/how-mcp-works)); Cloudflare Tunnel as an option ([Tunnels](https://coolify.io/docs/knowledge-base/cloudflare/tunnels/overview)).
- Dokploy: Docker and Swarm, Traefik, PostgreSQL, ports 80/443/3000, at least 2 GB and 30 GB ([Installation](https://docs.dokploy.com/docs/core/installation)); external secret managers; OpenAPI.
- Kamal: SSH as root by default, the config in the repository, no daemon and no panel ([Installation](https://kamal-deploy.org/docs/installation/)).
- Not checked: the Dokploy AI Assistant and API, Dokku, CapRover, Podman in the full text of the documentation, real memory use.

**D-71. Two publishing modes, one loopback listener.** `tunnel` and `direct` differ only by an extra public Caddy site block (`host { … }`, automatic HTTPS) that imports the same upstream file as the 127.0.0.1 block, so deploys, rollback and the upstream switch are unchanged and a tunnel keeps working in `direct` mode. Cloudflare as a whole: D-85. On the public block `CF-Connecting-IP` is overwritten with the real peer address when `trusted_proxy` is `cloudflare`. Status: accepted by the owner (2026-11-20).



**D-72. Protection is fully on by default.** `lite init` enables the `security` plugin completely: fail2ban, automatic security updates, protective kernel parameters, and SSH by key only for everyone (root by key only), unless `--no-security` is given. It already requires a key for the new user and tells the person to check a second login. `allow-users` is optional. Caddy, podman, PostgreSQL and Redis remain opt-in plugins. Status: accepted by the owner (2026-11-20).

**D-73. High-level commands and a UI-first path.** The path for the target user is: install `lite`, run `lite init`, hand over the key, then use only the UI; the server is protected by `init` itself. The terminal stays supported but is never required. `lite` commands express goals, not system tools: low-level steps such as fail2ban, sysctl, automatic updates or SSH settings are internal parts of one goal and are not part of the everyday commands. Status: accepted by the owner (2026-11-20).

**D-74. A separate UI-only client.** The person's computer gets a small program with only the interface, started by a double click, not the whole product. It reaches the server over SSH and opens nothing to the internet. Status: accepted by the owner (2026-11-20).

**D-75. `lite-ui` manages many servers.** The page has a list of servers (USER@ADDRESS) kept in a file on the workstation; the person adds, removes and chooses a server in the browser. Every request names the chosen server, and `lite-ui` connects only to servers from the list. Status: accepted by the owner (2026-11-20).

**D-76. The structure of the interface.** The menu of `lite-ui`: Overview, Projects, Ingress, Databases, Backups, Security, Server. Overview shows the state of the server in one word, a few numbers, what needs attention (each item with its button) and the projects. **Ingress** is a part of the server, not of a project: how traffic reaches the server (Caddy with its own certificate, Cloudflare Tunnel, or both, with a switch; ports 80 and 443; certificates) and which domain leads to which application (a table of all domains of the server). Caddy leaves "plugins" and lives in Ingress. A domain is still stored inside the project description. Every screen says what to do next, uses plain words, and shows what a dangerous button will do before it is pressed. Status: accepted by the owner (2026-11-20).

**D-77. The interface is written in Vue 3 without a build step.** `lite-ui` embeds plain `.js` files and the single file `vue.global.prod.js` (MIT, kept in `internal/ui/web/vendor`), so the repository needs no Node or npm. Pages are components with string templates; the page compiles them in the browser, which is why the page policy allows `unsafe-eval` for its own scripts (the page is served only on 127.0.0.1 and needs the token). The interface shows only what the server has: a plugin that can tell reports `installed`, and the menu and the "Can be added" list follow it. A plugin's section is drawn from what the plugin reports, with no per-plugin front-end code. Status: accepted by the owner (2026-11-20).

**D-78. Dangerous changes confirmed with the owner's key (opt-in).** The owner pairs a computer from the interface: an ed25519 key pair is made there, the public part is written to `/etc/lite-deploy/owner-keys`. When at least one key is registered, the agent refuses the requests that change the server (everything except reads, deploy and rollback) unless they carry a valid signature over the method, path, body, server, time and a one-time number. Without a registered key nothing changes. Someone who only has an administrator account on the server cannot make such changes. Status: accepted by the owner (2026-11-20).

**D-79. A root helper switches traffic (completes D-46).** A project user that deploys (CI over ssh) no longer writes the Caddy upstream file or runs a sudo rule. It asks the helper `lite-deploy-switch` (a socket-activated root service on `/run/lite-deploy/switch.sock`), which identifies the caller by the kernel, serves only users of managed projects and only their own sites, and writes the file from a fixed template with the ports of the project description. The upstream files are root-owned, and the sudo rules of lite-deploy are removed by `migrate` once the helper is installed. A server without the helper keeps working the old way. The executor and the helper run with a small set of systemd limits that cannot get in the way of what they do. Status: accepted by the owner (2026-11-20).


## D-80 (2026-11-20). No logic on the client

`lite-ui`, the CLI and MCP are thin: every action is one command of the agent or a plugin (`lite PLUGIN COMMAND`), so the page, the terminal and an AI assistant do exactly the same thing. The Cloudflare side of a domain is `lite cloudflare connect` (and `access-*`), run on the server (the whole Cloudflare picture: D-85); the Cloudflare API token is read from standard input, used once and never stored. An earlier version had this logic in `lite-ui` (D-79 era) and it was removed. Client-only things stay client-only: updating `lite-ui`, running it in the background, encrypting a secret with the server key.

## D-81 (2026-11-20). A database is a secret

The core does not know databases. The connection to a database (PostgreSQL, MySQL, Redis, anything) is **a secret** of the project, for example `DATABASE_URL`, written in its secret settings like any other. Anyone can install a program on the host with a package and pass its connection as a secret. If a plugin for that kind of database is installed (`pg` today), it only **helps**: it makes the database and the user and puts the ready connection string into the project's secret. There is no list of address formats: whoever needs another notation writes the string. The fixed `database` field of the project description, `database_env`, `connection_format`, `rotate_password`, the internal secret `_db_password`, the action `provision_postgres` and the connection-string code of the core were removed in v0.27.0 (a breaking change of the project description; a project that had a database writes the connection as a secret and, for a container, shares the socket with `sockets`). The registry of servers (`lite server add`, the `server` plugin) had no consumer any more and was removed in v0.27.1.


## D-82 (2026-11-22). lite-vault: the secret store of the server

**Status: agreed with the owner in a long design talk (2026-11-21/22). Nothing is built yet; the work is the task T26 in `PLAN.md`.** It replaces only the *storage* of D-10 (today: files with mode 600 in the project's home and a clear-text store under root). The rest of D-10 stays: the description holds references (`secret://...`), secrets are write-only (there is no "show" command; the page shows names, never values; the "Show" button exists only while a person types), plugins for external stores come later.

### The problem

Today a secret lies on the server in clear text: `~project/.config/lite-deploy/env/SERVICE.env` (passed to the container as environment variables) and the store under `/var/lib/lite-deploy`. Anyone who gets a copy of the disk, a VPS snapshot or a stray file reads everything. A container started with `-e NAME=value` (or an `EnvironmentFile` in the Quadlet) shows the values in `podman inspect` and in the unit file. A project can read the files of another one only through mistakes in the permissions, but nothing in the model says "this application must not see that secret". There is no record of who read what.

### What lite-vault is

A small service on the server that is the **only** holder of the secrets and the only thing that can decrypt them. It listens on a unix socket, runs as its own unprivileged system user `lite-vault` under a hardened systemd unit, and is a **separate binary** (`lite-vault`) in this repository, next to the agent. Its code lives in `internal/vault` and must not import the agent or the executor, so it can be moved into its own repository later without a rewrite (see "Repository" below).

### The decisions

1. **One value, stored once, with an owner and a list of who may read it.** A secret has a scope: *private* (only the application that owns it), *selected* (an explicit list of projects) or *all* (every project on this server). A "shared secret" is **not a copy**: it is a **link**. A project gets access to the same value **under its own name** (`SMTP_PASSWORD` in one application, `MAIL_KEY` in another). Changing the value updates everyone; revoking one project's link leaves the others alone. This replaces `shared:KEY` / `secret://shared/NAME` of today.
2. **Who is asking is known from the kernel, not from a token.** The vault reads the peer credentials of the unix socket (`SO_PEERCRED`): every project has its own system user, so the caller's user is the project. Two layers: the vault checks the link; and on the file system the values it hands out lie in tmpfs, owned by the project user, mode 0400. A project cannot read another project's secrets by either way.
3. **Delivery: the default is files in tmpfs written at start, not the socket.** Before a component starts, the vault writes its secrets into `/run/...` (memory, never the disk), owned by the project user, 0400. A container gets them **mounted as files** (`/run/secrets/NAME`) or as a podman secret of type `env`; the value never appears in the Quadlet file, in `podman inspect` or in the process list. Why not `-e` and not `EnvironmentFile`: the value is then written in the launch settings and shown by `inspect`, and is inherited by child processes and crash dumps. Why files by default: **any** application works with no change in its code (ASP.NET Core has `AddKeyPerFile` for exactly this; others can read a file or use a small entrypoint). The socket is the second way (decision 5) for applications that want it. Start order: the unit of a component requires the vault; if the vault is **locked** (decision 7) a component does not start (an already running one keeps running) and the page says "the server is locked".
4. **Two kinds of secrets: a value and a key.** A **value** is handed to the application (a database password, a token of another service): there is no other way, the application has to present it. A **key** is **never handed out**: the application asks the vault to do an operation with it, and the vault answers. Operations: sign and verify (Ed25519, ECDSA P-256), HMAC-SHA256, encrypt and decrypt (an AEAD such as XChaCha20-Poly1305 or AES-256-GCM), and, later, others. A key is generated inside the vault and there is **no export command by design**; the public part of a signing key can be read. If an application is compromised, the attacker can use the key only while he has access, and he **cannot take it away**: after the access is closed the key is still safe and need not be reissued. Every operation is in the journal and can have a rate limit. Typical uses: signing the JWT tokens of a login, signing webhooks, encrypting single fields in a database (personal data), a "pepper" for password hashes (HMAC of the password before hashing: a leaked database is useless without that key), client certificates. Operations are available only through the socket (decision 5), so the application needs a small client (HTTP over a unix socket); a ready adapter for .NET is a separate item of the task.
5. **The socket API is small and the same for everything.** HTTP with JSON over the unix socket: read a value, run an operation, ask for the public part of a key, status. The same API will later be exposed over the network (decision 10) without a redesign, which is why it is designed now, even before the network mode exists.
6. **Aliases are for convenience, not for security.** A name like `SUPER_ADMIN_PASSWORD` tells an intruder what he is looking at and a neutral name makes him think a little longer; but whoever reads the application's environment also sees how the value is used. The real protection is **not giving the secret to an application that does not need it**. The page says so. Advice for people: avoid shared secrets where the service allows separate accounts (two database users instead of one shared password): then a break-in into one application does not touch the other one's credential and each can be revoked separately.
7. **Keeping the key of the vault: two modes.** *A (the default)*: a key file readable by the vault user only (0400). The server rises by itself after a reboot. It is weak against a disk snapshot (the snapshot of a VPS holds the data and the key together) and good against a leaked backup or a stray copy of the secret store; the page must say this plainly. *B (the "protected" mode)*: no key on the disk at all. The master key is derived from a passphrase (Argon2id) and lives in memory only. After a reboot the server is **locked**: the page shows "the server is locked" and a button **Unlock** (the passphrase goes through standard input, is never stored and never travels in a command line); components that need secrets start when it is unlocked. A reboot is not a normal event for such a server and must never happen by itself. *D (TPM)* would bind the key to the hardware and let the server rise alone with no key file, but the test server has no TPM (`/dev/tpm0` does not exist on `vmi3750460`), so D is postponed to hardware that has one; with no TPM systemd-creds degrades to a plain key file, i.e. to A. *C (the key fetched from another place at start)* belongs to the network mode.
8. **A hierarchy of keys, so "locked/unlocked" is one idea.** One master key, from which separate keys are derived (HKDF) for each purpose: the secrets, the key of the executor (today it lies on the disk), the database dumps while they wait on the server for sending. The state *locked / unlocked* is shown by the page and by `lite verify`. **Not** inside the hierarchy: the key of the **backups**, which stays the owner's own age key, kept outside the server; the whole point of it is that losing or breaking into the server does not reveal it. Full-disk encryption is out of scope.
9. **An honest statement of what it protects (the same words in the interface).** It protects against: a leaked or stolen disk, a snapshot or a backup (in mode B even the snapshot); an application reading another application's secrets; values showing up in `podman inspect`, in unit files and in the process list; unnoticed reading (there is a journal of every read). It does **not** protect against **root on the running server**, or against anything that reads the memory of the application or of the vault: if someone has root, nothing on that machine helps, and an external vault would only shrink the damage (revoke a token, change everything in one place, nothing to carry away from the disk). A compromised application loses **its own** secrets, never other applications' private ones; a secret that is linked to it is lost too, which is a reason to use few shared ones.
10. **The network mode (a later stage; here so the design is not closed to it).** The same API over the network with mutual TLS: one vault for several servers, kept on a small separate machine; a server asks for its secrets at start and keeps them in tmpfs. What it adds, and a local vault cannot: **leases** (a secret is given for a limited time and renewed while the vault recognises the server) and a **remote kill switch** ("lock this server": the secrets vanish from its memory and are not renewed), which works also against a server that is stolen, seized or no longer under the owner's control, because the decision is taken on another machine; **one unlock for the whole fleet** after a reboot; **short-lived database users** (the `pg` plugin creates a user for an hour; a leaked password dies by itself); one journal for all servers. A local vault can also have a "lock now" button and secrets with a lifetime, but the machine that decides is the same machine that may be compromised, so the benefit is only a part of the above. External stores (OpenBao, Infisical, Doppler, cloud managers) stay possible as **backends** of the same interface (the plugins of D-10); building our own network service must be justified against them: the justification is the leases and the kill switch tied to the rest of lite-deploy, and a setup a person who is not a professional can finish.
11. **Migration.** The first start of the vault on a server that has secrets imports them (project secrets, shared secrets, service env files), checks that each value reads back, removes the old clear-text copies and the old service env files, and reports it. One way; take the encrypted backup first. Today only the owner has a server, so there is no risk, but the step is required before there are other users.
12. **Storing.** Each secret is encrypted separately (AES-256-GCM) with its own key, which is wrapped by the master key; the encryption of what the browser sends stays as it is (`secrets.Encrypt` with the public key of the server, bound to the project and the name), and the executor hands the decrypted value to the vault. The journal of the vault is an append-only chain of records (each holds the hash of the previous one) so cutting out a record is noticed. The vault data is part of the existing encrypted backup of the settings and secrets.

### Repository

The same repository, a separate binary `lite-vault`, its own user and unit, `internal/vault` with no imports of the agent. Reasons: one format and one encryption code (`internal/secrets`), one release line with the minisign signature, the versions of the agent and the vault never differ. A **separate repository** is right only if someone should use the vault **without** lite-deploy, or an independent audit is wanted; the decision is taken when the network mode exists. The move is cheap if the package boundary above is kept.

### Not decided yet (to settle while building)

- The list of operations and algorithms of the key kind (the minimum: Ed25519 signing, HMAC-SHA256, AEAD encryption); how a key is rotated; whether an import of an existing key is ever allowed (default: no).
- How an application learns that a secret changed: files in tmpfs are rewritten and the component is restarted by `deploy` or by an explicit command; a hot reload only through the socket.
- The exact client for the socket (a tiny library, an HTTP call, a sidecar) and the .NET adapter for the key operations.
- What the already running components do when the vault is locked after they started (they keep running; only starts are refused), and what the page shows.
- Rate limits and quotas of the key operations; the format and rotation of the journal.


## D-84 (2026-11-22). One path through the agent; the key confirms everywhere; the program says loudly what is weak

Agreed with the owner in the discussion of 2026-11-22 and written down before it is built (task T28). It replaces an earlier shorter version of this entry (it was numbered D-82 by mistake: D-82 is lite-vault) and **cancels D-63** (one list of administrators). Items marked "(to confirm)" are the author's reading of the code or a proposal the owner has not answered yet.

1. **Four roles.** The agent (`lite-agent`: the only place that checks, plans and journals; root work only through the executor). `lite` (**always only a client of the agent**: on the server over the local socket, on a workstation over SSH). `liteup` (installs, updates, removes the software). `lite-ui` (a client too). A client holds no logic (D-80). `lite` and `lite-agent` become separate executables built from this repository (the size of this step is known only after the inventory in T28).
2. **Direct root only for setting up the server:** `liteup install`, `lite agent install`, `lite init`, `lite agent restart|status|uninstall` (the agent itself may be down, so it cannot be asked). **Everything else goes through the agent, also under `sudo`.** Today `sudo lite ...` runs the whole CLI as root in place (a comment in `internal/cli/forward.go`; README and D-64 say the opposite): a gap, and it is closed. Why `lite init` stays direct: it creates the administrators, their SSH keys, the firewall and the sshd settings, that is, what the agent's trust is built on, and the executor has no such actions (the author's reading of the code, not an earlier decision).
3. **Groups.** The agent trusts the group `lite-admin`, not `sudo`/`wheel`: "may use `lite`" and "may become root" are two rights. Nobody loses `sudo`: administrators with it stay. When `lite` creates an administrator (`lite init` or a user command) it **offers** `sudo` and `lite-admin` separately and says what the risk is; it does not force. `lite verify` also warns in the other direction ("nobody on this server can get root").
4. **Confirmation by the key works wherever `lite` runs, with the same key.** (a) `lite-ui`: the person confirms every dangerous action in the page; there is **no automatic signing on the strength of the page token** (today `signFor` in `internal/ui/owner.go` signs any changing request that carries the token: that is the hole). (b) `lite` on a workstation signs with the same key as `lite-ui`. (c) `lite` in the console of the server asks for the key (hidden input, like a password), uses it once in memory and never stores it; the key is a short string (about 44 characters), a file is only how a workstation keeps it. (d) Advice: two registered keys, one for the workstation and one typed on the server, so a leak of the second is revoked alone (several keys with labels already exist). The honest cost: a key typed on the server is in its memory for a moment. (e) `deploy` and `rollback` need the confirmation from everyone: CI deploys as the project user over SSH and does not go through the agent (`internal/cli/deploycmd.go:109`, to confirm by the inventory), so the agent need not tell a CI key from an administrator.
5. **Who decides.** The person who sets the server up, at `lite init` and later in the settings: it asks whether to require confirmation (recommended), makes a key and shows it once. A refusal is explicit and shown as a red warning afterwards.
6. **The program says loudly what is weak** (in `lite verify`, the Overview page and MCP, in plain words, with one action that fixes it): no owner key is registered; an administrator has `sudo` without a password (a stolen SSH key gives root); an administrator has both `sudo` and `lite-admin`; the CLI ran around the agent; the state differs from the remembered baseline (administrators, `owner-keys`, sshd settings, sudoers); the key file or the token of `lite-ui` has loose permissions. `lite init` does not finish until a key is registered or the person refuses explicitly. It offers to remove `NOPASSWD` **only after** the administrator has a password (many VDS images give the user none, and removing `NOPASSWD` would take `sudo` away). A sudo password is weak against a process that already runs as that user (it can fake `sudo` and watch the password): the real fix is item 3 with `lite-admin` and no `sudo` for daily work; the person decides.
7. **What is not protected, said plainly.** Anyone with root on the running server can edit `owner-keys`, the sshd settings and the journal; the program cannot prevent it, only notice and report it (the baseline, the journal also sent off the server, the hash chain of D-82 lite-vault). A repeated `lite init` on an initialised server asks for the key: a barrier against accidents, not against root (to confirm: whether it asks today is not checked).
8. **Transparent for the person.** Plain words, no terms, one action (a button or a single command).
9. **Not now.** A third factor (a passphrase on the key, a hardware key, a phone code or an approval from a phone); the interface built with Vue instead of templates compiled in the browser (it removes `unsafe-eval` from the CSP); the network vault.
10. **Claims about competitors.** The comparison with Dokploy and Coolify is not finished (`COMPARISON-PLAN.md`). Checked on 2026-11-22 in the Dokploy documentation: the installer runs as root and Dokploy's service mounts the host's Docker socket (root-equivalent; a search summary, the exact wording of "read-write" is not verified); an advisory GHSA-7r6p-v9gw-pwc8 about a low-privilege member reaching root is reported, not verified. Until the comparison is done, `VISION.md` and D-03 must say "not verified" and no text says "safer than Dokploy".

Open: the order of moving the commands that run in place under `sudo` (decided by the inventory); where the key of `lite` on a workstation lives and how it is shared with `lite-ui`; the format of the key to type (to confirm).

## D-83 (2026-11-22). Infrastructure of a project: what, and where it lives

Discussed with the owner on 2026-11-22; the direction is agreed, the build order is a proposal the owner has not yet confirmed (see "Open"). It builds on D-81 (a database is a secret, the core knows no databases), D-59 (components of different kinds), D-80 (no logic on the client) and, for how the secrets reach the application, on D-82 (lite-vault).

### The problem

The form of a new project offers one database and only PostgreSQL on this server. The owner (a developer) wants a project to have **infrastructure** ("infra"): PostgreSQL, Redis, MinIO (S3), later anything else, **each of which can live in a different place**: on this server, on another server, in a container. And, when one project is spread over several machines, the owner wants to **set up the database of the project from the main panel**, not by hand on the other machine.

### The model: two sides

**Application side (the contract).** Whatever the kind and the place, the application receives the same thing: **secret settings** (environment variables) that hold the connection, for example `DATABASE_URL`, `REDIS_URL`, `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`. This is D-81 and does not change: the project description holds only `secret://...` references, values never appear in it. The application does not know where the infra is.

**Infra side (the provisioning).** Here the place **does** matter, because someone has to prepare the thing: make the database, the role, the password, open access. The deciding question is **whether lite-deploy is the administrator of that place**:

| Place | Administrator? | What lite-deploy does |
|---|---|---|
| This server (host) | yes | creates the database and the role (`lite pg create-db`), stores the connection as a secret |
| In a container | yes (it creates the container) | creates the container, then the same preparation inside it |
| **Another server that is in the panel** ("trusted server") | **yes** (the panel manages it too) | prepares it on that server (create, `listen`, `allow`) and stores the connection as a secret of the project on the application server |
| Someone else's database (a cloud service, a server not in the panel) | **no** | takes ready data from the person, assembles the connection string, can test it; creates nothing |

An earlier wording "the application does not care, so all places are the same" was corrected by the owner: it holds for the application only. The provisioning differs, and the **first three** places are one family ("we administer it") that differ only in the target and in the access that must be opened.

### Place 1: this server (host)

* PostgreSQL: **done** (T22, D-81). `lite pg create-db DB ROLE --project P --secret NAME [--template T]` makes the database and the role, lets the role in over the socket, stores the connection string as a secret of the project. A container shares the socket directory with `sockets: ["/var/run/postgresql"]`.
* **Redis: to build.** Today the `redis` plugin (`internal/builtin/redis.go`) has only `install` and `status`. Needed: a command like `lite redis create-user --project P --secret NAME` that makes an ACL user restricted to a key prefix (or a numbered database) with a generated password and stores `REDIS_URL` as a secret. Redis listens on the loopback by default; a container reaches it like it reaches PostgreSQL (see the network question below).
* **MinIO: to build.** There is no MinIO plugin. Needed: install (a package or a binary with a signature check, like other binaries), a systemd service on the loopback, and a command that makes a **bucket and an access key/secret pair** restricted to that bucket (an IAM-style policy), stored as secrets of the project (`S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`).
* The rule for every new kind: **a plugin command, not code in the core** (D-81). The page, the terminal and MCP only call it (D-80).

### Place 2: another server that is in the panel ("trusted server")

The owner's wish: one project on several machines, the database of the project set up from the main panel. Agreed to do this **early, not "later"**, because the pieces already exist and the missing part is small.

What exists (`docs/remote-database.md`, `internal/builtin/pgremote.go`): on the database server `lite pg create-db DB ROLE --password-stdin`, `lite pg listen ADDR` (PostgreSQL also listens on the internal address), `lite pg allow DB ROLE APP_ADDR` (edits a separate block of `pg_hba.conf`, requires ssl and a password, opens port 5432 in the firewall **only for that address**; `0.0.0.0/0` is refused), `lite pg access`. Today a person runs them by hand on the second server and then adds `DATABASE_URL` by hand on the first.

What is missing is **one action that does the sequence across two servers**:

1. On the database server (D): `pg create-db` (generated password), `pg listen`, `pg allow` for the **internal address of the application server (A)**.
2. On the application server (A): store the connection string (host = the internal address of D, `sslmode=require`) as a secret of the project.

**Where the logic lives (D-80).** Each step is a command of a server (the agent of D, the agent of A). The panel only calls them in this order, exactly as the new-project form already calls `project create` and then `pg create-db`. The panel is already connected to every server of its list over SSH. Two consequences, both accepted: (a) the connection string **passes through the workstation** (from D to A), encrypted, never stored, as the API token of Cloudflare does; (b) the panel must know the **address of A as D sees it** (the internal network): the form asks for it with a default and says that the database port must **not** be opened to the public internet.

This is **not** the removed "registry of servers" (`lite server add`, the `server` plugin, removed in D-81 together with the `database.server` field of the project): a trusted server is just *another server of the list in the panel*. The project still does not know about it: it holds the secret only.

Risks to state in the form and in the docs: the network between the servers must be private; TLS is required; the secret on A is the only copy besides the one inside D; backups of that database are those of D (the Databases page of D); if D is rebuilt the secret on A goes stale (the form shows the secret and where it points).

### Place 3: someone else's database

We are not the administrator, so **nothing is created**. The form asks for the parts (host, port, database, user, password, mode of TLS), assembles the connection string in the notation the person chooses (a URL by default, or `--template`), stores it as a secret, and offers a button **"Test the connection"**: the **application server** tries to connect to that address (so the test answers the real question "will it work from there") and reports in plain words (cannot reach / refused / wrong password / TLS). This is cheap: a form and one check command. Today the person must write `DATABASE_URL=...` by hand (docs/remote-database.md); that stays possible under "Advanced".

### Place 4: in a container (built last)

The hardest, because **the current model does not fit it**. Facts of the code today (`internal/render/render.go`, `internal/project`):

* Every container component runs in **two slots (blue and green)** for a deploy without downtime. A database in two slots would be two instances on one data: not allowed.
* A container publishes its port only as `PublishPort=127.0.0.1:HOSTPORT:CONTAINERPORT`. **Containers of a project cannot see each other**: there is no project network; the only link to a host service is a shared socket directory (`sockets`).
* Persistent data: `data` directories (`Volume=%h/.local/share/lite-deploy/data/NAME:/data/NAME:U`), shared by the slots.

What it needs (each point is a decision to make at the time):

1. **A new kind of component: a single-instance one** (e.g. `kind: quadlet` with `slots: 1` or a separate `kind: service`): no blue-green, **not touched by `deploy` and `rollback`**, restarted by systemd, its own data volume, its own memory limit.
2. **A network of the project** (a Quadlet `.network` file): the containers of one project join it and reach each other by name (`db`, `cache`), nothing is published outside. Open: do the existing application containers (blue/green, published on the loopback for Caddy) join this network as well, and does the loopback publishing stay.
3. **Backups and upgrades.** A volume of a database in a container must be backed up (a dump through the container or a stop during the copy); the **major** version of PostgreSQL must not change by itself (a pinned image tag; the upgrade is an explicit action with a warning). Redis/MinIO are simpler (an RDB/AOF file, a bucket copy).
4. **Secrets are generated by the server** (the password of the database) and stored as secrets of the project, as in place 1.
5. **Defaults.** Redis and MinIO in a container are sensible from the start (simple, no schema migrations). **PostgreSQL stays on the host by default** because the host already has backups, the schedule, `verify` checks and the Databases page; a container is a chosen option, later.

### The form: one step "Infrastructure"

Replaces the step "2. Database" of the new-project form (T24). A list; the button **"Add"** gives: **What** (PostgreSQL / Redis / MinIO / other) and **Where** (This server / Another server of mine [a choice from the panel list] / Somewhere else / In a container), then only the fields of the chosen combination. Under each item: what the application will receive (the names of the secrets) and, for the places we administer, the exact steps that will be done (the same preview as the project already has: `path` and `content` of the files, the steps). "Install PostgreSQL on this server" stays as a button when the plugin is missing (done 2026-11-21). The Databases page stays as the place for backups and deletion.

### Order of work (proposed)

1. **Host for PostgreSQL (done), Redis and MinIO**: plugin commands `create-…` with `--project --secret`, the list "Infrastructure" in the form (host only at first).
2. **Another server of mine**: the sequence create/listen/allow on D and the secret on A, the address of A as seen by D, a clear report of the steps.
3. **Somewhere else**: the form of the parts and the "Test the connection" command.
4. **In a container**: the single-instance kind, the project network, backups and pinned versions; Redis and MinIO first, PostgreSQL last.

### Decided by the owner (2026-11-22), instead of the open questions

* **All four places are wanted, including the container.** Nothing is dropped: host, another server of the panel, someone else's database, container.
* **All kinds are wanted** (PostgreSQL, Redis, MinIO, and the model must allow others). The order in "Order of work" is only the order of building, not a choice of what to skip.
* **This is a development plan only.** Nothing is started until the owner says so; the order above is a proposal for when work begins.
* **There is no second server.** Place 2 cannot be tested live: it is built and covered by tests with the fake server, and its live check stays in `TESTING.md` as not run until a second server exists.

### Still open (technical, for when work begins)

* Place 2: the internal address of A: typed by the person, or found by the agent (the addresses of its interfaces, private ones first).
* Place 4: do the blue/green application containers join the project network, and does the loopback publishing stay.
* The delivery of the infra secrets to the application: for now `secret://` references as in D-81; when lite-vault (D-82 about lite-vault, T26) exists the same secrets can be delivered as tmpfs files; the infra commands must write them through the same secret path, so that move is free.
* MinIO: package or signed binary, and where the data lives (a `data` directory of the system user, or `/var/lib`).

## D-85 (2026-11-22). Cloudflare: what we do, where it lives, with what token

One place for what is scattered over D-11, D-15, D-20, D-71 and D-80 and over T21 and T25 of `PLAN.md`. Nothing here changes those decisions; it adds the one new thing (the Under Attack mode, below).

**What Cloudflare is for us.**
1. **Tunnel** is the default way to publish a site (D-15, D-71): the server connects out to Cloudflare, so no port of the server is open. `direct` mode (Caddy answers on 80 and 443 and gets the certificate itself) is the way out when Cloudflare has a problem, and a tunnel keeps working in it (D-71).
2. **Access** closes a domain or a path with a Cloudflare login (e-mail or a whole mail domain). It is also one of the confirmation methods of D-11 and the way to put the server UI behind a login (D-20).
3. **Under Attack** (new): the zone security level `under_attack`, a short check page for every visitor. Accepted by the owner (2026-11-21).
4. **Ordinary DDoS filtering** is not ours to switch on: Cloudflare does it for every domain behind its proxy, so for a tunnel. In `direct` mode there is none.

**Where the logic lives (D-80).** Everything is a command of the `cloudflare` plugin run on the server: `connect` (tunnel, route and DNS record in one step), `access-list`, `access-protect`, `access-remove`, `under-attack --mode on|off|status`, `status`, `remove`. The page, the terminal and MCP only call them. The Cloudflare API token is read from standard input, used once and never stored; only the token of the tunnel is kept, in a closed file for `cloudflared` (mode 600, root).

**The token and what it needs.** The owner of the Cloudflare account creates it; each domain can use its own token, even from another account (so several tunnels, `--name`).

| For | Permissions |
|---|---|
| `connect` | Account: Cloudflare Tunnel: Edit; Zone: DNS: Edit; Zone: Zone: Read |
| `access-*` | Account: Access: Apps and Policies: Edit |
| `under-attack` | Zone: Zone Settings: Edit (tentative: not yet checked against the real Cloudflare) |

**Under Attack, the rules.**
- It applies to the **whole domain (zone)**, never to one address. This is a limit of Cloudflare, and the page says it.
- `on` sets `under_attack`; `off` sets the normal level `medium`, whatever the level was before (the program keeps no state about it).
- Turning it on twice changes nothing. It is an action that confirms with the owner's key like the other changing actions of the plugin (`Confirm: true`, D-78).
- It is switched by hand. Switching on load or on a signal is not done.

**Limits to remember.**
- A DNS record of the same name from an earlier tunnel is not overwritten by a new route: error 1033 while the tunnel is Healthy (`DEPLOY-PATTERN.md`). `connect` replaces a record of this name and the page asks first.
- Uploads through the free plan are limited to 100 MB.
- A fallback Basic-auth password exists only in `direct` mode and never in tunnel mode.

**Not done.** Deleting a tunnel together with its route and DNS record; WAF rules, rate limiting, Bot Fight Mode; automatic switching of Under Attack. Live check of all Cloudflare calls: `TESTING.md` (T20).

Status: a summary of accepted decisions; Under Attack accepted by the owner (2026-11-21), its token permission to be confirmed live.
