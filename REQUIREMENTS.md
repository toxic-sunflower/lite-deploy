# Requirements and limits (updated 2026-11-20)

This file records what the product must do, what is in the first working version and what is not. It outranks `PLAN.md`: the plan is rebuilt around it. Section 0 is a short summary of what has been agreed, so the thread is not lost.

## 0. Summary: how it should work

1. **Installation** with one command installs the minimum: `liteup` (the installer and manager), `lite` (and `lite-deploy`), `minisign` and the agent. Nothing else on the server changes.
2. **`lite init`**, the only command that may be run as root, prepares the server: it creates administrators (the password through the system `passwd`, an SSH key is mandatory, `sudo`), leaves them SSH login by key only, sets up the firewall (`--ufw keep|off|http|max`, `max` by default), installs plugins (`--plugins caddy,podman,pg`). It does not disable root SSH login: that is the server owner's decision.
3. **All other `lite` commands refuse to run when logged in as root**: log in as a regular user. They need no `sudo` either: commands that need privileges are handed to the agent.
4. **After that everything is in the interface** (and on the command line): plugins, the firewall mode, ports, packages, projects, deploys, the server check (`verify`, `info`, `fix`).
5. **The interface** runs on the owner's computer and reaches the server over SSH. On the server an unprivileged agent runs, and the executor is started by systemd as root for one action from a fixed list. An arbitrary shell as root is never executed.
6. **Projects**: `podman` containers (Quadlet) or systemd services; blue-green deploys without downtime; CI deploys through a restricted SSH key.

## 1. What it is

`lite`: one binary that is installed on a bare Linux server, prepares it for work and deploys projects to it. Management from the command line and from a web interface. One server, several projects; other servers are connected by address (R-26).

## 2. State on 2026-11-20

The latest release is on the Releases page (v0.8.0 is the first release candidate). The site and the installer work. **Nothing has been run on a production server yet.** Besides unit tests with doubles, `lite` was run on 2026-11-19 in an Ubuntu 24.04 container with systemd (rootless podman 4.9) and on a test WSL (Ubuntu 26.04, podman 5.7, PostgreSQL 18, Caddy): `lite init` (user, SSH by key only, security, caddy, podman, pg, redis), the agent, commands without sudo, encrypted backups and restore, `project create` (a container project and `kind: unit`, a repeat gives `unchanged`), a `unit` deploy with a minisign signature check, the Caddy switch, a rollback, and `liteup` update, reinstall and uninstall. That run found and fixed 12 errors the tests had not seen. **Not checked yet:** ufw, a real SSH login, starting a Quadlet container (there was no image), Backblaze, S3, SFTP, the form in a browser.

