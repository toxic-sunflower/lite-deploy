#!/bin/sh
# Builds the Claude Desktop extensions (.mcpb) from the lite-ui programs in dist/.
#   packaging/mcpb/build.sh VERSION
# For every dist/lite-ui-OS-ARCH[.exe] it makes dist/lite-deploy-OS-ARCH.mcpb: a zip with manifest.json and server/lite-ui[.exe].
# The extension of a system is installed in Claude Desktop: Settings, Extensions, Install Extension.
set -eu
version=${1:?usage: build.sh VERSION}
root=$(cd "$(dirname "$0")/../.." && pwd)
dist=$root/dist
for bin in "$dist"/lite-ui-*; do
  [ -f "$bin" ] || continue
  base=$(basename "$bin")
  case $base in *.mcpb) continue ;; esac
  name=${base%.exe}
  os=$(echo "$name" | cut -d- -f3)
  arch=$(echo "$name" | cut -d- -f4)
  case $os in windows) platform=win32; target=lite-ui.exe ;; darwin) platform=darwin; target=lite-ui ;; linux) platform=linux; target=lite-ui ;; *) continue ;; esac
  tmp=$(mktemp -d)
  mkdir -p "$tmp/server"
  cp "$bin" "$tmp/server/$target"
  chmod 755 "$tmp/server/$target"
  sed -e "s/@VERSION@/${version#v}/" -e "s/@BINARY@/$target/g" -e "s/@PLATFORM@/$platform/" "$root/packaging/mcpb/manifest.json.tmpl" > "$tmp/manifest.json"
  out=$dist/lite-deploy-$os-$arch.mcpb
  rm -f "$out"
  (cd "$tmp" && zip -q -r "$out" manifest.json server)
  rm -rf "$tmp"
  echo "built $out"
done
