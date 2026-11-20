# Protect the server

`lite init` already brings a fresh server to a sensible secure state (SSH by key only, protection against password guessing, automatic security updates, protective kernel parameters), unless you passed `--no-security`. This page shows how to check it, and how to change or repeat any part of it. It all lives in the `security` and `firewall` plugins, and in `lite verify`.

## The check

```sh
lite verify
```

It never changes anything. Every line is `ok`, `warning` or `ERROR`, and a problem comes with the command that fixes it. The total at the end tells you how many of each. The exit code is `8` when there are errors, so it works in scripts too.

## Let `lite` fix the safe things

```sh
lite fix --dry-run      # what would be done
lite fix                # do it
```

`fix` runs only a short list of safe, repeatable fixes: SSH hardening, automatic updates, kernel parameters, fail2ban, backup timers and installing the plugins your projects need. Everything else (the firewall, users, keys, package changes, anything that needs a value from you) is listed under "Manual" and you do it yourself.

## What the `security` plugin does

If you skipped it in `init`, or want to repeat it, turn it all on with one command (SSH goes last, so a mistake cannot lock you out early):

```sh
lite security enable --yes
lite security status
```

`enable` sets SSH to key-only login for everyone (root only by key; `lite security enable no-root --yes` forbids root entirely), turns on protection against password guessing, automatic security updates, protective kernel parameters, and the closing of dead SSH sessions (a connection that stops answering is dropped after about three minutes, so it does not hang on the server). It refuses to close password login unless an administrator with a key will remain, it checks the SSH configuration before applying it and puts the old one back on an error.

A few related commands:

- `lite security ssh --yes` (or `lite security ssh no-root --yes`): change how SSH login works.
- `lite security allow-users alice bob --yes`: only these users may use SSH at all (project users that CI logs in as are added automatically; `any` removes the limit). Be sure every administrator is on the list.
- `lite security banned` lists addresses blocked after repeated failed logins, `lite security unban ADDRESS --yes` lifts a block.
- `lite security logins`: recent logins and failed attempts.

**Before** you close password login on a server that was not set up by `lite init`: open a second terminal and prove that your key login works.

## The firewall

```sh
lite firewall status
lite firewall mode max --yes      # only SSH is open
lite firewall mode http --yes     # SSH, 80 and 443
lite firewall allow 8443/tcp --from 10.0.0.0/24 --yes
lite firewall remove 8443/tcp --from 10.0.0.0/24 --yes
```

The SSH, 80 and 443 ports cannot be closed through `lite`, and every change asks for confirmation. A site behind a tunnel needs no inbound web ports at all, so `max` is the right mode; a site published `direct` needs `http`.

`lite verify` also reports ports that listen on all interfaces and are not blocked by the firewall.

## Docker

`lite` refuses to install Docker packages, and `verify` warns if Docker is installed. Access to the Docker daemon is the same as root on the server, which breaks the isolation between projects. Use podman (this is what `lite` installs).

## Check regularly

Put `lite verify` into a habit, or a timer. After every `lite` update run it once.
