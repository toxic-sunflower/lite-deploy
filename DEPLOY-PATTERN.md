# The deploy pattern: Actions builds, the server receives the finished result, the key is restricted by a gatekeeper

Invented and checked on a reference project (2026-10-23). Suitable for any project on a shared server.

## The idea

1. **GitHub Actions** builds the images and puts them into a private registry (GHCR) tagged with the full commit hash. There are no sources and no git on the server.
2. Actions logs in to the server over SSH with a **restricted key** and sends only the command `deploy <hash>`. The deploy script and the service descriptions are on the server in advance and are installed by hand (the decision of 2026-10-24: a script that was sent would run as the project user, that is, the key could run any code). Later a separate deployment system automates the manual installation (VISION.md).
3. **The gatekeeper** (`lite-deploy gate`) sits in `authorized_keys` as a forced command (`command="...",restrict ssh-ed25519 ...`). It allows only `deploy <hash>`, `rollback`, `status`, checks the hash (`^[0-9a-f]{40}$`) and runs the deploy script installed on the server. The key has no console.
4. **Blue-green** on the server: two slots on local ports; the new version comes up in the free slot, passes the `/health/ready` check (the answer carries the version = the commit hash), and only then Caddy switches to it (the upstream file + `systemctl reload caddy`). The old slot stays for an instant rollback.

## Why this is safe and simple

- A stolen deploy key gives only "deploy a version" or "roll back", but not a console and not reading secrets.
- The gatekeeper is installed on the server once by hand and changed only by hand: the key cannot extend its own rights.
- The application secrets live on the server (a file with mode 600), not in the repository and not in the images.
- To switch Caddy the project user has one `sudoers` rule: only `systemctl reload caddy`.
- Every project has its own system user without a password and without `sudo`, its own rootless podman, and its own role and database in PostgreSQL.

## What is needed on the server once (per project)

A user (`adduser --disabled-password`), `loginctl enable-linger`, a PostgreSQL role and database (container access through a unix socket), settings files, the gatekeeper, the key in `authorized_keys`, the `sudoers` rule, a login to GHCR with a read-only token, the site in `/etc/caddy/conf.d/`, and the `upstreams` directory writable by the project user.

## Pitfalls already stepped on

