#!/usr/bin/env bash
# Установка podman для запуска приложений без root (rootless) службами systemd.
# Можно запускать повторно. Запуск: sudo bash 02-podman.sh
set -euo pipefail

die() { echo "ОШИБКА: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "запустите от root: sudo bash $0"
# shellcheck disable=SC1091
. /etc/os-release
[[ ${ID:-} == ubuntu ]] || die "скрипты рассчитаны на Ubuntu, найдено: ${ID:-неизвестно}"
(( ${VERSION_ID%%.*} >= 24 )) || die "нужна Ubuntu 24.04 или новее, найдена $VERSION_ID"

export DEBIAN_FRONTEND=noninteractive

echo "==> Установка podman"
apt-get update
# uidmap: пространства имён пользователей для rootless; passt и slirp4netns: сеть без root;
# dbus-user-session и systemd-container: службы пользователя systemd (Quadlet) и machinectl
apt-get install -y --no-install-recommends \
  podman uidmap passt slirp4netns dbus-user-session systemd-container

echo
echo "Готово. Версии:"
podman --version
echo "Квота пространств имён пользователей (нужна >0 для rootless): $(sysctl -n user.max_user_namespaces)"
