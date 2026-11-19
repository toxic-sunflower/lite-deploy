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

**D-15. The default set of plugins.** Caddy, Postgres, Podman, Cloudflare Tunnel (the default publishing provider), `base` (server protection). Status: accepted. (Docker was dropped from the set by R-11, 2026-11-19.)

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

**D-69. `liteup`.** The installer and manager is `liteup` (install, update, reinstall, uninstall, status), as `rustup` is for Rust; it is part of every signed release, and the agent restarts automatically on install and update. `install.sh` stays as a wrapper. Status: accepted by the owner (2026-11-20).

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

**D-71. Two publishing modes, one loopback listener.** `tunnel` and `direct` differ only by an extra public Caddy site block (`host { … }`, automatic HTTPS) that imports the same upstream file as the 127.0.0.1 block, so deploys, rollback and the upstream switch are unchanged and a tunnel keeps working in `direct` mode. On the public block `CF-Connecting-IP` is overwritten with the real peer address when `trusted_proxy` is `cloudflare`. Status: accepted by the owner (2026-11-20).



**D-72. Protection is fully on by default.** `lite init` enables the `security` plugin completely: fail2ban, automatic security updates, protective kernel parameters, and SSH by key only for everyone (root by key only), unless `--no-security` is given. It already requires a key for the new user and tells the person to check a second login. `allow-users` is optional. Caddy, podman, PostgreSQL and Redis remain opt-in plugins. Status: accepted by the owner (2026-11-20).
