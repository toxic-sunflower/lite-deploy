#!/usr/bin/env bash
# Устанавливает всё ПО на голый сервер Ubuntu одной командой: база и безопасность, podman, PostgreSQL 18, Caddy.
# Запуск: sudo bash install.sh [--harden-ssh] [--only base,podman,postgres,caddy]
#
#   --harden-ssh  дополнительно отключить вход по паролю и под root (только когда вход по ключу уже проверен)
#   --only        выполнить только перечисленные шаги (по умолчанию все по порядку)
#
# Шаги можно повторять: каждый безопасен при повторном запуске. После установки запускается verify.sh.
set -euo pipefail

die() { echo "ОШИБКА: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "запустите от root: sudo bash $0"

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
only=""
base_args=()
while (( $# )); do
  case $1 in
    --harden-ssh) base_args+=(--harden-ssh) ;;
    --only) shift; only=${1:-}; [[ -n $only ]] || die "--only требует список шагов" ;;
    *) die "неизвестный аргумент: $1" ;;
  esac
  shift
done

declare -A files=([base]=01-base.sh [podman]=02-podman.sh [postgres]=03-postgres.sh [caddy]=04-caddy.sh)
order=(base podman postgres caddy)

wanted() { [[ -z $only ]] || [[ ",$only," == *",$1,"* ]]; }

if [[ -n $only ]]; then
  IFS=',' read -ra picked <<<"$only"
  for name in "${picked[@]}"; do [[ -n ${files[$name]:-} ]] || die "неизвестный шаг: $name (есть: ${order[*]})"; done
fi

# Если установка идёт через sudo, SUDO_USER нужен шагу base для проверки ключей перед защитой SSH.
for name in "${order[@]}"; do
  wanted "$name" || continue
  echo
  echo "################ шаг: $name ################"
  if [[ $name == base ]]; then
    bash "$here/${files[$name]}" "${base_args[@]}"
  else
    bash "$here/${files[$name]}"
  fi
done

echo
echo "################ проверка ################"
bash "$here/verify.sh"
