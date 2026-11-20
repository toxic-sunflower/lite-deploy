#!/bin/sh
# The liteup bootstrap (the way sh.rustup.rs is for rustup). It installs ONLY `liteup`: it downloads it from the latest
# release into /usr/local/bin and stops. Then you use liteup itself:
#
#   curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh
#   sudo liteup install          # installs lite-deploy, minisign and the agent
#   liteup status | sudo liteup update | reinstall | uninstall [--purge|--everything]
# Options (environment variables): LITEUP_PREFIX (default /usr/local/bin), LITE_DEPLOY_BASE_URL (for testing).
set -eu

REPO="toxic-sunflower/lite-deploy"
prefix=${LITEUP_PREFIX:-/usr/local/bin}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "root is required: curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh"
case $(uname -s) in Linux) ;; *) die "the server side needs Linux" ;; esac
command -v curl >/dev/null 2>&1 || die "curl is required"

base=${LITE_DEPLOY_BASE_URL:-https://github.com/$REPO/releases/latest/download}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "[1/2] Downloading liteup"
curl -fsSL "$base/liteup" -o "$tmp/liteup" || die "could not download liteup"
curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS" || die "could not download SHA256SUMS"
want=$(awk '$2=="liteup" || $2=="*liteup" {print $1}' "$tmp/SHA256SUMS" | head -n 1)
[ -n "$want" ] || die "SHA256SUMS has no entry for liteup"
got=$(sha256sum "$tmp/liteup" | awk '{print $1}')
[ "$want" = "$got" ] || die "checksum mismatch for liteup (expected $want, got $got)"

mkdir -p "$prefix"
install -m 755 "$tmp/liteup" "$prefix/liteup"
echo "[2/2] Installed $prefix/liteup."
echo "Next: sudo liteup install"
