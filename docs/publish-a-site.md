# Publish a site

`lite` keeps every site on `127.0.0.1`. That is deliberate: nothing about your application is open to the internet until you choose how to open it. This tutorial uses a **Cloudflare Tunnel**, which needs no open inbound port on the server (the firewall mode `--ufw max`, SSH only, is the right one).

`lite` does not install or manage the tunnel. The steps below are a short recipe; the Cloudflare documentation is the authority for its side.

## What you need

- A domain on Cloudflare, for example `example.com`.
- The project from [Your first project](first-project.md): a Caddy site for the name `shop.example.com` on `127.0.0.1:18080`.

## 1. Know the port

The port is the `listen_port` of the site in `shop.json` (`18080` in the examples). If you did not set one, `lite` chose a free one: see the plan step "Site main: …", or `lite status`.

## 2. Install `cloudflared` as a separate, unprivileged user

Install `cloudflared` from the Cloudflare packages, then run it as its own system user, not as root and not as a project user. Create the tunnel in the Cloudflare dashboard (Zero Trust → Networks → Tunnels), copy its token and put it into a file that only that user can read, for example:

```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin cloudflared
sudo install -d -m 755 /etc/cloudflared
sudo sh -c 'umask 077; cat > /etc/cloudflared/token'   # paste the token, then Ctrl+D
sudo chown cloudflared: /etc/cloudflared/token
```

Do not use `cloudflared service install <token>` on a shared server: the token would end up in a service file readable by everyone and in the process list.

## 3. Route the name to the port

In the tunnel's "Public hostname" settings add:

- hostname: `shop.example.com`
- service: `http://127.0.0.1:18080`

Cloudflare creates the DNS record. The tunnel forwards the original `Host` header, which is exactly what the Caddy site expects.

## 4. Tell the site to trust Cloudflare's client address

In `shop.json` set `"trusted_proxy": "cloudflare"` on the site:

```json
{ "name": "main", "host": "shop.example.com", "service": "api", "listen_port": 18080, "trusted_proxy": "cloudflare" }
```

and run `lite project create shop.json …` again. The application then sees the real visitor address in `X-Forwarded-For`, taken from `CF-Connecting-IP`.

## Check

```sh
curl -si https://shop.example.com/
```

- You get your application's answer: done.
- `1033` from Cloudflare while the tunnel is "Healthy": the name had an old DNS record from another tunnel. Delete the old record and recreate the route.
- `502` with Cloudflare's page: the container is in a restart loop (for example because a required setting is empty). Run `lite status shop`: a slot that does not answer is the one to look at, and a failed deploy prints the last log lines of the new version.
- An empty `200` that says `Server: Caddy` but has none of your headers: Caddy did not pick up the name. Check that the site host in `shop.json` is exactly the hostname you routed.

## Without a tunnel

Any reverse proxy that can talk to `http://127.0.0.1:PORT` with the right `Host` header works the same way, for example a Caddy or nginx on **another** server (the "ingress" server) that terminates TLS. `lite` itself does not terminate TLS on the public interface yet.
