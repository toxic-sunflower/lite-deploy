# Releasing (for the owner)

One-time setup:

1. The signing key was created without a password. The private key: `~/.config/lite-deploy/minisign.key` (never commit it; a copy is in the repository secret `MINISIGN_KEY`). The public key: `minisign.pub` in the repository and `MINISIGN_PUBKEY` in `site/liteup`. To change the key: `minisign -G -W -p minisign.pub -s minisign.key -f`, then update the secret and the key in the script.
2. Settings → Pages → Source: GitHub Actions.
3. The repository must be public, otherwise downloading without a login does not work.

Every release:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

The tag starts `.github/workflows/release.yml`: tests, builds `lite-deploy` for linux amd64 and arm64 and the separate `lite-ui` for Linux, macOS and Windows (amd64 and arm64), adds the `liteup`, `lite-ui` and `lite-ui.ps1` scripts, signs `SHA256SUMS` and publishes the release. The version number is only a release label; `liteup` takes the newest release by default.

`liteup` options: `--version <tag>` (pin a version), `--prefix <dir>`, `--skip-sig` (no signature check, unsafe). `liteup` itself is part of every release, so `sudo liteup update` updates both the binary and the script.
