#!/bin/sh
# Takes the pictures of the interface for the documentation: site/img/NAME-light.png and NAME-dark.png.
# It builds lite-ui, starts it with a stand-in for ssh that answers with sample data (fake-ssh), and photographs every page with
# a headless Edge or Chrome. Set EDGE to the browser if it is not found. Run it from the repository root.
set -eu
root=$(cd "$(dirname "$0")/../.." && pwd)
tmp=$(mktemp -d)
EDGE=${EDGE:-"/mnt/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"}
mkdir -p "$tmp/bin"
cp "$root/site/screenshots/fake-ssh" "$tmp/bin/ssh"
(cd "$root" && go build -o "$tmp/lite-ui" ./cmd/lite-ui)
port=8789
(cd "$tmp" && PATH="$tmp/bin:$PATH" XDG_CONFIG_HOME="$tmp/cfg" ./lite-ui --host alice@203.0.113.7 --no-open --listen "127.0.0.1:$port" >"$tmp/out.txt" 2>&1 &)
sleep 1
base=$(sed -n 3p "$tmp/out.txt" | tr -d ' ')
topath() { if command -v wslpath >/dev/null 2>&1 && case $EDGE in /mnt/*) true ;; *) false ;; esac; then wslpath -w "$1"; else echo "$1"; fi; }
# NAME:ADDRESS:WIDTHxHEIGHT
for spec in overview:overview:1240x860 projects:projects:1240x980 new-project:projects/new:1240x1000 ingress:ingress:1240x860 databases:databases:1240x800 backups:backups:1240x1180 protection:security:1240x1100 server:server:1240x900; do
  name=${spec%%:*}; rest=${spec#*:}; addr=${rest%%:*}; size=${rest#*:}
  for theme in light dark; do
    out="$root/site/img/$name-$theme.png"
    "$EDGE" --headless=new --disable-gpu --hide-scrollbars --window-size="${size%x*},${size#*x}" --virtual-time-budget=9000 \
      --screenshot="$(topath "$out")" "$base&theme=$theme#$addr" >/dev/null 2>&1
    echo "$out"
  done
done
pid=$(ss -ltnp 2>/dev/null | grep ":$port" | sed 's/.*pid=\([0-9]*\).*/\1/')
[ -z "$pid" ] || kill "$pid"
rm -rf "$tmp"