| Part | Code | Checked on a server |
|---|---|---|
| `liteup` installer and manager: install, update, reinstall, uninstall, status; the agent restarts on install | done | partly (test WSL) |
| Project description (schema v2), plan, `apply`, `project create` | done | partly |
| `deploy`, `rollback`, `status`, the `gate` gatekeeper | done | partly |
| Unprivileged agent and the on-request executor | done | partly |
| Web interface: projects (deploy, rollback), firewall, new project form | done | no, not opened in a browser |
| `firewall` plugin (`status`, `mode`, `allow`, `remove`, `enable`) | done | no |
| Signed releases, the documentation site | done | yes |
| `lite init` (users, keys, key login, firewall mode, plugins `caddy`, `podman`, `pg`, `redis`, `security`) | done | partly |
| `lite verify`, `lite info`, `lite --help` (R-6) | done | partly |
| `lite fix` (`verify --autofix`, a button in the interface): only the list of safe fixes; the rest is printed as "manual" | done | partly |
| Refusing to work when logged in as root (R-5) | done | partly |
| Working without root (R-16): administrators run commands without sudo; the CLI hands them to the agent; the executor does only actions from a fixed list; root is needed only for `lite init` and `liteup` | done | yes |
| `lite package` (R-18, R-21, R-22); Docker packages are refused (R-11) | done | partly |
| The plugin mechanism: manifest, external plugins, an interface section (R-19, R-24, R-25) | done | partly |
| `security` plugin (R-23): SSH, fail2ban, automatic updates, sysctl, login journal, checks | done | partly (no real sshd login) |
| Project dependencies on host plugins (R-20): `lite verify` checks podman, pg, caddy from the description | done | yes |
| `unit` component (R-12, R-27): a process on the host under the project user's systemd, a tar.gz release over https with a mandatory minisign signature, blue-green, rollback; fields `kind`, `exec`, `stop`, `data`, `release` | done | yes (container) |
| Server registry (R-26): `lite server add|list|remove`; a project database on another server (`database.server`, `port`, `password_secret`): a network connection string with `sslmode=require` | done | no |
| Plugins `redis`, `pg` (databases, roles, remote access, backups), `podman ps` | done | yes |
| `backup` plugin (R-28): project settings and secrets are encrypted with the owner key (age, the server keeps only the public part); database dumps optionally; storage locations Backblaze B2, S3-compatible and SFTP (rclone), upload to all configured; `fetch`, `restore`, timers, checks | done | partly (backup and restore yes; the storages no) |
| Secret store (R-29): local (files with mode 600 under root) | done; pluggable backends (git, Vault) postponed by decision | |
| Creating a project from the interface (R-17): a form, a plan preview, creation; secrets are encrypted on the owner's computer with the executor public key | done (the form was tested with a DOM double only) | no |
| `compose` (R-27) | through `kind: unit` with `exec` and `stop` (`podman compose up/down`) and a release directory with `compose.yml`; there is no separate kind | no |
| Persistent component directories `data` (R-13, inside one project) | done | yes |
| Interface `lite-ui` (Vue 3, separate program): Overview, Projects with the new project form, Ingress (domains, publish switch, Cloudflare Tunnel), Protection, Server (plugins that are installed and that can be added; updating lite); several servers chosen in the browser | done; checked in a browser against a stand-in for the server | no, not on a real server |
| Updating `lite` (`lite update`, the Update button): the new release is checked, installed by `liteup update` as a separate unit, the previous version is restored if the new one does not start | done | no |
| `cloudflare` plugin: cloudflared as its own user, the token in a 600 file | done | no |
| `lite ssh-key list|add|remove` (change your own key without losing access) | done | no |
| `lite init` protects the server, waits for a real key login, saves the original SSH and firewall state; `liteup uninstall --everything` restores it | done | no |
| Docker (R-11) | forbidden by the owner's decision | |
| Shared directories and sockets between projects (R-13) | postponed by the owner (2026-11-19); needs rootless id checks on a server | |

## 3. What was added without agreement

These are the implementer's decisions, not the owner's. Each needs to be confirmed or removed.

- The commands `firewall status|allow|remove|enable` and the ban on closing ports 80 and 443.
- The internal commands `agent install`, `agent uninstall`, `agent serve`, `exec`, `call`.
- The `--token-file` flag of `ui` and running the interface as a systemd service.

## 4. Requirements (in the owner's words)

**Installation**
- R-1. Installed with one command from the site. The installer installs the minimum: the binary, `minisign`, the agent. There are no separate agent install commands for a person. The installer is `liteup`: `install`, `update`, `reinstall`, `uninstall`, `status` (decided 2026-11-20). The one-liner is the bootstrap `install.sh`, which only downloads `liteup`; `sudo liteup install` then installs lite-deploy. The agent is restarted automatically on install and update.
- R-2. Works on more than Debian and Ubuntu.
- R-3. The command is called `lite`.

**Preparing the server**
- R-4. `lite init` brings the server to a state in which `lite` works. It is the only command that may run as root.
  - `--user names`: existing or new users. New ones are created in a dialog. Entering a public SSH key is mandatory, without it `init` does not complete. `lite` neither sees nor stores the password (it is set by the system `passwd`). The user gets `sudo`.
  - Without `--user` the current user is configured; as root without `--user` the command refuses.
  - `--all-users`: all users with a shell, except system users and project users.
  - SSH password login is disabled for the configured users (the key remains).
  - `--ufw keep|off|http|max`: leave the firewall alone; no filtering; web ports; SSH only. `max` by default.
  - `--plugins list`: turn plugins on.
- R-5. Everything except `init` (and `--help`, `version`) refuses to run as root; only a regular user works (checked by `loginuid`). `lite` does not disable root SSH login: that is the owner's decision.
- R-6. `lite verify`: read-only, a server check and warnings. `lite info`: components, plugins and their state. `lite fix` (and `verify --autofix`): fixes obvious configuration mistakes, only from a list of safe fixes.

