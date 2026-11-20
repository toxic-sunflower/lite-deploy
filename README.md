# lite-deploy

**lite-deploy** sets up a Linux server and deploys your projects to it, with no root needed for daily work. Quadlet containers or plain systemd services, Caddy and PostgreSQL on the host, blue-green deploys without downtime, encrypted backups, and a restricted SSH key for CI.

**New here?** Follow the [Quickstart](docs/quickstart.md), then pick a tutorial: [your first project](docs/first-project.md), [publishing a site](docs/publish-a-site.md), [deploying from CI](docs/ci.md), [backups](docs/backups.md), [protecting the server](docs/protect-the-server.md). This page is the reference for installing and for every plugin. Maintainers: see [CONTRIBUTING.md](CONTRIBUTING.md).

## Install

You need Linux with systemd (amd64 or arm64). One command, as root:

```sh
curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh
```

This installs `liteup`, the tool that installs and manages `lite-deploy` (the way `rustup` does for Rust), and with it:

- `lite-deploy` in `/usr/local/bin` with the short name `lite` (the binary comes from GitHub Releases; the signature and the checksum are verified);
- `minisign` to verify the signature;
- the agent: an unprivileged `lite-deploy` service user and systemd sockets (the interface works through it).

Users, the firewall, Caddy, `podman` and PostgreSQL are not touched: `lite init` sets them up (below), and then the interface.

Later, as root, `liteup` manages everything:

```sh
sudo liteup update                      # move to the latest release; the agent is restarted automatically
sudo liteup install --version vX.Y.Z    # a specific version (the numbers are on the Releases page)
sudo liteup reinstall                   # uninstall and install the same version again
sudo liteup uninstall                   # remove lite and the agent; settings, secrets and backups stay
sudo liteup uninstall --purge --yes     # also delete settings, secrets and backups (irreversible)
sudo liteup uninstall --everything      # the server back to bare: projects, databases, Caddy, PostgreSQL... (asks to type the server name)
liteup status                           # installed and latest versions
```

The installer takes the **latest release** by default. It has been tested on Ubuntu; on Debian, Fedora, RHEL, openSUSE and Arch packages are installed with their package manager, but the installation was not tested there. The scripts are short and can be read before running: [site/install.sh](site/install.sh) (downloads `liteup`) and [site/liteup](site/liteup). 

## The interface on your computer: lite-ui

The web interface is a separate small program, `lite-ui`, that lives on **your own computer** and reaches the server over SSH with your key. It is never installed on the server, and the server opens nothing to the internet for it. Install it with one command:

```powershell
# Windows (PowerShell)
irm https://lite-deploy.prozorov.me/lite-ui.ps1 | iex
```

```sh
# WSL, Linux, macOS: no root, goes into ~/.local/bin
curl -fsSL https://lite-deploy.prozorov.me/lite-ui | sh
```

Then, in a new terminal, give it your server once (it is remembered):

```sh
lite-ui --host YOUR_USER@YOUR_SERVER
```

The page opens in your browser at an address like `http://127.0.0.1:8787/?t=…`; only your computer can reach it. This needs the OpenSSH client (built into Windows 10 and later and into macOS) and a key login to the server.

## Prepare the server

After installing, run `lite init` once on the server (as root; the only command that works when logged in as root):

```sh
sudo lite init --user YOUR_USER
```

The command first shows what it will do and asks for confirmation. Then it:

- creates the user if it does not exist (`--user` also accepts existing users; several names separated by commas; `--all-users` takes every regular user with a shell);
- if the user has no password, runs the system `passwd`: the password is typed there, `lite` never sees it;
- gives administrator rights (group `sudo` or `wheel`);
- asks for a public SSH key and adds it; without a key the setup does not continue;
- sets up the firewall: `--ufw max` (the default, only SSH is open), `--ufw http` (SSH and the web ports 80 and 443), `--ufw off` (no filtering), `--ufw keep` (leave an existing firewall alone). The previous `ufw` rules are reset with `max` and `http`;
- installs plugins: `--plugins caddy,podman,pg,redis,backblaze` (the name `postgres` is accepted as `pg`). Caddy is needed for project sites: it is installed from the official repository, the main Caddyfile includes the sites from `/etc/caddy/conf.d`, and the old Caddyfile is saved as a copy;
- **protects the server** (unless you pass `--no-security`): SSH login by key only for everyone (root by key only), fail2ban, automatic security updates and protective kernel parameters; the SSH setting is checked with `sshd -t` and the previous one is restored on an error.

