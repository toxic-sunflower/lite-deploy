# Releasing (for the owner)

One-time setup:

1. The signing key was created without a password. The private key: `~/.config/lite-deploy/minisign.key` (never commit it; a copy is in the repository secret `MINISIGN_KEY`). The public key: `minisign.pub` in the repository and `MINISIGN_PUBKEY` in `site/liteup`. To change the key: `minisign -G -W -p minisign.pub -s minisign.key -f`, then update the secret and the key in the script.
2. Settings → Pages → Source: GitHub Actions.
3. The repository must be public, otherwise downloading without a login does not work.

Every release:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The tag starts `.github/workflows/release.yml`: tests, builds `lite-deploy` for linux amd64 and arm64 and the separate `lite-ui` for Linux, macOS and Windows (amd64 and arm64), adds the `liteup`, `lite-ui` and `install-ui.ps1` scripts, signs `SHA256SUMS` and publishes the release. The version number is only a release label; `liteup` takes the newest release by default.

`liteup` options: `--version <tag>` (pin a version), `--prefix <dir>`, `--skip-sig` (no signature check, unsafe). `liteup` itself is part of every release, so `sudo liteup update` updates both the binary and the script.

## Before a release: a check on a clean virtual machine

Every release candidate is installed on a fresh Ubuntu 24.04 machine (real systemd, sshd and ufw; a cheap VPS or a local virtual machine, not a container) and walked through from nothing. Write down what failed.

1. `curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh`, then `sudo liteup install`. Check `lite version` and `liteup status`.
2. `sudo lite init --user NAME`. Log in with the key from a second terminal when `init` asks; it must finish by itself. `lite verify` shows no errors.
3. On your workstation: `lite-ui`, add the server. The pages Overview, Projects, Protection and Server open and show real data.
4. In the interface add podman, Caddy and PostgreSQL from Server; they appear in the menu (Ingress, Databases) only after that.
5. Create a project from the form (a container and a `unit` one), deploy a version, roll back.
6. Ingress: switch a site between “through a tunnel” and “directly”; with “directly” and the web ports open, a certificate is issued.
7. `lite cloudflare install` and `setup`; the tunnel shows as running.
8. `lite backup setup` with a test age key, `lite backup run`, restore the settings backup on a second clean machine.
9. A newer release exists: the Update button on Server updates `lite` and the agent comes back; with a deliberately broken release the previous version is restored.
10. `lite ssh-key add` with a second key, log in with it, `lite ssh-key remove` the first.
11. `sudo liteup uninstall --everything` takes the machine back to a state where `sudo liteup install` works again, and SSH login still works afterwards.