**System management**
- R-7. From the interface and from the command line you can install, remove and update system packages and change the firewall settings. These are typed operations; arbitrary scripts are not run. Details are in "Packages and plugins" below (R-18…R-24).
- R-8. An arbitrary shell as root is never run. Steps may be shell commands, but only from a described set.
- R-9. `lite` commands do not duplicate system ones one to one; they work a level higher (a firewall mode, "open a port for a project"), otherwise running a system command over SSH would be simpler.

**Composition**
- R-10. The core: `lite`, `minisign`, the agent. Plugins (installed by `lite init --plugins` or from the interface): `caddy` (incoming traffic, needed for sites), `podman`, `pg` (PostgreSQL), `redis`, `firewall`, `security`, `server`, `backup` and the storages `backblaze`, `s3`, `sftp`.
- R-11. Containers are `podman` and Quadlet only (decided 2026-11-19; Docker is forbidden because access to its daemon is root). `lite package` refuses to install or update Docker packages, `lite verify` warns if Docker is installed. A project may use no containers at all.
- R-12. A project component is a container under systemd or a plain systemd service; both kinds are allowed in one project.
- R-13. Projects may have shared resources: files, directories, sockets. Postponed by the owner (2026-11-19); persistent directories inside one project (`data`) exist.
- R-14. A project may require system dependencies: a service on the host (for example Redis), a package, a library. See R-20.

**Deploys and the interface**
- R-15. A project is deployed and rolled back without downtime (blue-green), and its state is visible. CI deploys through a restricted key.
- R-16. The interface runs on the owner's computer and reaches the server over SSH. An unprivileged agent runs on the server; `sudo` without a password and `sudoers` rules for the interface are not needed. The same holds for the command line: administrators work without `sudo`.
- R-17. Everything from R-4, R-6 and R-7 is available from the interface too.

**Packages and plugins (clarified by the owner on 2026-11-18)**
- R-18. A server can be prepared: install system packages through `lite` (from the CLI and from the interface).
- R-19. A plugin is what adds commands to `lite` (`lite package`, `lite ingress`, `lite pg`, `lite logs` …), a section to the interface and, optionally, checks for `verify`. Any implementation: built-in code, scripts, a separate program. A plugin does not have to be tied to an OS package (for example a log viewer plugin); if it needs packages, it asks `lite package` for them (see R-20). A package and a plugin are different things.
- R-20. A project may require host dependencies (packages and plugins, for example Redis, postgres) and describes them in its description; the check and the deploy take them into account.
- R-21. All packages installed through `lite` are listed by a command (`lite package list`) and in the interface (a journal is kept to tell them from those installed by hand).
- R-22. In the CLI and in the interface you can check whether a package is installed and its version (`lite package check NAME`). The rest: `lite package install|remove|update NAME`.
- R-23. Server protection is the `security` plugin; the whole list is in the first version (decided by the owner on 2026-11-18, the server is production):
  1. SSH: no root login by password; no password login for any user; a list of allowed users (`AllowUsers`).
  2. `fail2ban` against password guessing.
  3. Automatic security updates (`unattended-upgrades`, `dnf-automatic`).
  4. Kernel parameters (sysctl network protection).
  5. A login journal: who logged in and from where, failed attempts.
  6. Checks for `verify`: SSH password login is on, extra ports are open, security updates are available, who has `sudo`, whether `fail2ban` works.

  The firewall stays a separate plugin `firewall`. The plugin is turned on by `lite init --plugins security` or the interface; disabling the password for the configured users moved from `init` into this plugin.
