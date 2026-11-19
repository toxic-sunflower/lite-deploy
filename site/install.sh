#!/bin/sh
# lite-deploy installer. One command installs everything it needs:
#   the binary from GitHub Releases (SHA256 and the minisign signature are verified) into /usr/local/bin,
#   minisign (to verify the signature) and the agent: an unprivileged service user and systemd sockets.
#   Everything else (users, firewall, Caddy, podman, PostgreSQL) is set up by `lite init` and the interface.
#
#   curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh
#   curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh -s -- --version v0.1.0
#
# Options (or environment variables):
#   --version <tag>       LITE_DEPLOY_VERSION   version (the latest by default)
#   --prefix <dir>        LITE_DEPLOY_PREFIX    where to install (/usr/local/bin by default)
#   --skip-sig            LITE_DEPLOY_SKIP_SIG=1  do not verify the signature (SHA256 only; unsafe)
#   LITE_DEPLOY_BASE_URL  where to download from (for testing the installer; GitHub Releases by default)
#
# The minisign public key the releases are signed with. Matches minisign.pub in the repository.
MINISIGN_PUBKEY="RWQXkl8IozvEWfo230NRtIvfuO4KI+SiT2s0p+Qynna/9WSATC/JDcyJ"
REPO="toxic-sunflower/lite-deploy"

set -eu

say() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

version=${LITE_DEPLOY_VERSION:-latest}
prefix=${LITE_DEPLOY_PREFIX:-/usr/local/bin}
skip_sig=${LITE_DEPLOY_SKIP_SIG:-0}

while [ $# -gt 0 ]; do
  case $1 in
    --version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift ;;
    --prefix)  [ $# -ge 2 ] || die "--prefix needs a value"; prefix=$2; shift ;;
    --skip-sig) skip_sig=1 ;;
    -h|--help) sed -n '2,14p' "$0" 2>/dev/null || true; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

[ "$(uname -s)" = Linux ] || die "only Linux is supported"
# LITE_DEPLOY_BINARY_ONLY=1: install the binary only (for testing the installer without root)
binary_only=${LITE_DEPLOY_BINARY_ONLY:-0}
if [ "$binary_only" != 1 ]; then
  [ "$(id -u)" -eq 0 ] || die "root is required: curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh"
  [ -d /run/systemd/system ] || die "systemd is required"
fi
case $(uname -m) in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "architecture $(uname -m) is not supported (amd64 and arm64 only)" ;;
esac

case $version in
  latest|v[0-9]*) ;;
  *) die "the version must look like v1.2.3 or latest" ;;
esac

if [ -n "${LITE_DEPLOY_BASE_URL:-}" ]; then
  base=$LITE_DEPLOY_BASE_URL
elif [ "$version" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$version"
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --proto '=https,file' --tlsv1.2 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "curl or wget is required"
fi
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"

as_root() {
  if [ "$(id -u)" -eq 0 ]; then "$@"
  elif command -v sudo >/dev/null 2>&1; then sudo "$@"
  else die "root or sudo is required (or install the package by hand)"; fi
}

if command -v apt-get >/dev/null 2>&1; then pm=apt
elif command -v dnf >/dev/null 2>&1; then pm=dnf
elif command -v zypper >/dev/null 2>&1; then pm=zypper
elif command -v pacman >/dev/null 2>&1; then pm=pacman
else pm=""; fi

apt_updated=0
pkg_install() {
  case $pm in
    apt)
      if [ "$apt_updated" = 0 ]; then as_root apt-get update -qq; apt_updated=1; fi
      as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" >/dev/null ;;
    dnf)    as_root dnf install -y -q "$@" ;;
    zypper) as_root zypper --non-interactive --quiet install "$@" ;;
    pacman) as_root pacman -S --noconfirm --needed "$@" ;;
    *) die "unknown package manager on this system: install ${*} by hand" ;;
  esac
}

if [ "$skip_sig" != 1 ]; then
  [ "$MINISIGN_PUBKEY" != "REPLACE_WITH_MINISIGN_PUBLIC_KEY" ] || die "the installer has no signing key (a release problem, please report it)"
  if ! command -v minisign >/dev/null 2>&1; then
    say "Installing minisign to verify the signature..."
    pkg_install minisign || die "could not install minisign (on RHEL it is in EPEL): install it by hand or use --skip-sig (SHA256 only, unsafe)"
    command -v minisign >/dev/null 2>&1 || die "minisign did not appear after installation: install it by hand or use --skip-sig (SHA256 only, unsafe)"
  fi
else
  say "WARNING: the signature is not verified, only SHA256 from the same address."
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
name="lite-deploy-linux-$arch"

say "[1/4] Downloading $name ($version)"
fetch "$base/$name" "$tmp/$name" || die "could not download $base/$name"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "could not download SHA256SUMS"

if [ "$skip_sig" != 1 ]; then
  fetch "$base/SHA256SUMS.minisig" "$tmp/SHA256SUMS.minisig" || die "could not download SHA256SUMS.minisig"
  minisign -Vm "$tmp/SHA256SUMS" -P "$MINISIGN_PUBKEY" -x "$tmp/SHA256SUMS.minisig" >/dev/null \
    || die "the SHA256SUMS signature is invalid: the files may have been tampered with, installation cancelled"
  sig_note="signature OK, "
fi

want=$(awk -v n="$name" '$2==n || $2=="*"n {print $1}' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "SHA256SUMS has no entry for $name"
got=$(sha256sum "$tmp/$name" | awk '{print $1}')
[ "$want" = "$got" ] || die "checksum mismatch (expected $want, got $got)"
say "[2/4] Verified: ${sig_note:-}SHA256 OK"

chmod 755 "$tmp/$name"
if [ -w "$prefix" ] 2>/dev/null || { [ ! -e "$prefix" ] && mkdir -p "$prefix" 2>/dev/null; }; then
  install -m 755 "$tmp/$name" "$prefix/lite-deploy"
elif command -v sudo >/dev/null 2>&1; then
  say "Write access to $prefix is needed, using sudo."
  sudo install -d -m 755 "$prefix"
  sudo install -m 755 "$tmp/$name" "$prefix/lite-deploy"
else
  die "no write access to $prefix and no sudo (run as root or pass --prefix)"
fi

# the short command name: lite
ln -sf lite-deploy "$prefix/lite" 2>/dev/null || { command -v sudo >/dev/null 2>&1 && sudo ln -sf lite-deploy "$prefix/lite"; } || true
say "[3/4] Installed $prefix/lite-deploy (short name: lite)"

[ "$binary_only" != 1 ] || exit 0

# ---- the agent ----
say "[4/4] Installing the agent (systemd)"
"$prefix/lite-deploy" agent install >/dev/null

say ""
say "Done: $("$prefix/lite-deploy" version | head -n 1)."
say "Next: sudo lite init --user YOUR_NAME   (creates an administrator with your SSH key; run lite --help for all commands)"