- Rootless podman from `sudo -u` does not work: a session is needed (`machinectl shell user@ ...`; `lite` uses `systemd-run --machine=USER@.host --user`).
- Create directories inside another user's home directory as that user, otherwise the intermediate ones become owned by root.
- Database migrations must be backwards compatible (two versions run on one database during the switch). A rollback returns only the code.
- File uploads through the free Cloudflare plan are limited to 100 MB.
- Test the gatekeeper in a sandbox (its own `HOME`, a `systemctl` stub, foreign commands and wrong hashes). The test was `.gate-test.sh` (14 checks), written for the earlier variant with an archive; revisit it after the move to manually installed scripts.
- GitHub branch protection (rulesets and classic) is unavailable for a **private** repository on the free plan: the API answers 403 "Upgrade to GitHub Pro or make this repository public". Without it you cannot forbid direct pushes, force pushes and deleting `main`, and anyone who can write to the repository can start a deploy. The options: GitHub Pro (paid), make the repository public (not suitable for closed code) or work without protection and rely on 2FA and on restricting the `production` environment to the `main` branch. Environments with branch restrictions do work.
- The deploy switch is separate for each environment: a repository variable named after the environment (`DEPLOY_PRODUCTION_ENABLED`, `DEPLOY_STAGING_ENABLED`). The `if` of a job does not see GitHub environment variables; the check inside the steps happens only after entering the environment.
- Branch names: you cannot have `release` and `release/1.0` at the same time (git stores branches as files and directories).
- Free GitHub: about 2000 Actions minutes a month and about 500 MB of private GHCR packages per account. Images tagged with a hash pile up, so periodic cleanup is needed.
- The `cloudflared` token and the command from the Cloudflare panel (`service install <token>`): the token ends up in a service file readable by everyone and in the process list. On a shared server run the service as a separate user, with the token in a closed file (`EnvironmentFile`, root, 600).
- If a name in Cloudflare DNS had a record from an earlier tunnel, the new route does not overwrite it: **error 1033** while the tunnel status is Healthy. Delete the old record and recreate the route. "Server not found" after that may be the DNS cache on the computer (`ipconfig /flushdns` in PowerShell, not in WSL).
- Caddy: `systemctl reload caddy` does not change the start time in `systemctl status`, so you cannot tell from it whether the config was re-read. After editing the site files: a check as the `caddy` user (`sudo -u caddy caddy validate …`) and `reload`. A sign of a stale config: an empty `200` with the `Server: Caddy` header and without the headers of our site.
- Run the Caddy config check as `caddy`, not as root: otherwise log files owned by root may appear which the running Caddy can then not write to.
- An asterisk in the path of another user's home directory is expanded by the calling user's shell, not by `sudo`: `sudo chown … /home/other/*.x` finds no files. Use `sudo sh -c '… /home/other/*.x'`.
- NuGet packages: if the application references a shared project and the database provider pulls a lower dependency version (and the needed version arrives only through a closed `PrivateAssets=all` package), the wrong version ends up in the image and the application crashes at start (`FileNotFoundException` for `Microsoft.EntityFrameworkCore.Relational`). Give the version explicitly in the shared project. The `MSB3277` warning at build time points to this; tests do not catch it, only checking the published image can.
- An application may refuse to start with empty required settings (an application's admin needs a login and a password). Then the slot goes into a restart loop, Caddy answers `502`, and the Cloudflare "Bad gateway" page is seen from outside. Look at `list-units` and the slot journal first. Caddy's answers to a proxy error carry no headers of the site.
- Check the settings files by machine after writing: the set of keys and the length of passwords (without printing values). Pasting several commands into a terminal at once hands the second command to the first one's input (`read`), so give commands one at a time.
- Cloudflare Access: the names of the interface sections change (Authentication, Login methods, Integrations → Identity providers). The error "That account does not have access" means the email is not in the policy or the login method is wrong. The Access session counts from the login and is not extended by activity.
- The Caddy management API must not be turned off (`admin off`): `systemctl reload caddy` calls `caddy reload`, which needs the API. If the API is reachable by containers, move it to a unix socket and check that the reload works.

## Isolating projects on a shared server (a checklist)

We assume that one project can be hacked. Then the others must not suffer.

**PostgreSQL (a shared server, each project has its own role and database):**
- The role is `LOGIN` only, without `SUPERUSER`, `CREATEDB`, `CREATEROLE`. Revoke `PUBLIC` access to the project database.
- The password is long and random (`openssl rand -base64 24`), separate for each project, and lies only in the project file with mode 600.
- `pg_hba.conf` has a separate rule for the pair "project role + project database" (`scram-sha-256`), above the general `local all all peer`. In the project rules do not use `all` either in the database or in the role.
- Mounting the whole socket directory is fine: there is one socket for the whole Postgres server, and the boundary is set by roles, passwords and `pg_hba.conf`, not by the file. Mounting a single socket file is harmful: after a Postgres restart the container is left with a dead link.
- What remains for someone who hacked a project: guessing the passwords of other roles over the socket. The protection: long random passwords. Connecting as another role without a password is impossible (`peer` requires the OS user name to match the role).

**Linux and containers:**
- Every project has its own user without a password and without `sudo` (except one `systemctl reload caddy` rule), and the home directories are closed to each other (750 or 700).
- The container: not root, `DropCapability=ALL`, `NoNewPrivileges=true`, a read-only file system.
- Resource limits in the units (`MemoryMax`, `PidsLimit`), so that one project does not eat the whole server.
- Secrets, passwords and deploy keys share nothing between projects.

**The Cloudflare tunnel (if used):** the service runs as a separate user without rights, the token is in a closed file (root, 600), the sites listen on `127.0.0.1` only, Cloudflare Access on service names (the admin panel).

**To check on the server (after the first deploy):** whether a container reaches `localhost:2019` (the Caddy API) and other host ports; that a project role cannot connect to other databases (including `postgres`); look through `pg_hba.conf` for rules with `all`.
