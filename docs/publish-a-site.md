# Publish a site

A site reaches the internet in one of two ways. You choose per site with `publish` in the project description, and you can switch at any time with `lite publish`.

| `publish` | How traffic arrives | Firewall (`lite firewall mode`) | TLS certificate |
| --- | --- | --- | --- |
| `tunnel` (default) | Cloudflare Tunnel (`cloudflared`) to Caddy on `127.0.0.1` | `max`: only SSH is open | Cloudflare's |
| `direct` | Visitors connect to the server on ports 80 and 443 | `http`: SSH, 80 and 443 | Caddy gets and renews it itself (Let's Encrypt) |

The local port on `127.0.0.1` exists in both modes, so a tunnel can keep working while the site is `direct`.

Check or change the mode of a site:

```sh
lite publish shop main            # show the current mode
lite publish shop main direct     # Caddy serves the name on 80/443 with its own certificate
lite publish shop main tunnel     # back to 127.0.0.1 only
```

`lite publish` changes the project description kept on the server and applies it like `lite project create`, with a preview. Secrets and data are untouched.

## Direct: Caddy makes the certificate

1. Point the DNS record of the name (`A`, and `AAAA` if the server has IPv6) at the server. If the name is on Cloudflare, either turn the proxy off (grey cloud) or keep it on with SSL mode "Full (strict)".
2. Open the web ports and switch the site:

   ```sh
   lite firewall mode http
   lite publish shop main direct
   ```

3. Wait a minute and check:

   ```sh
   curl -si https://shop.example.com/
   lite verify          # warns if a direct site cannot be reached because of the firewall
   ```

Caddy asks Let's Encrypt for the certificate as soon as the name points at the server and port 80 or 443 is reachable. Before that it keeps retrying; nothing else is needed. With `"trusted_proxy": "cloudflare"` the application still sees the real visitor address: on the public port `lite` overwrites `CF-Connecting-IP` with the connection's own address, so a visitor cannot forge it.

## Tunnel: Cloudflare Tunnel

`lite` does not install or manage the tunnel. The steps below are a short recipe; the Cloudflare documentation is the authority for its side. This mode needs no open inbound port (`lite firewall mode max`).

### What you need

- A domain on Cloudflare, for example `example.com`.
- The project from [Your first project](first-project.md): a Caddy site for the name `shop.example.com` on `127.0.0.1:18080`.

### 1. Know the port

The port is the `listen_port` of the site in `shop.json` (`18080` in the examples). If you did not set one, `lite` chose a free one: see the plan step "Site main: …", or `lite status`.

### 2. Install `cloudflared` as a separate, unprivileged user

Install `cloudflared` from the Cloudflare packages, then run it as its own system user, not as root and not as a project user. Create the tunnel in the Cloudflare dashboard (Zero Trust → Networks → Tunnels), copy its token and put it into a file that only that user can read, for example:

```sh
sudo useradd --system --no-create-home --shell /usr/sbin/nologin cloudflared
sudo install -d -m 755 /etc/cloudflared
sudo sh -c 'umask 077; cat > /etc/cloudflared/token'   # paste the token, then Ctrl+D
sudo chown cloudflared: /etc/cloudflared/token
```

Do not use `cloudflared service install <token>` on a shared server: the token would end up in a service file readable by everyone and in the process list.

### 3. Route the name to the port

In the tunnel's "Public hostname" settings add:

- hostname: `shop.example.com`
- service: `http://127.0.0.1:18080`

Cloudflare creates the DNS record. The tunnel forwards the original `Host` header, which is exactly what the Caddy site expects.

### 4. Tell the site to trust Cloudflare's client address

In `shop.json` set `"trusted_proxy": "cloudflare"` on the site:

```json
{ "name": "main", "host": "shop.example.com", "service": "api", "listen_port": 18080, "trusted_proxy": "cloudflare" }
```

and run `lite project create shop.json …` again. The application then sees the real visitor address in `X-Forwarded-For`, taken from `CF-Connecting-IP`.

### Check

```sh
curl -si https://shop.example.com/
```

- You get your application's answer: done.
- `1033` from Cloudflare while the tunnel is "Healthy": the name had an old DNS record from another tunnel. Delete the old record and recreate the route.
- `502` with Cloudflare's page: the container is in a restart loop (for example because a required setting is empty). Run `lite status shop`: a slot that does not answer is the one to look at, and a failed deploy prints the last log lines of the new version.
- An empty `200` that says `Server: Caddy` but has none of your headers: Caddy did not pick up the name. Check that the site host in `shop.json` is exactly the hostname you routed.

## When Cloudflare is down: switch to direct

A tunnel is one more thing that can fail. To be able to move quickly:

1. Keep ports 80 and 443 in mind: with the tunnel you run `lite firewall mode max`, so the web ports are closed. In an outage, open them: `lite firewall mode http`.
2. Switch the site: `lite publish shop main direct`.
3. Point the DNS record of the name at the server (`A`/`AAAA` instead of the tunnel's `CNAME`). Caddy gets the certificate when the name resolves to the server, usually within a minute.

When Cloudflare is back, reverse the steps: `lite publish shop main tunnel`, restore the tunnel's DNS record, `lite firewall mode max`.

If you want the switch to be a single command, stay on `direct` all the time and use the tunnel only as an extra path: the loopback port is always there.
