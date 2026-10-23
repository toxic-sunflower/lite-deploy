#!/usr/bin/env bash
# Установка Caddy из официального репозитория. Сервер общий для нескольких проектов, поэтому главный
# Caddyfile только подключает файлы /etc/caddy/conf.d/*.caddy: у каждого проекта свой файл с сайтом.
# Можно запускать повторно. Запуск: sudo bash 04-caddy.sh
set -euo pipefail

die() { echo "ОШИБКА: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "запустите от root: sudo bash $0"
# shellcheck disable=SC1091
. /etc/os-release
[[ ${ID:-} == ubuntu ]] || die "скрипты рассчитаны на Ubuntu, найдено: ${ID:-неизвестно}"
(( ${VERSION_ID%%.*} >= 24 )) || die "нужна Ubuntu 24.04 или новее, найдена $VERSION_ID"

export DEBIAN_FRONTEND=noninteractive

echo "==> Репозиторий Caddy и установка (как в официальной инструкции)"
apt-get install -y --no-install-recommends debian-keyring debian-archive-keyring apt-transport-https curl gnupg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list
chmod o+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg /etc/apt/sources.list.d/caddy-stable.list
apt-get update
apt-get install -y caddy

echo "==> Главный Caddyfile: подключает сайты проектов из conf.d"
MARK='# managed-by: fanvue-games deploy/server/04-caddy.sh'
mkdir -p /etc/caddy/conf.d
if ! grep -qF "$MARK" /etc/caddy/Caddyfile 2>/dev/null; then
  [[ -f /etc/caddy/Caddyfile ]] && cp /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.bak.$(date +%Y%m%d%H%M%S)"
  cat > /etc/caddy/Caddyfile <<EOF
$MARK
# Каждый проект добавляет свой файл /etc/caddy/conf.d/<проект>.caddy. Не правьте этот файл руками.
import /etc/caddy/conf.d/*.caddy
EOF
fi
# пустое сопоставление маски даёт предупреждение, поэтому оставляем файл-заглушку
[[ -f /etc/caddy/conf.d/00-placeholder.caddy ]] || echo '# заглушка: чтобы маска import всегда находила файл' > /etc/caddy/conf.d/00-placeholder.caddy

caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
systemctl enable --now caddy
systemctl reload caddy

echo
echo "Готово."
caddy version
systemctl is-active caddy
