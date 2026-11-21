# Testing S4 in WSL (a test server)

Goal: make sure `lite-deploy project create` creates a project from scratch, that a repeated `apply` gives `unchanged`, and find differences between the doubles in the tests and the real system. The project `demo-app` (`testdata/wsl-test/project.json`) has no registry and no CI key: it has no image, the units are only written and re-read, not started.

**A test machine only.** Never touch the production server.

Note: since v0.6 the commands below can also be run as a regular administrator without `sudo` (the CLI hands privileged commands to the agent); `lite project create FILE --ciphertext name=file` does a preview, asks for confirmation and applies in one go. The step-by-step `plan` and `apply` flow below is kept because it shows every step.

## 0. What WSL must have

| What | Check | If missing |
|---|---|---|
| systemd enabled | `cat /etc/wsl.conf` contains `[boot]` and `systemd=true`; `systemctl is-system-running` answers `running` or `degraded` | add it to `/etc/wsl.conf`, then run `wsl --shutdown` in PowerShell |
| Ubuntu 24.04+ | `lsb_release -rs` | |
| podman 4.4 or newer | `podman --version` | 4.9 from the Ubuntu 24.04 repository is fine: there will only be the warning "only tested on 5.x". If a Quadlet unit turns out to be incompatible, it shows up at the `reload_user_units` step: send the output |
| Caddy with the API on `localhost:2019` | `curl -s localhost:2019/config/ \| head -c 50` | `sudo lite init --user YOU --plugins caddy` |
| A `Caddyfile` with the import | `grep conf.d /etc/caddy/Caddyfile` shows `import /etc/caddy/conf.d/*.caddy` | the same |
| PostgreSQL | `sudo -u postgres psql -c 'select 1'` | `sudo lite init --user YOU --plugins pg` |
| ufw (optional) | `sudo ufw status` | only a warning |
| Go 1.24 | `go version` | for building |

## 1. Build and install

```bash
git fetch origin && git checkout main
go build -o lite-deploy ./cmd/lite-deploy
sudo install -m 755 lite-deploy /usr/local/bin/lite-deploy
sudo lite-deploy agent install
sudo lite init --user YOU --ufw keep
```

## 2. A secret and a plan

The secret value is read from standard input (it does not get into arguments or history):

```bash
printf 'any-test-value' | lite-deploy secret encrypt demo-app app_secret > /tmp/ct_app_secret
printf 'postgresql://demo_app:test@/demo_app?host=/var/run/postgresql' | lite-deploy secret encrypt demo-app database_url > /tmp/ct_database_url
lite-deploy project plan testdata/wsl-test/project.json --ciphertext app_secret=/tmp/ct_app_secret --ciphertext database_url=/tmp/ct_database_url --out /tmp/plan.json
```

13 steps are expected (`s01 verify_host` … `s13 record_project`). Nothing on the server changes.

## 3. Dry-run

```bash
sudo lite-deploy apply /tmp/plan.json --dry-run --project-file testdata/wsl-test/project.json
```

Expected: almost all steps `would_change`, not a single `would_fail`. If `verify_host` says `would_fail`, the error text lists what is missing (item 0). Check that the dry-run created nothing: `id demo-app` must say "no such user".

## 4. Applying

```bash
sudo lite-deploy apply /tmp/plan.json --project-file testdata/wsl-test/project.json
```

Type `yes`. Expected: all steps `changed`. On an error `apply` stops at the first failed step (the rest are `skipped`); fix the cause and run the same command again (a plan lives for 24 hours).

Checking the result:

```bash
id demo-app && ls -la /home/demo-app/.config/lite-deploy/env /home/demo-app/.config/containers/systemd
sudo ls -la /var/lib/lite-deploy/secrets/demo-app /etc/caddy/conf.d /etc/caddy/upstreams
cat /var/lib/lite-deploy/state/projects/demo-app.json
sudo cat /etc/sudoers.d/lite-deploy-demo-app
curl -si -H 'Host: demo.example.com' http://127.0.0.1:18002/    # 503 not deployed
sudo tail -5 /var/lib/lite-deploy/journal.jsonl
sudo grep -B1 -A3 'lite-deploy:begin' /etc/postgresql/*/main/pg_hba.conf
```

Take the port numbers from the plan (`s09`); 18002 is an example.

## 5. Repeat (the main check)

```bash
sudo lite-deploy apply /tmp/plan.json --yes --project-file testdata/wsl-test/project.json
sudo lite-deploy apply /tmp/plan.json --dry-run
```

Expected: `unchanged` everywhere. Any `changed` or `would_change` is an idempotency error: send the output.

## 6. What to send

The complete output of steps 3, 4 and 5 (there are no secrets in them; values are not printed) and the result of the check commands from step 4. Of particular interest: `systemd-run --machine` (the `reload_user_units` step), `list-unit-files` for Quadlet, `runuser -u postgres psql`, `visudo -cf`.

## 7. Cleanup

```bash
sudo rm -f /etc/caddy/conf.d/demo-app-*.caddy /etc/caddy/upstreams/demo-app-*.caddy /etc/sudoers.d/lite-deploy-demo-app
sudo systemctl reload caddy
sudo -u postgres psql -c 'DROP DATABASE demo_app' -c 'DROP ROLE demo_app'
sudo sed -i '/lite-deploy:begin demo_app/,/lite-deploy:end demo_app/d' /etc/postgresql/*/main/pg_hba.conf && sudo systemctl reload postgresql
sudo loginctl disable-linger demo-app && sudo deluser --remove-home demo-app
sudo rm -rf /var/lib/lite-deploy/secrets/demo-app /var/lib/lite-deploy/state/projects/demo-app.json /etc/lite-deploy/projects/demo-app.json
```
