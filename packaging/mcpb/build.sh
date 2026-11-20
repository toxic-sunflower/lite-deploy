#!/bin/sh
# Builds the Claude Desktop extension (.mcpb): one file for every system.
#   packaging/mcpb/build.sh VERSION
# It is a zip with manifest.json and server/index.js, a launcher that runs the lite-ui installed on the workstation,
# so the extension never needs updating when lite-ui does. Output: dist/lite-deploy.mcpb.
# Install it in Claude Desktop: Settings, Extensions, Install Extension.
set -eu
version=${1:?usage: build.sh VERSION}
root=$(cd "$(dirname "$0")/../.." && pwd)
dist=$root/dist
mkdir -p "$dist"
tmp=$(mktemp -d)
mkdir -p "$tmp/server"
cp "$root/packaging/mcpb/server/index.js" "$tmp/server/index.js"
sed -e "s/@VERSION@/${version#v}/" "$root/packaging/mcpb/manifest.json.tmpl" > "$tmp/manifest.json"
out=$dist/lite-deploy.mcpb
rm -f "$out"
(cd "$tmp" && zip -q -r "$out" manifest.json server)
rm -rf "$tmp"
echo "built $out"
