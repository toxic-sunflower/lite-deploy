# Update and troubleshoot

## Update `lite`

As root (or with `sudo`):

```sh
sudo liteup status       # installed and latest versions
sudo liteup update       # move to the latest release
```

`update` downloads the release, checks its signature and checksum, replaces the binary and **restarts the agent**, so nothing keeps running the old code. Your projects, secrets and settings are not touched. Pin a version with `sudo liteup install --version v0.6.2`.

## Reinstall or uninstall

```sh
sudo liteup reinstall                 # uninstall and install the same version again
sudo liteup uninstall                 # remove lite and the agent
sudo liteup uninstall --purge --yes   # also delete settings, secrets and backups (irreversible)
```

A plain `uninstall` keeps `/etc/lite-deploy` (settings and keys), `/var/lib/lite-deploy` (state and secrets) and `/var/backups/lite-deploy`. Project users, containers and databases are never removed by `lite`.

## Reading errors

Every error has a code and a hint. With `--json` any command prints a single JSON object `{"ok": false, "error": {"code": "…", "message": "…", "hint": "…"}}`, and the exit code tells the family of the problem:

| Exit code | Meaning |
|---|---|
| 2 | bad input (a field, a hash, a command) |
| 3 | a precondition is not met (for example a missing secret, or the agent is not running) |
| 4 | no permission, or a confirmation is missing (add `--yes`) |
| 5 | a plan step failed; the steps done stay, run the same command again |
| 6 | a conflict (an old plan, another deploy in progress) |
| 7 | not found (project, secret, rule) |
| 8 | `verify` found errors |
| 10 | a deploy failed and traffic did not move |

## Common problems

**"the agent is unavailable".** The agent is not running, or you are not an administrator (group `sudo` or `wheel`). Check `systemctl status lite-deploy-agent.socket`. If you replaced the binary by hand, restart it with `sudo systemctl restart lite-deploy-agent`.

**`lite` refuses to run as root.** By design. Log in as the administrator you created with `lite init`. Only `lite init`, `lite --help` and `lite version` work as root.

**A command says it "needs confirmation".** It changes the system. Add `--yes`, or run it in a terminal and answer the question.

**`project create` stopped in the middle.** Fix the reason it printed and run the very same command again: it continues from the failed step, and the steps already done are not repeated.

**A deploy says the version did not become ready.** The application did not answer `GET /health/ready` with `200` and a `version` equal to the hash within 90 seconds (or whatever `health.timeout_seconds` says). The error shows the last log lines. Traffic stays on the old version. See [Your first project](first-project.md) for the health contract.

**A deploy from CI is refused.** The restricted key allows only `deploy <40-character hash>`, `rollback` and `status`. A shortened hash is rejected. See [Deploy from CI](ci.md).

**Locked out of SSH.** Use your provider's console, delete `/etc/ssh/sshd_config.d/01-lite-security.conf` and reload `ssh`. This is why you keep a session open and test a second login before tightening SSH.

**The site returns 502 or "not deployed".** `503 not deployed` means the project exists but has never been deployed. `502` means the application is not answering: look at `lite status` and the deploy output.

## Starting over

To remove everything `lite` installed and begin again (you stay able to log in; the user and the SSH key are kept). Run as your administrator:

```sh
sudo liteup uninstall --purge --yes     # lite, the agent, settings, secrets and backups
sudo apt purge -y caddy postgresql 'postgresql-*' redis-server fail2ban podman
sudo apt autoremove -y --purge
sudo rm -rf /var/lib/postgresql /etc/postgresql /var/lib/redis /etc/caddy /var/log/caddy /etc/apt/sources.list.d/caddy-stable.list
sudo rm -f /etc/ssh/sshd_config.d/01-lite-security.conf /etc/ssh/sshd_config.d/50-lite-deploy.conf
sudo rm -f /etc/sysctl.d/60-lite-security.conf /etc/fail2ban/jail.d/lite-deploy.local /etc/apt/apt.conf.d/52lite-deploy-auto-upgrades
sudo rm -f /etc/systemd/system/lite-pg-backup.service /etc/systemd/system/lite-pg-backup.timer
sudo systemctl daemon-reload && sudo sysctl --system >/dev/null
sudo sshd -t && sudo systemctl reload ssh
sudo ufw --force reset && sudo ufw allow OpenSSH && sudo ufw --force enable
```

This deletes databases and project data. Then install again from the [Quickstart](quickstart.md). On your computer, remove `lite-ui` (`~/.local/bin/lite-ui` or `%LOCALAPPDATA%\lite-ui`) if you want.

## Getting help

Run `lite --help` for the command list and `lite info` for every plugin and its commands. Report problems at the project repository on GitHub, with the output of the failing command (it never prints secret values).
