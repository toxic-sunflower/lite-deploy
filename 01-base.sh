#!/usr/bin/env bash
# Базовая подготовка голого сервера Ubuntu: обновления безопасности, брандмауэр, fail2ban, защита SSH.
# Можно запускать повторно. Запуск: sudo bash 01-base.sh [--harden-ssh]
#
# --harden-ssh  отключает вход по паролю и вход под root. Включайте, только когда вы уже вошли
#               обычным пользователем по ключу (см. README.md): иначе потеряете доступ к серверу.
set -euo pipefail

die() { echo "ОШИБКА: $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "запустите от root: sudo bash $0"
# shellcheck disable=SC1091
. /etc/os-release
[[ ${ID:-} == ubuntu ]] || die "скрипты рассчитаны на Ubuntu, найдено: ${ID:-неизвестно}"
(( ${VERSION_ID%%.*} >= 24 )) || die "нужна Ubuntu 24.04 или новее, найдена $VERSION_ID"

HARDEN_SSH=0
for arg in "$@"; do
  case $arg in
    --harden-ssh) HARDEN_SSH=1 ;;
    *) die "неизвестный аргумент: $arg" ;;
  esac
done

export DEBIAN_FRONTEND=noninteractive

echo "==> Обновление системы и установка пакетов"
apt-get update
apt-get -y upgrade
apt-get install -y --no-install-recommends \
  ufw fail2ban unattended-upgrades chrony curl ca-certificates gnupg

echo "==> Автоматические обновления безопасности (перезагрузка сама не выполняется)"
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF

# порт SSH берём из действующей конфигурации, чтобы не закрыть себе доступ
SSH_PORT=$(sshd -T 2>/dev/null | awk '/^port /{print $2; exit}')
SSH_PORT=${SSH_PORT:-22}

echo "==> Брандмауэр: снаружи только SSH ($SSH_PORT), 80 и 443"
ufw default deny incoming
ufw default allow outgoing
ufw limit "${SSH_PORT}/tcp" comment 'ssh'
ufw allow 80/tcp comment 'caddy http'
ufw allow 443/tcp comment 'caddy https'
ufw allow 443/udp comment 'caddy http3'
ufw --force enable

echo "==> fail2ban для SSH"
cat > /etc/fail2ban/jail.d/sshd.local <<EOF
[sshd]
enabled = true
port = ${SSH_PORT}
backend = systemd
maxretry = 5
findtime = 10m
bantime = 1h
EOF
systemctl enable fail2ban
systemctl restart fail2ban

echo "==> Синхронизация времени (подпись вебхуков Fanvue зависит от точного времени)"
systemctl enable --now chrony

if [[ $HARDEN_SSH -eq 1 ]]; then
  echo "==> Защита SSH"
  who=${SUDO_USER:-}
  [[ -n $who && $who != root ]] || die "--harden-ssh нужно запускать через sudo от обычного пользователя, не от root"
  home=$(getent passwd "$who" | cut -d: -f6)
  [[ -s $home/.ssh/authorized_keys ]] || die "у пользователя $who нет ключей в $home/.ssh/authorized_keys: вход по паролю отключать нельзя"

  # первое найденное значение побеждает, поэтому имя файла начинается с 00-
  cat > /etc/ssh/sshd_config.d/00-hardening.conf <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
PubkeyAuthentication yes
MaxAuthTries 3
LoginGraceTime 30
X11Forwarding no
EOF
  if ! sshd -t; then
    rm -f /etc/ssh/sshd_config.d/00-hardening.conf
    die "конфигурация SSH не прошла проверку, изменения отменены"
  fi
  systemctl reload ssh
  echo "SSH защищён. НЕ закрывайте эту сессию: откройте вторую и проверьте, что вход по ключу работает."
else
  echo "Защита SSH пропущена. Когда зайдёте обычным пользователем по ключу: sudo bash $0 --harden-ssh"
fi

echo
echo "Готово. Проверка:"
ufw status verbose | head -12
systemctl is-active fail2ban chrony
