# Quickstart

From a bare Ubuntu server to a running site in one sitting. Every step links to a longer tutorial if you need the details. The example project is called `shop`.

You need:

- a server with Ubuntu 24.04 or newer, and SSH access to it as root (or a user with `sudo`);
- your public SSH key (`~/.ssh/id_ed25519.pub` on your computer);
- a container image of your application in a registry, tagged with the full git commit hash (for example `ghcr.io/you/shop-api:3f2c…`), **or** a built archive (see [A project without containers](process-project.md)).

## 1. Install

On the server, as root or with `sudo`. First download `liteup`, which installs only itself:

```sh
curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh
```

Then install `lite` with it:

```sh
sudo liteup install
```

This installs `lite` (the same as `lite-deploy`), `minisign` and the agent. Nothing else on the server changes. Check it:

```sh
lite version
```

## 2. Prepare the server

Still as root, once:

```sh
sudo lite init --user alice --ufw max --plugins caddy,podman,pg
```

`init` shows what it will do and asks for confirmation. It creates the user `alice` (asking for a password and for your public SSH key), gives them administrator rights, sets the firewall to "SSH only" (`--ufw max`), installs Caddy, podman and PostgreSQL, **and protects the server**: SSH by key only (root too), protection against password guessing, automatic security updates.

After the user is created, `init` asks you to log in once with your key: open a new terminal on your workstation and run `ssh alice@YOUR_SERVER`. `init` waits for that login (up to 5 minutes) and only then closes password login and finishes the protection, so you cannot lock yourself out. From now on you work as `alice`, and you do not need `sudo` for `lite` commands.

![The Overview page after the first start.](img/overview.png)

## 3. Check the server

```sh
lite verify
```

`verify` only reads. It lists what is fine, what deserves attention and what is wrong, with a suggested fix. After `init` there should be no errors. [Protect the server](protect-the-server.md) explains what was set up.

## 4. Create the project

Write a description of the project (`shop.json`):

```json
{
  "schema": "lite-deploy/project/v2",
  "name": "shop",
  "database": { "engine": "postgres", "connection_format": "url" },
  "services": {
    "api": {
      "image": "ghcr.io/you/shop-api",
      "container_port": 8080,
      "database_env": "DATABASE_URL"
    }
  },
  "sites": [
    { "name": "main", "host": "shop.example.com", "service": "api", "listen_port": 18080 }
  ]
}
```

Create it. Without `--yes`, `lite` shows a preview of every step and asks you to type `yes`:

```sh
lite project create shop.json
```

The details (secrets, a private registry, limits) are in [Your first project](first-project.md).

## 5. Deploy

```sh
lite deploy shop 3f2c0d1e9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d
lite status shop
```

`deploy` takes the full 40-character commit hash. It starts the new version next to the old one, waits until it answers its health check with the same hash, and only then switches traffic. If something is wrong, `lite rollback shop` switches back.

## 6. Make the site reachable

`lite` keeps every site on `127.0.0.1` (port `18080` above) until you publish it: either Caddy serves it directly with its own TLS certificate, or a Cloudflare Tunnel brings traffic to it. [Publish a site](publish-a-site.md) shows both and how to switch.

## What next

- Deploy automatically from CI: [Deploy from CI](ci.md).
- Set up backups before you have real data: [Backups and restore](backups.md).
- Keep `lite` current: [Update and troubleshoot](maintenance.md).
