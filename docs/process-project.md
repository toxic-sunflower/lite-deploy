# A project without containers

A component does not have to be a container. It can be a binary, a Node or .NET app, or even a `docker compose`-style stack run by podman. `lite` runs it as a systemd service of the project user, and deploys it with the same blue-green switch.

You build the release yourself (in CI, or on your laptop) and publish it as a **signed `tar.gz` archive**. `lite` downloads it, checks the signature and starts it. This tutorial uses the project `shop` with a component `web`.

## 1. Make a signing key

Releases are signed with [minisign](https://jedisct1.github.io/minisign/). On the machine that builds releases:

```sh
minisign -G -W -p shop.pub -s shop.key
```

- `shop.key` is the **private** key. Keep it secret, store it as a CI secret.
- `shop.pub` is the public key, a line starting with `RW…`. It goes into the project description.

## 2. Build and sign an archive

The archive contains your program and everything it needs. Name it after the commit hash:

```sh
HASH=$(git rev-parse HEAD)
tar czf app-$HASH.tar.gz -C build .
minisign -S -s shop.key -m app-$HASH.tar.gz -x app-$HASH.tar.gz.minisig
```

Upload both files to a place served over **https**, side by side:

```
https://dl.example.com/shop/app-3f2c….tar.gz
https://dl.example.com/shop/app-3f2c….tar.gz.minisig
```

The server must be able to download them (a private location can use a token, see below).

## 3. Describe the component

```json
{
  "schema": "lite-deploy/project/v2",
  "name": "shop",
  "services": {
    "web": {
      "kind": "unit",
      "exec": ["./bin/shop-server", "--quiet"],
      "host_ports": [18081, 18082],
      "data": ["uploads"],
      "release": {
        "url": "https://dl.example.com/shop/app-{sha}.tar.gz",
        "minisign_key": "RWQ…the-line-from-shop.pub…"
      },
      "health": { "path": "/health/ready" },
      "limits": { "memory": "512m", "pids": 256 }
    }
  },
  "sites": [
    { "name": "main", "host": "shop.example.com", "service": "web", "listen_port": 18080 }
  ]
}
```

What matters:

- `kind: "unit"` makes it a systemd service instead of a container.
- `exec` is the command. The program path is relative to the release directory unless it starts with `/`. The command is not run through a shell, so `$VARIABLES` and `%` in arguments are **not** expanded.
- `release.url` contains `{sha}`, replaced with the hash you deploy. It must be `https`. `minisign_key` is **mandatory**: the archive is verified against it before it is unpacked. The signature is expected at the same URL plus `.minisig`.
- `host_ports` are the ports of the two slots. **Your program must listen on the port given in the `PORT` environment variable**, on `127.0.0.1`.
- `data` lists persistent directories. They are shared by both slots and survive deploys. A process finds them in `LITE_DATA_<NAME>` (for `uploads`: `LITE_DATA_UPLOADS`).
- `health` works as for containers: `GET /health/ready` must return `200` and JSON with a `version` field equal to the commit hash.

Settings work as in [Your first project](first-project.md): `env` and `secrets` are available to `unit` components too, as environment variables of the service.

A private download location: add `"token_secret": "secret://release_token"` under `release`, and `lite` sends it as an `Authorization: Bearer` header.

## 4. Create and deploy

```sh
lite project create shop.json
lite deploy shop 3f2c0d1e9b8a7c6d5e4f3a2b1c0d9e8f7a6b5c4d
```

`deploy` downloads the archive into the free slot's directory, **verifies the signature**, unpacks it, starts the slot's unit, waits for the health check and switches Caddy. If the signature does not match, the release is rejected, the slot is not started and traffic does not move. A rollback works exactly as for containers.

## An app stack with `docker compose`

There is no separate "compose" kind. Put a `compose.yml` into the archive and run it with podman through `unit`:

```json
"exec": ["/usr/bin/podman", "compose", "up"],
"stop": ["/usr/bin/podman", "compose", "down"]
```

In `compose.yml` publish the port as `127.0.0.1:${PORT}:8080`. `stop` is run when the slot is stopped.

## If a deploy is rejected

- "the archive signature is not confirmed": the archive was signed with another key, or modified. Re-sign it with the private key that matches `minisign_key`.
- "could not download …": the server cannot fetch the URL (not https, wrong path, or it needs a token).
- The slot starts but never becomes healthy: the program is not listening on `$PORT`, or the health answer lacks the right `version`.
