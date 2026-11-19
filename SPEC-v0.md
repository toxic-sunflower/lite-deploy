# Specification v0: lite-deploy

> This is the v0 specification written in session S2 (2026-11-03). The implementation has grown since: the project schema is now `lite-deploy/project/v2` (v1 descriptions are still accepted; see `README.md` and `REQUIREMENTS.md`), there are plugins, the agent, `liteup` and more. The v0 text is kept as the reference of the core model: the plan, the executor actions, the CLI envelope.

Session S2, 2026-11-03. The basis: `DECISIONS.md` (D-01…D-38), `ARCHITECTURE.md`, the reference `projects/shop/`, steps 11–12 and 18 of `WALKTHROUGH.md`. The new decisions of this specification are recorded as D-39…D-46 with the status "proposed in S2": they follow from the accepted decisions, but they should be seen by the owner before S3.

The scope of v0 per D-28: mode A, the commands `project create`, `deploy`, `rollback`, `status`; the CLI with `--json` as the base API; the UI and MCP later on top of the same commands over SSH. Clarifications found while implementing S3: D-48…D-51 in `DECISIONS.md`. Not in it: a daemon, signature confirmations, Docker, an orchestrator, community plugins, installing software on a bare server (it is in S11), a Cloudflare tunnel (S7; until then the route in the tunnel is created by hand).

The readiness criterion of v0: the example project is installed and deployed through lite-deploy instead of manual scripts (section 12).

## 0. Conventions

- All format files: JSON, UTF-8, no comments. Unknown fields are rejected everywhere (D-33).
- JSON Schema draft 2020-12 schemas live in `schemas/`, are generated from the Go types and committed; a test checks that the committed schemas match the generated ones. The command `lite-deploy schema <project|plan|result|error>` prints a schema.
- Messages for people are in English. Identifiers, error codes, and field and action names are in English.
- Time: RFC 3339, UTC.
- "The owner": a person with `sudo` on the server. "The project user": the system user of a project. "The executor": the code that changes the system as root (in v0 it is `sudo lite-deploy apply`, D-31).
- One `lite-deploy` binary for all roles (the CLI, the executor, the gatekeeper), Go without `cgo` and `unsafe` (D-23). The install path: `/usr/local/bin/lite-deploy`, owned by root, mode 755. A project user cannot change it.

## 1. The repository structure (implemented in S3)

```
lite-deploy/
  cmd/lite-deploy/main.go          # the entry point, subcommand parsing
  internal/project/                # project description types, validation, defaults
  internal/plan/                   # plan building, canonical JSON, the hash
  internal/actions/                # one file per executor action: params, Validate, Apply, DryRun
  internal/render/                 # Quadlet, Caddy, sudoers, authorized_keys templates (built in through embed)
  internal/secrets/                # encrypting and decrypting a value (age), the value store
  internal/host/                   # the Runner interface (running commands without a shell), FS; test doubles
  internal/deploy/                 # blue-green: deploy, rollback, status, the gatekeeper
  internal/cli/                    # commands, the JSON envelope, exit codes, the remote mode over SSH
  internal/api/                    # (S6) types and OpenAPI for /v1; in v0 only data types
  schemas/                         # project.schema.json, plan.schema.json, result.schema.json, error.schema.json
  testdata/shop/           # project.json and the reference (golden) files of the generated units and sites
  docs/
  SPEC-v0.md, DECISIONS.md, ...
```

Rules:
- Any external command is run only through `host.Runner` (an argument array, no shell, with a timeout). There are no direct `exec.Command` calls outside `internal/host` (the linter checks it).
- Tests in the cloud environment use doubles of `host.Runner` and a temporary file system; applying to a real system is only on a test server (S4).
- `go test -race ./...`, `go vet`, `staticcheck`, the schema check and the golden file check in CI.

## 2. The project description (`project.json`)

The only input of `project create`. The schema: `schemas/project.schema.json`, the identifier `lite-deploy/project/v1`.

### 2.1. Fields

| Field | Type | Required | Rules |
|---|---|---|---|
| `schema` | string | yes | exactly `"lite-deploy/project/v1"` |
| `name` | string | yes | `^[a-z][a-z0-9-]{1,30}$`; not a reserved one (`root`, `postgres`, `caddy`, `cloudflared`, `lite-deploy`, `www-data`, `daemon`, `systemd-*`, and also any name equal to an existing user we do not manage, unless `adopt` is set, see 4.2) |
| `description` | string | no | up to 200 characters, one line |
| `adopt` | boolean | no, `false` by default | "take over what was already created by hand" (section 4.2) |
| `database` | object | no | if absent, no database is created |
| `registry` | object | no | a login to the image registry |
| `services` | object name → service | yes | 1…8 services; the name `^[a-z][a-z0-9-]{0,15}$` |
| `sites` | array | yes | 1…8 sites |
| `deploy_key` | object | no | the CI key for the gatekeeper |

`database`:
- `engine`: only `"postgres"` (in v0).
- `connection_format`: `"npgsql"` (`Host=/var/run/postgresql;Database=..;Username=..;Password=..`), `"url"` (`postgresql://role:password@/database?host=/var/run/postgresql`) or `"libpq"` (`host=/var/run/postgresql dbname=.. user=.. password=..`). `"url"` by default.
- The role and database names are derived from `name` by replacing `-` with `_` (for `shop` this gives `shop`) and are not set in the schema. This removes a whole class of input errors.

`registry` (needed if the images are private):
- `host`: `^[a-z0-9.-]+(:[0-9]{2,5})?$`, for example `ghcr.io`.
- `username`: `^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`.
- `token_secret`: a reference `secret://name` (section 7). A read-only token.

