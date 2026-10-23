#!/usr/bin/env bash
# Установка PostgreSQL 18 на хост из официального репозитория PGDG. Сервер слушает только локально.
# Ролей и баз проекта здесь нет, их создаёт отдельный шаг. Можно запускать повторно.
# Запуск: sudo bash 03-postgres.sh
set -euo pipefail

die() { echo "ОШИБКА: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "запустите от root: sudo bash $0"
# shellcheck disable=SC1091
. /etc/os-release
[[ ${ID:-} == ubuntu ]] || die "скрипты рассчитаны на Ubuntu, найдено: ${ID:-неизвестно}"
(( ${VERSION_ID%%.*} >= 24 )) || die "нужна Ubuntu 24.04 или новее, найдена $VERSION_ID"

export DEBIAN_FRONTEND=noninteractive

echo "==> Репозиторий PGDG и PostgreSQL 18"
apt-get update
apt-get install -y --no-install-recommends postgresql-common ca-certificates
# официальный скрипт сообщества PostgreSQL: добавляет репозиторий apt.postgresql.org с подписью
/usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y
apt-get install -y --no-install-recommends postgresql-18
systemctl enable --now postgresql

echo "==> Проверка: PostgreSQL не должен слушать внешние адреса"
sleep 2
listening=$(ss -ltnH 'sport = :5432' | awk '{print $4}')
echo "${listening:-(TCP-порт не слушается)}"
if echo "$listening" | grep -vE '^(127\.0\.0\.1|\[::1\]):5432$' | grep -q .; then
  echo "ВНИМАНИЕ: PostgreSQL слушает не только localhost. Проверьте listen_addresses в postgresql.conf." >&2
  exit 1
fi

echo
echo "Готово."
pg_lsclusters
