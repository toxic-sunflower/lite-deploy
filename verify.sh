#!/usr/bin/env bash
# Проверка состояния сервера после установки. Ничего не меняет, только читает.
# Запуск: sudo bash verify.sh   (без sudo часть проверок покажет «нет прав»)
# Код выхода 1, если найдена серьёзная проблема (помечена FAIL).
set -uo pipefail

fails=0
ok()   { printf '  OK    %s\n' "$*"; }
warn() { printf '  WARN  %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }

. /etc/os-release 2>/dev/null || true
echo "Система: ${PRETTY_NAME:-неизвестно}"

echo "Брандмауэр"
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q '^Status: active'; then
  ok "ufw включён"
  ufw status 2>/dev/null | sed -n '/^To/,$p' | sed 's/^/        /'
else
  fail "ufw выключен или не установлен (или нет прав: запустите через sudo)"
fi

echo "Службы"
for unit in fail2ban chrony caddy postgresql; do
  if systemctl is-active --quiet "$unit" 2>/dev/null; then ok "$unit работает"; else warn "$unit не запущен (не установлен?)"; fi
done
if [[ -f /etc/apt/apt.conf.d/20auto-upgrades ]] && grep -q 'Unattended-Upgrade "1"' /etc/apt/apt.conf.d/20auto-upgrades; then
  ok "автоматические обновления безопасности включены"
else
  warn "автоматические обновления безопасности не настроены"
fi

echo "SSH"
if command -v sshd >/dev/null && cfg=$(sshd -T 2>/dev/null); then
  pw=$(awk '/^passwordauthentication /{print $2}' <<<"$cfg")
  root=$(awk '/^permitrootlogin /{print $2}' <<<"$cfg")
  [[ $pw == no ]] && ok "вход по паролю отключён" || warn "вход по паролю включён (sudo bash 01-base.sh --harden-ssh)"
  [[ $root == no ]] && ok "вход под root отключён" || warn "вход под root разрешён (значение: $root)"
else
  warn "не удалось прочитать конфигурацию sshd (нужен sudo)"
fi

echo "PostgreSQL"
if command -v pg_lsclusters >/dev/null; then
  pg_lsclusters | sed 's/^/        /'
  exposed=$(ss -ltnH 'sport = :5432' 2>/dev/null | awk '{print $4}' | grep -vE '^(127\.0\.0\.1|\[::1\]):5432$' || true)
  if [[ -n $exposed ]]; then fail "PostgreSQL слушает наружу: $exposed"; else ok "PostgreSQL слушает только localhost"; fi
else
  warn "PostgreSQL не установлен"
fi

echo "podman и Caddy"
command -v podman >/dev/null && ok "$(podman --version)" || warn "podman не установлен"
command -v caddy >/dev/null && ok "caddy $(caddy version | awk '{print $1}')" || warn "Caddy не установлен"

echo "Что слушает снаружи (адреса, кроме localhost)"
ss -ltnH 2>/dev/null | awk '{print $4}' | grep -vE '^(127\.|\[::1\]|::1)' | sort -u | sed 's/^/        /'
echo "        (ожидаемо: SSH, 80, 443; всё остальное проверьте)"

echo
if (( fails > 0 )); then echo "Итог: найдено серьёзных проблем: $fails"; exit 1; fi
echo "Итог: серьёзных проблем нет"
