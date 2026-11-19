#!/bin/sh
# Compatibility wrapper: the installer is now liteup. This keeps the old one-liner working:
#   curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh
# Prefer:
#   curl -fsSL https://lite-deploy.prozorov.me/liteup | sudo sh
set -eu
command -v curl >/dev/null 2>&1 || { echo "ERROR: curl is required" >&2; exit 1; }
curl -fsSL https://lite-deploy.prozorov.me/liteup | sh -s -- install "$@"