Before it closes password login, `init` waits (up to 5 minutes) until you have logged in with your key from a new terminal; if no login is seen it leaves password login on and tells you how to finish later (`lite security enable`). With `--yes` it does not wait. After `init` log in as the regular user. Daily work needs **no root and no sudo**: if a command needs privileges, the CLI hands it to the agent (see "Working without root"). When logged in as root, the commands other than `lite init` and `lite --help` refuse to run.

Without `--user`, the user who ran `sudo lite init` is configured. `lite` is the short name of `lite-deploy`; `lite --help` lists everything.

## Deploy and the interface

After `project create`, a project is deployed by commit hash (the image `<image>:<hash>` must be in the registry):

```sh
lite deploy PROJECT FULL_40_CHARACTER_HASH
lite status
lite rollback PROJECT
```

A site is published through a Cloudflare Tunnel (`"publish": "tunnel"`, the default) or directly, with Caddy getting its own TLS certificate (`"publish": "direct"`). Switch at any time: `lite publish PROJECT SITE tunnel|direct` ([tutorial](docs/publish-a-site.md)).

The web interface with the "Deploy" and "Roll back" buttons is `lite-ui` (see above): `lite-ui --host admin@YOUR_SERVER`. It listens on `127.0.0.1` of your computer only.

The server must have the agent: `liteup` installs it. The agent runs unprivileged and listens only on the local socket `/run/lite-deploy/api.sock` (nothing is opened to the internet). It hands every action to the executor, which systemd starts as root for each request; the executor accepts only a fixed list of actions (`status`, `deploy`, `rollback`, `info`, `verify`, `fix`, plugin commands, project creation) and exits. System administrators (group `sudo` or `wheel`) can use it; no `sudo` password and no `sudoers` rule are needed for the interface. The caller is identified by the kernel (`SO_PEERCRED`), every request is written to the agent journal `/var/lib/lite-deploy-agent/audit.log`, and the executor writes to the systemd journal (`journalctl -u 'lite-deploy-exec@*'`).

## Working without root

Root and `sudo` are not needed for daily work. An administrator (group `sudo` or `wheel`) runs commands as a regular user: if a command needs privileges, the CLI hands it to the agent and the executor (started by systemd as root for one command) runs it. The executor accepts only a fixed list of actions and validates the parameters; an arbitrary root shell is never executed. Passwords and keys for such commands travel through stdin (`echo PASSWORD | lite pg password ROLE --password-stdin`) and never appear in arguments or the process list. `verify`, `deploy`, `rollback` and `status` also go through the agent when run by an administrator.

Root is needed only for `lite init` (the first server setup) and for installing with `liteup`. Examples in this document written with `sudo` work without it too: `sudo` can be left out.

## Plugins

A plugin adds commands to `lite`, a section to the interface and checks for `lite verify`. Plugin commands look the same everywhere: `lite PLUGIN COMMAND [arguments]`. Commands that change the system ask for confirmation (`--yes` or an answer in the terminal). The same is available in the interface: the "Packages and plugins" section is built from the plugin list.

```sh
lite info                  # plugins and their commands (the same in the interface)
lite plugin list
lite verify                # check the server: read-only, exit code 8 on errors
lite fix --dry-run         # what can be fixed automatically
```

Built-in plugins: `package`, `security`, `firewall`, `caddy`, `podman`, `pg`, `redis`, `server`, `backup`, `backblaze`, `s3`, `sftp`. Your own plugin is a directory `/etc/lite-deploy/plugins/NAME/` (owned by root, not writable by others) with a `plugin.json` file and scripts or programs that `lite` calls:

