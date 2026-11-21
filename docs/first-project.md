# Your first project

This tutorial builds a project called `shop` that runs one container, talks to PostgreSQL, keeps a secret and serves one site. You will create it, deploy a version, roll back and look at what happened.

Before you start, finish the [Quickstart](quickstart.md) up to "Check the server". You work as the administrator you created with `lite init`.

## What `lite` needs from your application

- **A container image** named `ghcr.io/you/shop-api`, published with a tag equal to the **full git commit hash** (40 characters). `lite` pulls `image:HASH`.
- **A health endpoint.** By default `GET /health/ready` on the container port must answer `200` and a JSON body with a `version` field equal to the commit hash that was deployed:

  ```json
  { "status": "ready", "version": "3f2c0d1e9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d" }
  ```

  This is how `lite` knows the **new** version, not the old one, is the one answering. Another path or field name can be set under `health`. If you leave `health` out of the description entirely, defaults apply. A service with no HTTP endpoint at all is not supported for containers yet.
- **Settings from the environment.** `lite` writes environment variables (plain ones, secrets and the database connection string) to a file that is passed to the container.

## 1. Write the description

`shop.json`:

```json
{
  "schema": "lite-deploy/project/v2",
  "name": "shop",
  "registry": { "host": "ghcr.io", "username": "you", "token_secret": "secret://registry_token" },
  "services": {
    "api": {
      "image": "ghcr.io/you/shop-api",
      "container_port": 8080,
      "limits": { "memory": "512m", "pids": 256 },
      "env": { "APP_ENV": "production" },
      "secrets": { "SESSION_SECRET": "secret://session_secret", "DATABASE_URL": "secret://database_url" },
      "sockets": ["/var/run/postgresql"]
    }
  },
  "sites": [
    { "name": "main", "host": "shop.example.com", "service": "api", "listen_port": 18080, "max_body": "5MB" }
  ]
}
```

What each part means:

- `name`: the project name. It is also the name of a system user `shop` that owns everything of the project, and the database role made by the `pg` plugin is `shop` too (hyphens become underscores).
- `registry`: only for private images. The token is a secret, so it is referenced as `secret://registry_token`, never written here.
- **A database is a secret.** A project does not describe a database: the connection is a secret setting of a component, for example `DATABASE_URL`. To have PostgreSQL made for you, install the `pg` plugin (Server, Can be added) and tick “Make a PostgreSQL database for this project on this server” in the form: the server makes the database and its user and puts the connection in the secret `database_url` of the project; the component shares the socket with `"sockets": ["/var/run/postgresql"]` and gets `"secrets": { "DATABASE_URL": "secret://database_url" }`. On the command line: `lite pg create-db shop shop --project shop --secret database_url` (add `--template 'Host={host};Database={db};Username={user};Password={password}'` for another notation of the address; the parts are `{host} {port} {user} {password} {db}`). For any other database (MySQL, Redis, a database on another server) install the program (Packages) and add the secret yourself.
- `services.api`: one container. It listens on `container_port` inside the container; `lite` publishes it on `127.0.0.1` only.
- `env` and `secrets`: plain variables and references to secrets. Values of secrets never appear in the description.
- `sites`: a Caddy site. Caddy listens on `127.0.0.1:18080` for the name `shop.example.com` and passes traffic to whichever version is active.

All fields are validated; unknown fields are rejected. Check the file without touching the server:

```sh
lite-deploy project validate shop.json
```

## 2. Encrypt the secrets

Secret values are read from standard input, so they never enter your shell history:

```sh
printf '%s' 'ghp_yourReadOnlyToken' | lite secret encrypt shop registry_token > ct_registry_token
printf '%s' 'a-long-random-string'  | lite secret encrypt shop session_secret > ct_session_secret
```

Each command prints an encrypted block. The files can be shown to anyone: only the server can decrypt them, and each is bound to the project and the name.

![The New project form.](img/new-project.png)

## 3. Preview and create

```sh
lite project create shop.json \
  --ciphertext registry_token=ct_registry_token \
  --ciphertext session_secret=ct_session_secret
```

`lite` prints the plan (about a dozen steps: a user, the database, the secrets, the container units, the Caddy site, a check) and asks for confirmation. Type anything but `yes` to stop. Nothing has changed yet. To apply, run it again and answer `yes` (or add `--yes`).

If a step fails, fix the cause and run **the same command** again: it continues from where it stopped and leaves everything done before alone. Running it once more after success must report every step `unchanged`.

## 4. Deploy a version

```sh
lite deploy shop 3f2c0d1e9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d
```

The first deploy goes to the `blue` slot, the second to `green`, and so on. For each deploy `lite`:

1. pulls the image for that hash;
2. starts it in the free slot, next to the running version;
3. polls the health endpoint until the answer carries the same hash (up to 90 seconds by default);
4. points Caddy to the new slot and reloads it. If this fails, the previous state is restored.

If the new version does not become healthy, traffic stays on the old one and `deploy` reports the last log lines.

## 5. Look at it

```sh
lite status shop
```

shows the active slot, each slot's port and the version it answers, and the latest events. Test the site locally with the name it serves:

```sh
curl -si -H 'Host: shop.example.com' http://127.0.0.1:18080/
```

## 6. Roll back

```sh
lite rollback shop
```

switches traffic to the previous slot, if it is still running. Code is rolled back, **data is not**: make your database migrations backwards compatible, so that two neighbouring versions work with the same database.

## 7. Change something

Edit `shop.json` and run `lite project create` again with the same `--ciphertext` flags (you can skip the secrets that already exist on the server). Only what differs is changed.

## Using the web interface instead

Everything above is also in the web interface, including a form for a new project. The interface is the small program `lite-ui` on **your workstation**: it reaches the server over SSH and is never installed on the server. Install it (Windows PowerShell: `irm https://lite-deploy.prozorov.me/install-ui.ps1 | iex`; WSL, Linux and macOS: `curl -fsSL https://lite-deploy.prozorov.me/install-ui.sh | sh`), then:

```sh
lite-ui
```

It opens in your browser at an address like `http://127.0.0.1:8787/?t=…`. At the top press "Add server" and enter `alice@YOUR_SERVER`; your servers are listed there for next time. In the "New project" section fill in the form and press "Preview plan", then "Create project". Secret values you type are encrypted in the page before they leave it.

## Next

- Expose the site: [Publish a site](publish-a-site.md).
- Deploy on every push: [Deploy from CI](ci.md).