`services.<name>`:
- `image`: the image name without a tag, `^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+$` (for example `ghcr.io/toxic-sunflower/shop-api`). At deploy time the tag is always the full commit hash (D-28, the scheme from `DEPLOY-PATTERN.md`).
- `container_port`: 1…65535.
- `host_ports`: optional, an array of two numbers `[the blue slot port, the green slot port]` in the range 18000–18999 (the range changes in `config.json`). If not set, the executor picks the nearest free pair and records it in the state (section 9).
- `health`: an object `{ "path": "/health/ready", "version_field": "version", "timeout_seconds": 90 }`. The defaults are the same. The contract: GET `path` on the slot port returns 200 and JSON in which the field `version_field` equals the full hash of the commit being deployed.
- `limits`: `{ "memory": "768m", "pids": 512 }`. `memory`: `^[1-9][0-9]*[mg]$`, at most 16g; `pids`: 16…4096. The defaults: `512m` and `256`.
- `env`: an object name → string (non-secret values). The name `^[A-Za-z_][A-Za-z0-9_]*$`, the value without a line break, up to 1024 characters.
- `secrets`: an object variable name → a reference `secret://name`.
- `database_env`: the name of the variable where the connection string goes (needed if the service uses the database). For example `ConnectionStrings__Default`.
- A service cannot set: the user, privileges, capabilities, volumes, the network, the start command. These settings are fixed by the Quadlet template (section 4.3): not root, `DropCapability=ALL`, `NoNewPrivileges=true`, `ReadOnly=true`, `Tmpfs=/tmp`, the port only on `127.0.0.1`, the volume only the PostgreSQL socket directory (if `database_env` is set). If another mode is needed, it is a separate schema version, not a "dangerous flag" field.

`sites[]`:
- `name`: `^[a-z][a-z0-9-]{0,15}$`, unique in the project.
- `host`: a DNS name in lowercase.
- `service`: the name of a service from `services`.
- `listen_port`: the Caddy port on `127.0.0.1` (in the range 18000–18999; a free one is chosen by default). Sites listen on `127.0.0.1` only (the decision about the tunnel, `DEPLOY-PATTERN.md`).
- `publish`: `"tunnel"` (the only value in v0: the site waits for a tunnel, and the route is created by hand or in S6).
- `trusted_proxy`: `"cloudflare"` (then `X-Forwarded-For` is taken from `CF-Connecting-IP`) or `"none"` (the default).
- `max_body`: a size, `^[1-9][0-9]*(KB|MB)$`, `5MB` by default, at most `1000MB`.
- `routes`: an optional array `{ "path": "/api/vault/upload", "max_body": "100MB" }` for special request body limits. `path` starts with `/`, without spaces and without `{}`.

`deploy_key`:
- `public_key`: a public key string `ssh-ed25519 AAAA… comment` (ed25519 only, one key, without options; the options are added by the template).

### 2.2. Validation

It is done by a single function `project.Validate`, the same for the CLI, the UI and MCP. The answer: a list of errors `{ "path": "/services/api/image", "code": "pattern", "message": "..." }`. Beyond the schema:
- every `service` reference in the sites exists;
- `database_env` is set only if there is a `database`;
- site names and ports do not repeat, and `host_ports` and `listen_port` do not overlap between the services and sites of a project;
- the `secret://name` references in `secrets` and `registry.token_secret`: the name `^[a-z][a-z0-9_]{1,63}$`, not starting with `_` (such names are reserved for internal secrets, for example `_db_password`);
- the same host in two sites is forbidden;
- the file size is up to 64 KiB.

### 2.3. An example (a sketch for the example project)

The names of the shop secrets (`Shop__*`) should be checked against `app.env` on the server; the principle is shown here.

```json
{
  "schema": "lite-deploy/project/v1",
  "name": "shop",
  "adopt": true,
  "database": { "engine": "postgres", "connection_format": "npgsql" },
  "registry": { "host": "ghcr.io", "username": "toxic-sunflower", "token_secret": "secret://ghcr_read_token" },
  "services": {
    "api": {
      "image": "ghcr.io/toxic-sunflower/shop-api",
      "container_port": 8080,
      "host_ports": [18081, 18082],
      "limits": { "memory": "768m", "pids": 512 },
      "env": { "ASPNETCORE_ENVIRONMENT": "Production", "Shop__UseMock": "false" },
      "secrets": {
        "Auth__SessionSecret": "secret://auth_session_secret",
        "Shop__WebhookSecret": "secret://webhook_secret"
      },
      "database_env": "ConnectionStrings__Default"
    },
    "admin": {
      "image": "ghcr.io/toxic-sunflower/shop-admin",
      "container_port": 8080,
      "host_ports": [18091, 18092],
      "limits": { "memory": "384m", "pids": 256 },
      "env": { "ASPNETCORE_ENVIRONMENT": "Production" },
      "secrets": {
        "Admin__Username": "secret://admin_username",
        "Admin__Password": "secret://admin_password"
      },
      "database_env": "ConnectionStrings__Default"
    }
  },
  "sites": [
    {
      "name": "main", "host": "shop.example.com", "service": "api",
      "listen_port": 18080, "publish": "tunnel", "trusted_proxy": "cloudflare",
      "max_body": "5MB", "routes": [ { "path": "/api/vault/upload", "max_body": "100MB" } ]
    },
    {
      "name": "admin", "host": "admin.shop.example.com", "service": "admin",
      "listen_port": 18090, "publish": "tunnel", "trusted_proxy": "cloudflare", "max_body": "5MB"
    }
  ],
  "deploy_key": { "public_key": "ssh-ed25519 AAAA… github-actions" }
}
```

All services of one project are deployed together: one hash, one slot state, one switch (as the service and the admin of the example project are now). Separate deploys of services are not part of v0.

## 3. The plan (`plan.json`)

The schema: `schemas/plan.schema.json`, the identifier `lite-deploy/plan/v1` (D-34).

```json
{
  "schema": "lite-deploy/plan/v1",
  "id": "01JC3Y8Q4N6R2T7V9K0M5B1XZA",
  "created_at": "2026-11-03T12:00:00Z",
  "expires_at": "2026-11-04T12:00:00Z",
  "server_id": "sha256:…",
  "kind": "project.create",
  "project": "shop",
  "project_hash": "sha256:…",
  "steps": [
    {
      "id": "s03",
      "action": "provision_postgres",
      "class": "normal",
      "params": { "project": "shop", "connection_format": "npgsql" },
      "summary": "Role and database shop in PostgreSQL, a pg_hba rule, the password is created on the server"
    }
  ],
  "hash": "sha256:…"
}
```