```json
{
  "name": "logs",
  "description": "log viewer",
  "requires": ["jq"],
  "commands": [{"name": "tail", "help": "last lines", "args": "SERVICE", "exec": "bin/tail.sh"}],
  "checks": [{"name": "size", "exec": "bin/size.sh", "fix": "lite logs clear"}]
}
```

A command receives its arguments as they are and its output goes to the user; a non-zero exit code is an error. A check exits with 0 (ok), 1 (warning) or anything else (error); the first output line is the message. The packages in `requires` must be installed, otherwise the command suggests `lite package install`. A plugin runs with `lite` rights (root, when privileged), so the plugin directory must belong to root: only the server owner can install plugins.

### Packages

```sh
lite package list              # what was installed through lite (the journal /var/lib/lite-deploy/packages.json)
lite package check nginx       # whether a package is installed, its version, through lite or not
lite package install nginx redis-server
lite package update nginx
lite package remove nginx      # the ssh server, sudo, systemd and the shell cannot be removed
```

Works with `apt`, `dnf`, `zypper` and `pacman`. Docker packages are never installed (see "What is not done").

### Server protection (the security plugin)

```sh
lite security enable            # everything at once, SSH last
lite security status            # the protection checks
```

What `enable` does (each step is also available on its own):

- `lite security ssh [no-root]`: SSH login by key only for every user, root by key only (`no-root`: root cannot log in at all), a limit on attempts. The setting goes to `/etc/ssh/sshd_config.d/01-lite-security.conf` (before `50-cloud-init.conf`: sshd takes the first value). Before writing, it checks that an administrator with a key remains and that you have one; after writing, `sshd -t` and the effective `sshd -T` values are checked; on any failure the previous state is restored, so that the server is never locked out;
- `lite security allow-users NAME… | any`: allow only the listed users over SSH (project users that CI logs in as are added automatically; `any` removes the restriction);
- `lite security fail2ban`: installs and configures `fail2ban` for SSH (5 attempts in 10 minutes, a one-hour ban); `lite security banned` and `lite security unban ADDRESS`;
- `lite security updates`: automatic security updates (`unattended-upgrades` on apt, `dnf-automatic` on dnf);
- `lite security sysctl`: protective kernel parameters;
- `lite security logins [COUNT]`: recent logins and failed attempts.

`lite verify` checks: root and password login over SSH, empty passwords, the user restriction, `fail2ban`, automatic updates and pending security updates, kernel parameters, administrators and their keys, and ports that listen to the outside (and whether the firewall closes them).

### Firewall

The `firewall` plugin (the "Firewall" section of the interface): `lite firewall status`, `mode off|http|max`, `allow PORT/tcp [--from ADDRESS]`, `remove PORT/tcp`, `enable`. It shows the `ufw` rules and opens and closes ports. The SSH, 80 and 443 ports cannot be closed, and every change is confirmed. `ufw` must be installed on the server (`lite init --ufw …` installs it).

## Databases, Redis and containers

- `lite pg create-db DATABASE [ROLE]`: creates a role and a database; a project may have several databases. The role password is written to `/etc/lite-deploy/secrets/pg-ROLE` (readable by root only) and does not change on a repeated call. `lite pg drop-db DATABASE` deletes a database. `lite verify` warns if PostgreSQL listens on more than localhost.
- `lite redis install|status`: Redis on the host; `lite verify` reports an error if bind is not localhost only.
- `lite podman ps [PROJECT]`: project containers (each project has its own rootless podman); `lite verify` checks linger for project users.

You can set a role password yourself: `echo PASSWORD | lite pg create-db DATABASE ROLE --password-stdin` or `... lite pg password ROLE --password-stdin` (16–128 characters `A-Za-z0-9_.~-`). The password goes through stdin and never appears in arguments or the process list.

### PostgreSQL access from other servers (on the server with the database)