- R-24. All plugins are connected, turned on and shown in `lite info` and `verify` in the same way. `lite package` itself is a plugin too (built-in).
- R-25. How a plugin is described (decided 2026-11-18): a directory with a manifest file (name, commands, needed packages, checks for `verify`) and an executable or scripts that `lite` calls. Built-in plugins ship inside the binary and are described the same way. Plugin scripts run without root, and privileged actions go through the executor from a fixed list (R-8).
- R-26. Servers (decided 2026-11-19): a project lives on one server, foreign resources (a database) are connected by an address from the server registry (`/etc/lite-deploy/servers.json`, name → address on the internal network); every server has its own `lite`. The list of servers to switch between in the interface (name → user@host for SSH) is kept on the owner's computer. There is no scheduling, no moving between servers and no balancing: ingress is a separate server, and the owner assigns servers by hand.
- R-27. A project component is described by how it runs (`quadlet`, `unit` for binaries and everything else; `compose` through `unit`) and how it arrives (an image or a release directory); blue-green, the readiness check and rollback are the same for all kinds. A `unit` release is a tar.gz archive over https with a mandatory minisign signature.
- R-28. Backups (decided 2026-11-19): the `backup` plugin is not tied to Backblaze; storage locations are pluggable (a local directory always; Backblaze B2, S3-compatible and SFTP are locations), and encryption is set per location. Project secrets enter a backup, and encryption is mandatory for them; the owner keeps the key off the server. Dumps of databases are encrypted optionally (`backup setup … --encrypt-dumps`).
- R-29. Secret stores (decided 2026-11-19): the secret layer is pluggable (local files, later an encrypted git, HashiCorp Vault as one more backend). Only the local store exists now.
- R-30. Publishing (decided 2026-11-20): a site is served either through a tunnel (`publish: tunnel`, Caddy on 127.0.0.1 only) or directly (`publish: direct`: Caddy also serves the host on 80/443 with its own automatic TLS certificate). The two are switchable with `lite publish PROJECT SITE MODE`, so a Cloudflare outage can be bypassed. `lite verify` warns when a direct site is blocked by the firewall.
- R-31. The interface is a separate program (decided 2026-11-20): `lite-ui` runs on the person's computer (Windows, macOS, Linux/WSL), reaches the server over SSH, keeps a list of servers that the person adds and chooses in the browser, and is installed with one command (`lite-ui.ps1` or `lite-ui`). It is never installed on the server, and the server opens nothing to the internet for it. The server side (`lite-deploy`, `liteup`) is Linux only.
- R-32. Protection by default (decided 2026-11-20): `lite init` applies the `security` plugin (SSH by key only for everyone, fail2ban, automatic updates, kernel parameters) unless `--no-security`.

## 5. Limits of the first working version (v1)

The goal of v1: a bare Ubuntu server → install → `lite init` → a project is created and deployed from the interface. Everything checked on a real server.

**In**
1. Installation (R-1) on Ubuntu and Debian; other distributions without a guarantee.
2. The name `lite` (R-3); `lite-deploy` stays as a link for compatibility.
3. `lite init` with `--user`, `--all-users`, `--ufw` (R-4) and the root ban (R-5).
4. `lite verify` and `lite info` (R-6).
5. Projects on `podman` containers and `unit` components: creation, deploy, rollback, state (R-15).
6. The interface through the agent (R-16): projects, the firewall mode, the result of `verify` and `info`.

**Second line (right after v1)**
- Packages from the interface (R-7), "open a port for a project" (R-9).

**Not in v1**
- A guarantee on RHEL, Fedora, Arch, openSUSE (R-2).
- Docker (R-11, forbidden), shared resources between projects (R-13).
- Confirming dangerous actions with a signature, MCP for AI, a Cloudflare tunnel, phone access, scheduling across servers.

## 6. Decided

- Checking is done on the owner's Ubuntu server.
- The installer installs only the minimum without flags: the binary, `minisign`, the agent (R-1). Caddy is a plugin (decided 2026-11-17).
- There is no `--setup` flag in `lite init`.
- The default firewall mode is `max` (SSH only); the owner switches it to `http` when sites are needed, or uses `keep` on an already configured server.
- The commands `firewall allow/remove/enable` are replaced by the mode and project ports (R-9).
- The installer is `liteup` (2026-11-20), the way `rustup` is for Rust; the agent restarts on install.
- All user-visible text, code comments and documentation are in English (2026-11-20).

## 7. Order of work up to v1

1. A run on a server of what is written: `liteup`, `project create`, `deploy`, the interface. Fix what is found. Repair CI.
2. Check the pieces that only doubles have seen: ufw, a real SSH login, a Quadlet container start, the storages, the form in a browser.
3. A full end-to-end check on a clean server following the quickstart.
