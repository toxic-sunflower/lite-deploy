#!/bin/sh
# install-ui.sh: installs the lite-ui program (the web interface of lite-deploy) on your own computer (Linux, WSL, macOS). No root needed.
#
#   curl -fsSL https://lite-deploy.prozorov.me/install-ui.sh | sh
#
# It downloads the small lite-ui program from the latest release into ~/.local/bin, checks its SHA256 against the
# SHA256SUMS of the same release (and the minisign signature too, if minisign is installed). Nothing is installed on
# the server. Then: lite-ui
#
# Options (environment variables): LITE_UI_VERSION (a tag like v0.7.0, the latest by default), LITE_UI_PREFIX
# (the folder, ~/.local/bin by default), LITE_UI_BASE_URL (for testing), LITE_UI_SKIP_SIG=1.
set -eu

# The minisign public key the releases are signed with. Matches minisign.pub in the repository.
MINISIGN_PUBKEY="RWQXkl8IozvEWfo230NRtIvfuO4KI+SiT2s0p+Qynna/9WSATC/JDcyJ"
REPO="toxic-sunflower/lite-deploy"

say() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

version=${LITE_UI_VERSION:-latest}
prefix=${LITE_UI_PREFIX:-$HOME/.local/bin}
case $version in latest|v[0-9]*) ;; *) die "the version must look like v1.2.3 or latest" ;; esac

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "this script is for Linux, WSL and macOS; on Windows use PowerShell: irm https://lite-deploy.prozorov.me/install-ui.ps1 | iex" ;;
esac
case $(uname -m) in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "architecture $(uname -m) is not supported (amd64 and arm64 only)" ;;
esac

if [ -n "${LITE_UI_BASE_URL:-}" ]; then base=$LITE_UI_BASE_URL
elif [ "$version" = latest ]; then base="https://github.com/$REPO/releases/latest/download"
else base="https://github.com/$REPO/releases/download/$version"; fi

name="lite-ui-$os-$arch"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() { curl -fsSL "$1" -o "$2" || die "could not download $1"; }

say "[1/3] Downloading $name ($version)"
fetch "$base/$name" "$tmp/$name"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS"

note=""
if command -v minisign >/dev/null 2>&1 && [ "${LITE_UI_SKIP_SIG:-0}" != 1 ]; then
  fetch "$base/SHA256SUMS.minisig" "$tmp/SHA256SUMS.minisig"
  minisign -Vm "$tmp/SHA256SUMS" -P "$MINISIGN_PUBKEY" -x "$tmp/SHA256SUMS.minisig" >/dev/null \
    || die "the SHA256SUMS signature is invalid: the files may have been tampered with, installation cancelled"
  note="signature OK, "
else
  say "Note: minisign is not installed, so only the SHA256 from the release is checked."
fi

want=$(awk -v n="$name" '$2==n || $2=="*"n {print $1}' "$tmp/SHA256SUMS" | head -n 1)
[ -n "$want" ] || die "SHA256SUMS has no entry for $name"
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$name" | awk '{print $1}')
else got=$(shasum -a 256 "$tmp/$name" | awk '{print $1}'); fi
[ "$want" = "$got" ] || die "checksum mismatch (expected $want, got $got)"
say "[2/3] Verified: ${note}SHA256 OK"

mkdir -p "$prefix"
install -m 755 "$tmp/$name" "$prefix/lite-ui"
say "[3/3] Installed $prefix/lite-ui"
case ":$PATH:" in *":$prefix:"*) ;; *) say "Note: $prefix is not in your PATH; run $prefix/lite-ui or add it to PATH." ;; esac
say "Done: $("$prefix/lite-ui" --version)."
say "Next: lite-ui   (it opens the interface in your browser: press "Add server" and enter YOUR_USER@YOUR_SERVER; needs a key login to the server over ssh)"