```sh
sudo lite pg listen 10.0.0.2                       # also listen on the server internal address (restarts PostgreSQL)
sudo lite pg allow shop shopapp 10.0.0.5           # let the role reach the database from this address: ssl, password, port 5432 only for it
lite pg access                                     # who has access
sudo lite pg deny shop shopapp 10.0.0.5            # remove
```

The rules live in a separate block of `pg_hba.conf`; on an error in the file the previous content is restored. The whole internet (`0.0.0.0/0`) is not accepted.

## Several servers

A project lives on one server and each server has its own `lite`. Foreign resources are connected by an address from the server registry (addresses on the internal network):

```sh
sudo lite server add db1 10.0.0.2       # on the project server: the name and address of the database server
lite server list
```

A database on another server is described in the project like this:

```json
"database": { "engine": "postgres", "server": "db1", "port": 5432, "password_secret": "secret://db_password", "connection_format": "url" }
```

The role and the database are named after the project (hyphens replaced by `_`). On the database server the owner creates and opens them:

```sh
echo PASSWORD | sudo lite pg create-db shop shop --password-stdin
sudo lite pg listen 10.0.0.2
sudo lite pg allow shop shop PROJECT_SERVER_ADDRESS
```

The same password is given to the project as the secret `db_password`. The connection string goes over the network only encrypted (`sslmode=require`).

## Components without containers (`kind: unit`)

A component can be a process on the host: a binary, Node, .NET. In the project description:

```json
"services": {
  "api": {
    "kind": "unit",
    "exec": ["./bin/app", "--quiet"],
    "host_ports": [18081, 18082],
    "release": { "url": "https://dl.example.com/app-{sha}.tar.gz", "minisign_key": "RWQ…", "token_secret": "secret://release_token" },
    "health": { "path": "/health/ready" }
  }
}
```

`deploy HASH` downloads the archive (https only, `{sha}` is replaced with the hash) into the free slot directory of the project user, starts that slot's systemd unit, waits for readiness and switches Caddy; a rollback works as for containers. The slot port is passed in the `PORT` variable. `exec[0]` is relative to the release directory unless it starts with `/`; `$` and `%` in arguments are not expanded. The signature is mandatory: CI signs the archive with a minisign key (`minisign -Sm app-HASH.tar.gz`, putting `app-HASH.tar.gz.minisig` next to it), and the project description holds only the public key. Before unpacking, `deploy` verifies the signature (`minisign -Vm`); on a mismatch the release is rejected, the slot is not started and traffic is not switched.

### compose and persistent data

There is no separate `compose` kind: `kind: unit` replaces it. The release archive contains `compose.yml`, and the commands are:

```json
"api": {
  "kind": "unit",
  "exec": ["/usr/bin/podman", "compose", "up"],
  "stop": ["/usr/bin/podman", "compose", "down"],
  "host_ports": [18081, 18082],
  "data": ["uploads"],
  "release": { "url": "https://dl.example.com/app-{sha}.tar.gz", "minisign_key": "RWQ…" }
}
```

In `compose.yml` the port is published as `127.0.0.1:${PORT}:8080`. The `data` field creates persistent directories shared by the blue and green slots that survive deploys: a process sees them in the `LITE_DATA_<NAME>` variables, a container at `/data/<name>`.

## Creating a project in the interface

The interface (`lite-ui`) has a "New project" section: the name, the database (on this or another server), the image registry, the components (a container or a process on the server), the sites and the CI key. "Preview plan" builds the plan and shows the steps with no changes on the server; "Create project" applies it. Secret values are typed in the form and encrypted on your computer with the executor public key: only ciphertext goes to the server, and the values never enter the project description. If the description changed after the preview, creation is rejected and asks for a new plan.

From the command line the same is done by `lite project create FILE` (it shows a preview and asks for confirmation).

## Fixing what was found

```sh
lite verify                # read-only
lite fix --dry-run         # what will be fixed automatically
lite fix                   # the same as verify --autofix; runs only the safe fixes
```