The fields:
- `id`: a ULID. `created_at`, `expires_at`: the lifetime is 24 hours by default, at most 7 days. An expired plan is not applied.
- `server_id`: `sha256` of `/etc/machine-id` with the salt `lite-deploy`. A plan can be applied only on the server it was built for.
- `kind`: in v0 `project.create` (creating and updating a project, one command: applying the same description again changes only what differs).
- `project_hash`: the hash of the canonical `project.json` (the one the plan was built from).
- `steps`: up to 200 steps. The `id`s are unique (`s01`, `s02`, …), steps are applied in order. `class`: `safe`, `normal` or `dangerous` (D-29); in v0 all steps are `normal`, and `dangerous` is reserved for future deletion actions.
- `summary`: a human-readable description for the dry-run, the UI and MCP. The executor does **not use** it for decisions; it looks only at `action` and `params`.
- `hash`: `sha256` of the canonical representation of the whole plan without the `hash` field. The canonical form per RFC 8785 (JCS): keys in alphabetical order, no spaces. The hash is visible when applying (D-34); `apply` recomputes it and refuses on a mismatch.

Building: `lite-deploy project plan project.json` on the server (as the owner's regular user, root is not needed: it reads only the non-secret state from `/var/lib/lite-deploy/state/`, section 9). The order of steps for `project.create`:

1. `verify_host`
2. `create_project_user`
3. `set_secret` for `_db_password` (`adopt` mode only, if the owner passed an existing password; D-48)
4. `provision_postgres` (if there is a `database`)
5. `set_secret` (one per each value the owner passes: the ciphertext inside `params`, section 7)
6. `registry_login` (if there is a `registry`)
7. `write_service_env` (per service)
8. `install_quadlet` (per service × the blue and green slots)
9. `reload_user_units`
10. `install_caddy_site` (per site)
11. `install_reload_rule`
12. `reload_caddy`
13. `probe_site` (per site)
14. `install_deploy_key` (if there is a `deploy_key`)
15. `record_project`

Steps are numbered in sequence `s01`, `s02`, …; the numbers in the list above are the order of groups, not ids.

Secrets are in the plan only as ciphertext; ciphertexts are passed to `project plan` with the flags `--ciphertext name=file`, and an extra ciphertext is the error `unused_secret` (D-50). If a needed secret is already written on the server (its name is in the `secrets` field of the project state file, D-49), no `set_secret` step is added for it. If the secret is neither in the plan nor on the server, building the plan ends with the error `secret_missing` with the name of the secret.

### Applying: `sudo lite-deploy apply plan.json`

1. Checks: the schema version, the hash, the expiry, `server_id`, the size (up to 256 KiB), the number of steps.
2. A check of every step: the action is in the executor list (section 4), the `params` pass the action check.
3. Output: the plan id and the hash to the screen (and to the journal).
4. Steps run in order. The first error stops applying; the steps already done stay (there is no rollback, actions are idempotent, applying again continues). The result: a list of steps with the status `changed`, `unchanged`, `skipped` or `failed`.
5. `--dry-run`: the same path but without changes; every step reports what it would do (`would_change` / `unchanged` / `would_fail` with a reason). This is the readable mode of D-37. It needs root because it compares with the project user's files.
6. `apply` without `--dry-run` in an interactive terminal first shows the dry-run and asks to type `yes`; in a non-interactive mode it needs `--yes`.
7. Every run and every step is written to the journal (section 9) before executing and after.

## 4. The executor actions in v0

### 4.1. General rules

- The executor accepts **typed parameters, not the content of files**: it builds the texts of units, Caddy sites, sudoers and `authorized_keys` itself from templates built into the binary (D-42). A client cannot make it write an arbitrary file.
- All paths are computed from the validated `project`, `service`, `slot`, `site`; path parameters are not accepted.
- Commands without a shell. Names are checked before any action; values that end up in command arguments pass the same pattern.
- Idempotence: an action compares the wanted state with the current one and changes only the difference; a repeated run returns `unchanged`.
- Before replacing an existing file that is not ours (no management mark, section 4.2), a copy `<file>.bak-<plan id>` is made.
- The journal: the executor records the action, `project`, the result and the plan hash. Secret values never get into the journal or the output.

The project name is checked everywhere with `^[a-z][a-z0-9-]{1,30}$`. Notation: `P` the project, `U` the system user of `P` (equal to the project name), `H=/home/U`, `ROLE` = the project name with `-` → `_`.

### 4.2. The `adopt` mode

Needed for the v0 criterion: the example project is already set up by hand. If the project has `adopt: true`:
- the existing user `U`, the role and the database are not recreated and are not an error;
- the existing files (units, Caddy sites, env) are compared with the reference; if they differ, the old file is saved as `.bak-<plan id>`, the new one is written, and the difference goes into the output;
- the executor cannot learn the password of an existing role. The options: the owner sets the internal secret `_db_password` in advance (`lite-deploy secret set shop _db_password`, hidden input, the value of the existing password) or the step parameters have `rotate_password: true`: the password is changed to a new one and the env files are rewritten. Without one of the two the `provision_postgres` step in `adopt` mode ends with the error `adopt_password_unknown`;
- the management mark: the file `/var/lib/lite-deploy/state/projects/<P>.json` (created by `record_project`). Without `adopt` a project with an existing user but without the mark is rejected (`project_exists_unmanaged`).

### 4.3. The list of actions

A table: the name, the parameters (all required unless stated), what it does, a repeated run.

**`verify_host`**: `{}`. Read-only. It checks: Ubuntu 24.04 or newer; `podman` (4.4 or newer; below 5.0 the warning "only tested on 5.x"), `caddy`, `postgresql` (if the plan has a database), `loginctl`, `visudo` are installed; `ufw` is active (a warning, not an error); the main `Caddyfile` contains `import /etc/caddy/conf.d/*`; `/etc/caddy/conf.d` exists; Caddy listens for the API on `localhost:2019` (needed for reload, `DEPLOY-PATTERN.md`). The error: `precondition_failed` with a list.

**`create_project_user`**: `{project}`.
1. The user `U` without a password and without `sudo`: `adduser --disabled-password --gecos "" U` (not if it exists).
2. `loginctl enable-linger U`.
3. The mode of `H`: 750.
4. A check of the `/etc/subuid` and `/etc/subgid` ranges for `U` (an error if missing).
5. Creating as `U`: `~/.config/containers/systemd`, `~/.config/lite-deploy`, `~/.local/state/lite-deploy`.

**`provision_postgres`**: `{project, connection_format, rotate_password?: bool}`.
1. If the role `ROLE` does not exist: a password of 24 bytes from `crypto/rand`, base64 without `/+=`; `CREATE ROLE ROLE LOGIN PASSWORD …` (without `SUPERUSER`, `CREATEDB`, `CREATEROLE`), `CREATE DATABASE ROLE OWNER ROLE`, `REVOKE ALL ON DATABASE ROLE FROM PUBLIC`. The password goes to `psql` through the variable `-v pw=…` on standard input, not in arguments.
2. The password is saved as the internal secret `_db_password` (root 600, section 7).
3. A rule in `pg_hba.conf` strictly for the pair: `local ROLE ROLE scram-sha-256`, between the markers `# lite-deploy:begin ROLE` and `# lite-deploy:end ROLE`, above the general `local all all peer`; `systemctl reload postgresql`. Rules with `all` are not created, and `verify_host` gives a warning if `pg_hba.conf` has such rules above the general one.
4. A login check: a connection over the socket as `ROLE` with the password.
A repeat: the role and the database exist, the rule exists: `unchanged`. The password changes only with `rotate_password`.

**`set_secret`**: `{project, name, ciphertext}`. `name` matches `^_?[a-z][a-z0-9_]{1,63}$` (the internal `_…` names are allowed only to the executor; `_db_password` can be set from a plan only in `adopt` mode). It decrypts (section 7), checks the binding of `project` and `name`, and writes the value to `/var/lib/lite-deploy/secrets/<project>/<name>` (owner root, 600, the directory 700). It reports only `changed` or `unchanged` (a comparison of value hashes).

**`registry_login`**: `{project, registry, username, secret}`. It runs `podman login --authfile ~/.config/lite-deploy/auth.json` as `U` with the token on standard input. The `auth.json` file is 600. A repeat: it rewrites only if the token changed.

**`write_service_env`**: `{project, service, env: [{name, kind, value|secret}]}` where `kind` is: `literal` (a value), `secret` (a reference to a name), `database` (the connection string format). It builds the file `~/.config/lite-deploy/env/<service>.env` (owner `U`, 600) from the lines `NAME=value` (without quotes, this is the `EnvironmentFile` format in Quadlet). It takes the secret values from the store; a value with a line break is rejected. A repeat: it compares by the hash of the resulting file.

**`install_quadlet`**: `{project, service, slot, host_port, container_port, memory, pids, database_socket}`. `slot`: `blue` or `green`. It writes `~/.config/containers/systemd/<project>-<service>-<slot>.container` (owner `U`, 600) from a template equivalent to the current the example project units:

```
[Unit]
Description=<project> <service> (slot <slot>)
After=network-online.target
Wants=network-online.target

[Container]
ContainerName=<project>-<service>-<slot>
Image=localhost/<project>-<service>:<slot>
PublishPort=127.0.0.1:<host_port>:<container_port>
EnvironmentFile=%h/.config/lite-deploy/env/<service>.env
Volume=/var/run/postgresql:/var/run/postgresql      # only with database_socket=true
ReadOnly=true
Tmpfs=/tmp
NoNewPrivileges=true
DropCapability=ALL
PidsLimit=<pids>
PodmanArgs=--memory=<memory>   # the Memory= key appeared in Quadlet only in podman 5, PodmanArgs works in 4.x too

[Service]
Restart=always
RestartSec=5
TimeoutStartSec=120

[Install]
WantedBy=default.target
```

The image `localhost/<project>-<service>:<slot>` is created by the deploy with the `podman tag` command (as now); the unit knows nothing about the version.

**`reload_user_units`**: `{project}`. It runs `systemctl --user daemon-reload` in the session of the user `U` (`systemd-run --machine=U@.host --user …` or `machinectl shell`; the way is specified in S4, a plain `sudo -u` does not work for rootless podman, `DEPLOY-PATTERN.md`). Then it checks that the units are visible (`systemctl --user list-unit-files`).

**`install_caddy_site`**: `{project, site, host, listen_port, upstream_initial, trusted_proxy, max_body, routes[]}`.
1. The site file `/etc/caddy/conf.d/<project>-<site>.caddy` (root:root, 644) from a template: `http://<host>:<listen_port>`, `bind 127.0.0.1`, `encode zstd gzip`, headers (`Strict-Transport-Security "max-age=31536000"`, `X-Content-Type-Options nosniff`, `Referrer-Policy strict-origin-when-cross-origin`, `-Server`), `request_body max_size` per `max_body` and `handle` blocks for `routes`, `import /etc/caddy/upstreams/<project>-<site>.caddy`, a log to `/var/log/caddy/<project>-<site>.log` (rotation 50 MiB, 5 files). If `trusted_proxy=cloudflare`, these headers are added by the upstream file (below), as in the current `deploy-script`.
2. The directory `/etc/caddy/upstreams` (owner root, 755) and the file `/etc/caddy/upstreams/<project>-<site>.caddy` (owner `U`, 644). The initial content on first creation: `respond "not deployed" 503`. If the file already exists (the project has been deployed), the content is **not touched**.
3. The directory `/var/log/caddy` (owner `caddy`).
4. It does not reload Caddy (that is `reload_caddy`).

The file names differ from the current ones (`shop.caddy` and `shop-admin.caddy`): with `adopt` the old files with the same `host` are replaced so that there are not two sites on one address (the step reports it).

**`install_reload_rule`**: `{project}`. It writes `/etc/sudoers.d/lite-deploy-<project>` with a single line `U ALL=(root) NOPASSWD: /usr/bin/systemctl reload caddy`. The file is first written to a temporary one, checked with `visudo -cf`, then installed with mode 440. This stays until S6: in mode B the Caddy switch moves to the executor socket and sudoers disappears.

**`reload_caddy`**: `{}`. A configuration check as the user `caddy` (`sudo -u caddy caddy validate --config /etc/caddy/Caddyfile`, not as root: otherwise files owned by root appear, `DEPLOY-PATTERN.md`), then `systemctl reload caddy`. A check error: the project site files written in this apply are returned to the previous state from `.bak` (or deleted if they were new), and no reload is done.

**`probe_site`**: `{host, listen_port, expect}`. A request `GET http://127.0.0.1:<listen_port>/` with the header `Host: <host>`. `expect`: `"not_deployed"` (503 and the body `not deployed`) for a new project, `"deployed"` (any code except 503 `not deployed`, and without an empty `200` with `Server: Caddy` and without the site headers) for a deployed one. It catches the case "Caddy did not pick up the name" (`DEPLOY-PATTERN.md`). The planner chooses `expect` by the state from `state/`.

**`install_deploy_key`**: `{project, public_key}`. It adds one line, marked with the comment `lite-deploy:<project>`, to `~/.ssh/authorized_keys` of the user `U` (owner `U`, 600, the `.ssh` directory 700):

```
command="/usr/local/bin/lite-deploy gate --project <project>",restrict ssh-ed25519 AAAA… lite-deploy:<project>
```

An old line with the same mark is replaced. Lines without the mark are not touched. The key must be ed25519 on one line without options.

**`record_project`**: `{project, project_hash, ports}`. It writes `/var/lib/lite-deploy/state/projects/<project>.json` (644, not secret): `hash`, `ports`, `secrets` (the names of the secrets from the secrets directory, no values), `deployed` (D-49; set by a deploy, S5). This is the management mark (4.2) and the source of the "current state" for the next `plan`. `project.json` itself is copied to `/etc/lite-deploy/projects/<project>.json` (644).

### 4.4. Actions not in v0 (reserved)

`delete_project`, `delete_database`, `delete_site`, `rotate_deploy_key`, `trust_key_add` and `trust_key_remove`, `install_plugin`, `update_agent`: the class `dangerous` (D-29). The v0 executor does not know them and rejects them (`unknown_action`). (Since v0.6 the executor also has `install_unit`, `write_release_auth` and the project-creation and plugin requests; the action list in `internal/actions` is the source of truth.)

## 5. Deploy, rollback, state (`deploy`, `rollback`, `status`)

In v0 these are **not plan steps** but operations of the project user, as in the current `deploy-script`. They were moved to Go (`internal/deploy`) and are called by the gatekeeper. The description below fixes the behaviour that has already been checked by hand and must be kept.

### 5.1. Slots and ports

Two slots `blue` and `green`; every service has one port per slot (`host_ports`). The project state: `~/.local/state/lite-deploy/active` (the slot `blue`, `green` or `none`), `history.log`, the lock `lock`.

### 5.2. `deploy <sha>`

1. `<sha>` must match `^[0-9a-f]{40}$`.
2. A `flock` lock on `~/.local/state/lite-deploy/lock`; if it is taken, exit with code 6 (`deploy_in_progress`). The old script had no such lock, and two simultaneous deploys corrupted the state there.
3. `idle` = the slot opposite to the active one (`blue` if there is no active one).
4. For every service: `podman pull <image>:<sha>` with `--authfile ~/.config/lite-deploy/auth.json`, then `podman tag <image>:<sha> localhost/<project>-<service>:<idle>`.
5. `systemctl --user restart <project>-<service>-<idle>.service` for all services.
6. Waiting for readiness: for every service poll `GET http://127.0.0.1:<slot port><health.path>` every 2 seconds up to `health.timeout_seconds` (90 by default); ready when `version_field` in the answer equals `<sha>`.
7. Not ready: `podman logs --tail 40`, stopping the `idle` slots, writing `deploy-failed`, exit with code 10. Traffic stays on the previous slot.
8. Ready: the switch. For every site `/etc/caddy/upstreams/<project>-<site>.caddy` is written (a temporary file and `mv`):

```
reverse_proxy 127.0.0.1:<the service slot port> {
	header_up X-Forwarded-For {header.CF-Connecting-IP}   # only with trusted_proxy=cloudflare
	header_up X-Forwarded-Proto https
}
```

   Then `sudo -n /usr/bin/systemctl reload caddy`. If the reload failed, the files are restored from the copies, the reload is run once more, exit with code 1 (`caddy_reload_failed`).
9. `active` = `idle`; a record in `history.log`: `<time> deploy <sha> slot=<idle> previous=<previous>`. The previous slot stays running for a rollback.

### 5.3. `rollback`

A refusal if nothing has been deployed yet (`nothing_deployed`) or the previous slot does not answer (`rollback_unavailable`, the hint: deploy the version you need again). Otherwise a switch to the previous slot (step 8 above), `active` changes, a `rollback` record. Images and database data are not rolled back: migrations must be backwards compatible (`DEPLOY-PATTERN.md`).

### 5.4. `status`

The active slot, for every service and slot: the port and the version from `health`, or "not responding"; the last 5 lines of `history.log`. The JSON form is in section 6.

### 5.5. The gatekeeper: `lite-deploy gate --project <P>` (D-39)

The forced command in `authorized_keys` (4.3, `install_deploy_key`). It reads `SSH_ORIGINAL_COMMAND` and allows exactly:
- `deploy <40 hex>`;
- `rollback`;
- `status` and `status --json`.

Everything else: code 2, the message "the command is not allowed". The gatekeeper calls the `internal/deploy` functions directly in the same process, without a shell and without running scripts. Earlier the gatekeeper and `deploy-script` lay in the home directory of the project user (700, owner `U`), so a hacked project could overwrite them; the binary in `/usr/local/bin` belongs to root and a project cannot change it. The gatekeeper quotas: section 8.

## 6. The CLI and the response format

### 6.1. The v0 commands

| Command | Who | Class | What it does |
|---|---|---|---|
| `lite-deploy schema <project\|plan\|result\|error>` | anyone | safe | prints a JSON Schema |
| `lite-deploy project validate <file>` | anyone | safe | validates a description, reads nothing on the server |
| `lite-deploy project plan <file> [--out plan.json] [--ciphertext name=file]... [--rotate-password]` | the owner | safe | builds a plan, shows it readably (D-50) |
| `lite-deploy apply <plan.json or -> [--dry-run] [--yes]` | root | normal | applies a plan |
| `lite-deploy project create <file> [--yes]` | root | normal | `plan` + dry-run + `apply` in one command (the same as the three commands above) |
| `lite-deploy project list` | the owner | safe | projects from `state/projects/` |
| `lite-deploy init` | root | normal | creates the executor key and directories; an existing key is not touched (D-52) |
| `lite-deploy secret pubkey` | anyone | safe | the executor public key (`age1…`) |
| `lite-deploy secret encrypt <project> <name>` | anyone | safe | reads the value from standard input or hidden from the terminal, prints the ciphertext (it can be done on the owner's machine, without access to the server, with `--pubkey`) |
| `lite-deploy secret set <project> <name>` | root | normal | the same as encrypt + writing in place (for `_db_password` and one-off cases) |
| `lite-deploy secret list <project>` | the owner | safe | names and write times; no values |
| `lite-deploy deploy <project> <sha>` | the project user, the gatekeeper, the owner | normal | section 5.2 |
| `lite-deploy rollback <project>` | the same | normal | section 5.3 |
| `lite-deploy status [project]` | the same | safe | section 5.4 |
| `lite-deploy gate --project <P>` | SSH (the CI key) | — | section 5.5 |
| `lite init [--user <name>[,<name>…]] [--all-users] [--ufw off\|http\|max] [--plugins caddy,podman,postgres] [--yes]` | root (the only command allowed when logged in as root) | normal | the executor key and directories; administrator users (creation, the password through the system `passwd`, the group `sudo` or `wheel`, a mandatory public SSH key); the firewall mode (`max` by default: SSH only); plugins (`caddy`: the repository, the main Caddyfile, the service; `podman`, `postgres`: packages); a ban on SSH password login for these users in `/etc/ssh/sshd_config.d/50-lite-deploy.conf` with an `sshd -t` check. Before executing it shows the plan and asks for confirmation |
| `lite-deploy agent install` | root (internal: called by the installer) | normal | creates the service user `lite-deploy` (without rights and a shell), writes the unit files (`lite-deploy-agent.socket`, `lite-deploy-agent.service`, `lite-deploy-exec.socket`, `lite-deploy-exec@.service`), enables the sockets; the system administrators group (`sudo` or `wheel`) can use the agent |
| `lite-deploy agent serve [--socket <path>]` | systemd | — | the unprivileged agent: HTTP/JSON over the unix socket `/run/lite-deploy/api.sock`, `/v1/status`, `/v1/deploy`, `/v1/rollback`, `/v1/firewall[/allow\|/remove\|/enable]`, `/v1/info`; access by the system administrators group (`sudo` or `wheel`) and `SO_PEERCRED` |
| `lite-deploy exec` | systemd (root) | — | the executor: one connection on the socket `/run/lite-deploy/exec.sock` (available only to the agent user), one request `{"cmd","args","caller"}` from a fixed list, the answer in the `--json` format |
| `lite-deploy call GET\|POST /v1/<path>` | a system administrator (group `sudo` or `wheel`) | — | the agent client: the POST body from standard input, the agent answer is printed as is |
| `lite-deploy firewall status` | root on the server | safe | the `ufw` state: whether it is on, the SSH port, the rules (port, protocol, action, source, the protected flag) |
| `lite-deploy firewall allow <port>/<tcp\|udp> [--from <address or network>] --yes` | root | normal | open a port; without `--yes` the error `confirmation_required` |
| `lite-deploy firewall remove <port>/<tcp\|udp> [--from <address or network>] [--action allow\|limit\|deny] --yes` | root | normal | close a port; the SSH, 80 and 443 ports are protected (`protected_rule`), no such rule: `rule_not_found` |
| `lite-deploy firewall enable --yes` | root | normal | first `default deny incoming`, `default allow outgoing`, the rules for SSH (`limit`), 80/tcp, 443/tcp, 443/udp, then turning on |
| `lite-deploy ui [--host <user@server>] [--listen 127.0.0.1:8787]` | the owner | normal | the local web interface: state, deploy, rollback, the firewall (on top of the CLI, listens on loopback only, a token in the address) |
| `lite-deploy version` | anyone | safe | the version, the schema versions |

Common flags: `--json` (machine output), `--host <user@address>` (the client mode: the command runs on the server through `ssh <host> lite-deploy <arguments> --json`, input and output are passed through; this is how the UI and MCP work, D-28), `--quiet`. The rights come from the OS user: for `apply` with `--host` `ssh -t <host> sudo lite-deploy apply …` is used.

The direct root ban: when logged in as root (`/proc/self/loginuid` equals 0) all commands except `init`, `version`, `schema` and the internal `agent`, `exec` answer `permission_denied` (and, since v0.6, `help`). Through `sudo` from a regular user and from systemd services there is no ban.

The `secret set` command and secret values: never accepted in arguments and never read from environment variables; only standard input or hidden input from the terminal.

### 6.2. The response envelope (`--json`)

Every answer: one JSON object on one line (or several with `--stream`, not in v0).

Success:
```json
{ "ok": true, "api_version": "v1", "command": "project.plan", "data": { }, "warnings": [ { "code": "ufw_inactive", "message": "ufw is off" } ] }
```

An error:
```json
{ "ok": false, "api_version": "v1", "command": "project.plan",
  "error": { "code": "invalid_project", "message": "the project description did not pass validation",
             "details": [ { "path": "/services/api/image", "code": "pattern", "message": "…" } ],
             "hint": "fix the fields listed in details" } }
```

`data` per command:
- `project.validate`: `{ "valid": true }`.
- `project.plan` and `apply --dry-run`: `{ "plan": { … }, "steps": [ { "id": "s03", "state": "would_change", "detail": "…" } ] }` (with `plan` without `state`).
- `apply`: `{ "plan_id": "…", "plan_hash": "sha256:…", "steps": [ { "id": "s03", "state": "changed|unchanged|skipped|failed", "detail": "…", "error": null } ] }`.
- `deploy`: `{ "project": "…", "version": "<sha>", "slot": "green", "previous_slot": "blue", "switched": true }`.
- `rollback`: `{ "project": "…", "version": "<sha>", "slot": "blue", "previous_slot": "green" }`.
- `status`: `{ "project": "…", "active": "blue", "slots": { "blue": { "api": { "port": 18081, "version": "…" | null } } }, "history": [ { "time": "…", "event": "deploy", "version": "…", "slot": "…" } ] }`.
- `secret list`: `{ "secrets": [ { "name": "auth_session_secret", "updated_at": "…" } ] }`.

In the text mode (without `--json`) the same result is printed for people in English, with indentation, without color when the output is not a terminal.

### 6.3. Exit codes and error codes

| Exit code | Meaning | Error codes (`error.code`) |
|---|---|---|
| 0 | success | |
| 1 | a general error | `internal`, `caddy_reload_failed`, `io_error` |
| 2 | invalid input | `invalid_project`, `invalid_plan`, `unknown_action`, `usage`, `command_not_allowed`, `invalid_sha`, `invalid_secret`, `unused_secret` |
| 3 | a precondition is not met | `precondition_failed`, `secret_missing`, `project_exists_unmanaged`, `adopt_password_unknown` |
| 4 | no permission | `permission_denied`, `confirmation_required` (mode B and the `firewall` commands without `--yes`), `protected_rule` |
| 5 | a plan step ended with an error (applied partly) | `step_failed` |
| 6 | a conflict | `plan_hash_mismatch`, `plan_expired`, `plan_wrong_server`, `deploy_in_progress`, `quota_exceeded` |
| 7 | not found | `project_not_found`, `secret_not_found`, `rule_not_found` |
| 8 | the server check found errors | `verify_failed` |
| 10 | the deploy did not pass, traffic was not changed | `deploy_unhealthy`, `deploy_failed` (the image was not downloaded or the unit did not start), `rollback_unavailable`, `nothing_deployed` |

## 7. Secrets (D-10, D-38)

The principle: a secret value in clear text exists only with the owner at the moment of input and with the executor at the moment of decryption. The agent, the CLI on the intermediate side, the UI, MCP, the plan and the journal see only ciphertext.

- **The executor key.** An X25519 pair, the `age` format (the library `filippo.io/age`: X25519, ChaCha20-Poly1305). It is created at installation (S11; in v0 by the command `sudo lite-deploy init`, once): the private key `/etc/lite-deploy/exec.age` (root, 600), the public key `/etc/lite-deploy/exec.pub` (644, readable by anyone). We do not invent our own encryption format.
- **Encrypting on the client.** `lite-deploy secret encrypt <project> <name>` wraps the value in JSON `{ "v": 1, "project": "…", "name": "…", "value": "…", "created_at": "…" }`, encrypts it with the executor public key and prints an armor string. The binding to the project and the name is needed so that a ciphertext cannot be slipped in under another name (age has no associated data).
- **Passing.** The ciphertext goes into `params.ciphertext` of the `set_secret` step inside the plan (up to 16 KiB; the value up to 8 KiB, without `\0`, `\n`, `\r` for values that go into env). The plan hash includes the ciphertext.
- **Storage.** `/var/lib/lite-deploy/secrets/<project>/<name>`: root, 600, directories 700. Project users read only the ready env files of their services (`~/.config/lite-deploy/env/<service>.env`, 600), not the store.
- **Write without read.** There is no "show the value" command in the API and the CLI (D-10). `secret list` shows the name and the time. The value can be read only on the server as root straight from the file; the action counts as `dangerous` (D-29), and it does not get into the journal while there is no daemon, so in v0 it is just root access.
- **The database connection string** is built by the executor from the internal secret `_db_password` and is not printed.
- **The registry token** (`registry.token_secret`) is stored the same way and gets only into the `auth.json` of the project user and onto the standard input of `podman login`.
- **References in the description:** `secret://name`. Values in `project.json` are forbidden: the schema accepts only a string of this form in the secret fields.

## 8. Default quotas (D-38)

The file `/etc/lite-deploy/config.json`; the values below apply if a field is not set.

| Parameter | Default | Where it applies |
|---|---|---|
| `limits.plan_bytes` | 262144 | `apply` |
| `limits.plan_steps` | 200 | `apply` |
| `limits.project_bytes` | 65536 | `validate`, `plan` |
| `limits.secret_bytes` | 8192 | `set_secret` |
| `limits.projects` | 50 | `create_project_user` |
| `limits.services_per_project` | 8 | the schema |
| `limits.port_range` | `[18000, 18999]` | port selection |
| `limits.plan_ttl_hours` | 24 (at most 168) | `plan`, `apply` |
| `quotas.deploy_per_10min_per_project` | 10 | `deploy`, `rollback` (the gatekeeper and the CLI; counted in `~/.local/state/lite-deploy/quota.log`) |
| `quotas.apply_per_hour` | 60 | `apply` (counted in the executor journal) |
| `quotas.secret_sets_per_hour` | 120 | `set_secret` |

Exceeding: exit code 6, `error.code` = `quota_exceeded`, with the limit and the time to retry in `details`. In v0 without a daemon the quotas protect against mistakes and a looping CI; they get their real meaning in mode B (S6), where the count is kept by the executor from its journal, which the agent cannot change.

## 9. State and the journal on the server

```
/usr/local/bin/lite-deploy                        the binary (root, 755)
/etc/lite-deploy/config.json                      settings and quotas (root, 644)
/etc/lite-deploy/exec.age, exec.pub               the executor key (600 / 644)
/etc/lite-deploy/projects/<project>.json          the applied description (644)
/var/lib/lite-deploy/state/projects/<project>.json   the management mark, ports, hashes (644)
/var/lib/lite-deploy/secrets/<project>/<name>     secret values (root, 600, directories 700)
/var/lib/lite-deploy/journal.jsonl                the executor journal (root, 600; the chattr +a attribute)
/etc/caddy/conf.d/<project>-<site>.caddy          the site (root, 644)
/etc/caddy/upstreams/<project>-<site>.caddy       where traffic goes (owned by the project user)
/home/<U>/.config/containers/systemd/*.container  units (600)
/home/<U>/.config/lite-deploy/env/<service>.env   the service settings (600)
/home/<U>/.config/lite-deploy/auth.json           the registry login (600)
/home/<U>/.local/state/lite-deploy/               active, history.log, lock, quota.log
```

A journal record (one JSON line): `{ "ts", "run_id", "plan_id", "plan_hash", "step_id", "action", "project", "state", "error", "uid" }`. There are no secret values and ciphertexts in the journal.

The directory `/var/lib/lite-deploy/state` is readable by everyone: it has no secrets, and `plan` uses it to find out what already exists and to keep ports from overlapping.

## 10. The agent API `/v1` (the shape for S6, only types in v0)

Mode B appears in S6, but the request shapes are fixed now so that the CLI with `--json` and the API give the same data (D-32, D-37). HTTP with JSON over `/run/lite-deploy/api.sock`, the version in the path, the response body is `data` or `error` from the envelope above (without the `command` wrapper).

| Method and path | The CLI equivalent | Class |
|---|---|---|
| `GET /v1/status` | `status` for all projects | safe |
| `GET /v1/projects`, `GET /v1/projects/{name}` | `project list`, the project description | safe |
| `POST /v1/projects/{name}/plans` (body: a project) | `project plan` | safe |
| `POST /v1/plans/{id}/apply` | `apply` | normal; dangerous: a `confirmation_id` is needed |
| `POST /v1/projects/{name}/deployments` (body: `{ "sha": "…" }`) | `deploy` | normal |
| `POST /v1/projects/{name}/rollback` | `rollback` | normal |
| `GET /v1/projects/{name}/status` | `status <project>` | safe |
| `GET /v1/projects/{name}/secrets` | `secret list` | safe |
| `PUT /v1/projects/{name}/secrets/{secret}` (body: `{ "ciphertext": "…" }`) | `secret set` | normal |
| `GET /v1/secrets/pubkey` | `secret pubkey` | safe |
| `GET /v1/confirmations/{id}`, `POST /v1/confirmations/{id}/signature` | none in v0 | — |
| `GET /v1/schemas/{name}` | `schema` | safe |

The caller is identified by `SO_PEERCRED` (the uid) and matched with a role (the owner, the project user, an AI client) in `config.json`. A plan is always first `plans` (dry-run), then `apply` (D-37). The OpenAPI description is generated from the same Go types as the JSON Schema. (The API that is implemented now is the one in `internal/agent`: `/v1/status`, `/v1/deploy`, `/v1/rollback`, `/v1/plugins`, `/v1/verify`, `/v1/fix`, `/v1/plugin`, `/v1/firewall*`, `/v1/project/{key,preview,create}`.)

## 11. The tests S3 must give

- Description validation: examples of valid and invalid `project.json`, the boundary values of patterns, unknown fields, forbidden names.
- Canonical JSON and the plan hash: stability when fields are reordered, a changed hash on any edit.
- Plan building from `testdata/shop/project.json`: the order of steps, the parameters, no secret values.
- Golden files: the generated Quadlet units and Caddy files match in meaning `projects/shop/quadlet/*` and `projects/shop/caddy/*` (comments and file names may differ, the settings lines match).
- The gatekeeper: all allowed commands, wrong hashes, foreign commands, an empty command, commands with `;`, `$()`, spaces and line breaks (the analog of the earlier 14 checks).
- Encryption: the round trip "encrypt, decrypt", a refusal with another `project` or `name`, a refusal on a corrupt ciphertext.
- Schemas: the committed `schemas/*.json` equal the generated ones.
- The envelope and exit codes for every error of section 6.3.

S4 adds checks on a test server: `apply` creates a project from scratch, a repeated `apply` gives `unchanged` everywhere, `adopt` on a copy of the live the example project setup breaks nothing.

## 12. Correspondence to the manual the example project steps (the v0 criterion)

| Manual step | Where in v0 |
|---|---|
| 11. A user, linger, subuid | `create_project_user` |
| 12. A role, a database, `pg_hba.conf`, `app.env` with the connection string | `provision_postgres`, `write_service_env` |
| 13. The deploy key and the gatekeeper | `install_deploy_key`, `lite-deploy gate` |
| 14. The gatekeeper, `deploy-script`, the Quadlet units, `daemon-reload` | the `lite-deploy` binary, `install_quadlet`, `reload_user_units` |
| 15. The sudoers rule | `install_reload_rule` |
| 16. `deploy.conf`, `admin.env`, `app.env` | the project description, `set_secret`, `write_service_env` |
| 17. The GHCR login | `registry_login` |
| 18. The Caddy directories and files, the initial upstreams, the check, `reload` | `install_caddy_site`, `reload_caddy`, `probe_site` |
| 19. GitHub environments, secrets and variables | outside v0 (stays manual or `gh api`) |
| 20. The `cloudflared` tunnel, routes, DNS | outside v0 (S6) |
| 21. Deploy, rollback, state | `deploy`, `rollback`, `status` |
| Rights and isolation from the `DEPLOY-PATTERN.md` checklist | the action templates (section 4.3); the checks of access to `localhost:2019`, to other databases and to `/etc/environment` stay in `verify` (S11) |

What stays outside v0 and is recorded in the project `TODO.md`: disabling password login, Cloudflare Access, the Cloudflare fallback plan, a WAF rule for the webhook, cleaning up images in GHCR, migrations by a separate role.

## 13. What is not decided and moves on

1. The way to run commands in the project user's session is chosen: `systemd-run --machine=U@.host --user` (D-53); confirmed on a container and a test WSL on 2026-11-19.
2. The format of the application template manifest: after v0.
3. Signing confirmations and the list of trusted keys (`trust_key_*`): S6.
4. Moving the Caddy switch from sudoers to the executor socket: S6.
5. Installing software on a bare server and `lite-deploy init`: S11 (in v0 `init` does only the key and the directories; since v0.6 it does the full setup).
6. The default port and the behaviour for services without HTTP (workers without a port and without health): not part of v0, the schema requires `container_port` (`kind: unit` components have `host_ports` and `PORT`).
7. The orchestrator and SaaS: after v1.

## 14. Decisions

D-39…D-46 are accepted (S2). The clarifications made while implementing S3, D-48…D-51, are proposed: the order of `_db_password` under `adopt`, the project state fields, the `--ciphertext` flag, the order of plan verification. The S3 implementation differs from the text above only where these decisions (D-48…D-52) and the note below say so.

The state of the implementation at the end of S4 (code and doubles; a real server not checked): `apply`, `apply --dry-run` and `project create` are implemented (D-53…D-56); `deploy`, `rollback`, `status`, `gate` returned `not_implemented` (S4, S5; implemented later); `init`, `secret pubkey|encrypt|set|list` and reading `config.json` are implemented; errors of parsing the document root have `path` equal to `/`.