Automatic fixes cover SSH, automatic updates, sysctl and fail2ban, the backup timers and installing the plugins projects need. The firewall, users, keys, package changes and anything that needs a value from you are only listed as "manual".

## Backups

Project secrets lie on the server in clear text (files with mode 600 under root), so they enter a backup only encrypted with a key whose private part only you have. Losing the server does not lose the key, and a leaked bucket does not reveal the secrets.

```sh
age-keygen -o lite-backup.key                    # on your computer; keep the file off the server
lite backup setup age1...                        # the public part from the age-keygen output
lite backup run                                  # settings + secrets (encrypted), pg databases, upload to the configured locations
lite backup schedule on                          # daily around 03:00
```

Without a key `lite backup run` refuses, and `lite verify` reports an error if the server has secrets but backups are not configured. The executor key and the storage keys are not part of a backup: they are issued again on a new server.

Database dumps can be encrypted too, and `lite pg backup` / `lite pg restore` work on their own:

```sh
lite backup setup age1... --encrypt-dumps        # dumps are encrypted as well; the server keeps only the public part
lite pg backup [DATABASE]                        # a backup in /var/backups/lite-deploy/pg, the latest 7 per database are kept
lite pg schedule on                              # daily around 03:30 (a systemd timer)
lite pg backups                                  # the list
lite pg restore DATABASE FILE                    # restore (the current data is replaced; the database must exist)
lite pg restore DATABASE FILE --as OTHER_DB      # restore into another existing database, for example to test a backup
lite pg restore DATABASE FILE.age --identity-stdin < lite-backup.key
```

`lite verify` warns if a database has no backups and reports an error if the latest is older than 36 hours. Only the databases of `lite` projects (and those created with `lite pg create-db`) are checked; the others are counted in one line.

### Storage locations outside the server

The locations work the same way (commands `install`, `setup`, `status`, `upload`, `list`, `fetch`, `schedule`); `lite backup run` uploads to all configured ones. Uploads use `copy`, never `sync`, so rotating local backups does not erase them at the storage. Retention in the bucket is set by the bucket own rules.

| Plugin | Setup (keys only on stdin) |
|---|---|
| `backblaze` | `printf '%s\n%s\n' KEY_ID KEY \| lite backblaze setup BUCKET [PREFIX] --yes` |
| `s3` (AWS, Wasabi, MinIO, R2…) | `printf '%s\n%s\n' KEY SECRET \| lite s3 setup https://ADDRESS BUCKET [PREFIX] --yes` |
| `sftp` | `{ cat id_ed25519; echo ---; ssh-keyscan -t ed25519 HOST; } \| lite sftp setup HOST USER DIRECTORY --yes` |

For SFTP the host key is mandatory: without `known_hosts` the connection is not opened. The storage key sits in `/etc/lite-deploy/<plugin>/rclone.conf` (0600, root). Database dumps that are not encrypted travel to the bucket as they are: turn on `--encrypt-dumps` or do not send them to a bucket you do not trust.

### After losing the server

```sh
sudo lite init ...                               # a new server, plugins
sudo lite backblaze install                      # issue new Backblaze keys in the console
printf '%s\n%s\n' ID KEY | lite backblaze setup BUCKET PREFIX --yes
lite backblaze fetch config                      # download the settings backups
cat lite-backup.key | lite backup restore config-DATE.tar.gz.age --identity-stdin --yes
lite backblaze fetch pg
lite pg create-db DATABASE ROLE                  # then: lite pg restore DATABASE FILE
```

After this, `deploy` of each project brings it back.

## What is not done

- **Docker.** Forbidden: access to the Docker daemon is root on the server and breaks the isolation of project users. Containers are `podman` and Quadlet only; `lite package` does not install Docker packages, and `lite verify` warns if it is installed.
- **Shared directories and sockets between projects (postponed).** Every project has its own user and its own user ids in rootless podman; shared access would need groups and id mapping, and without checking on a real server it is not safe. The data of one project (`data`) works.

## License

MIT, see [LICENSE](LICENSE).
