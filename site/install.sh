#!/bin/sh
# Установщик lite-deploy.
#
# Без параметров (ваш компьютер): скачивает бинарник из GitHub Releases, проверяет SHA256 и подпись minisign,
# кладёт в /usr/local/bin. Если нет minisign, ставит его через менеджер пакетов.
#
# С --server (сервер, нужен root): то же, и дополнительно ставит Caddy (ingress) с главным Caddyfile,
# создаёт пользователя службы агента и включает агента и исполнителя (systemd).
#
#   curl -fsSL https://lite-deploy.prozorov.me/install.sh | sh
#   curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh -s -- --server
#   curl -fsSL https://lite-deploy.prozorov.me/install.sh | sh -s -- --version v0.1.0
#
# Параметры (или переменные окружения):
#   --server              установка на сервер (Caddy и агент), от root
#   --version <тег>       LITE_DEPLOY_VERSION   версия (по умолчанию последняя)
#   --prefix <каталог>    LITE_DEPLOY_PREFIX    куда ставить (по умолчанию /usr/local/bin)
#   --skip-sig            LITE_DEPLOY_SKIP_SIG=1  не проверять подпись (только SHA256; небезопасно)
#   LITE_DEPLOY_BASE_URL  откуда качать (для проверки установщика; по умолчанию GitHub Releases)
#
# Открытый ключ minisign, которым подписаны релизы. Совпадает с minisign.pub в репозитории.
MINISIGN_PUBKEY="RWQXkl8IozvEWfo230NRtIvfuO4KI+SiT2s0p+Qynna/9WSATC/JDcyJ"
REPO="toxic-sunflower/lite-deploy"

set -eu

say() { printf '%s\n' "$*"; }
die() { printf 'ОШИБКА: %s\n' "$*" >&2; exit 1; }

version=${LITE_DEPLOY_VERSION:-latest}
prefix=${LITE_DEPLOY_PREFIX:-/usr/local/bin}
skip_sig=${LITE_DEPLOY_SKIP_SIG:-0}
server=0

while [ $# -gt 0 ]; do
  case $1 in
    --version) [ $# -ge 2 ] || die "--version требует значение"; version=$2; shift ;;
    --prefix)  [ $# -ge 2 ] || die "--prefix требует значение"; prefix=$2; shift ;;
    --skip-sig) skip_sig=1 ;;
    --server)  server=1 ;;
    -h|--help) sed -n '2,20p' "$0" 2>/dev/null || true; exit 0 ;;
    *) die "неизвестный параметр: $1" ;;
  esac
  shift
done

[ "$(uname -s)" = Linux ] || die "поддерживается только Linux"
if [ "$server" = 1 ]; then
  [ "$(id -u)" -eq 0 ] || die "для установки сервера нужен root: curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh -s -- --server"
  [ -d /run/systemd/system ] || die "на сервере нужен systemd"
fi
case $(uname -m) in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "архитектура $(uname -m) не поддерживается (есть amd64 и arm64)" ;;
esac

case $version in
  latest|v[0-9]*) ;;
  *) die "версия должна быть вида v1.2.3 или latest" ;;
esac

if [ -n "${LITE_DEPLOY_BASE_URL:-}" ]; then
  base=$LITE_DEPLOY_BASE_URL
elif [ "$version" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$version"
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --proto '=https,file' --tlsv1.2 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "нужен curl или wget"
fi
command -v sha256sum >/dev/null 2>&1 || die "нужен sha256sum"

as_root() {
  if [ "$(id -u)" -eq 0 ]; then "$@"
  elif command -v sudo >/dev/null 2>&1; then sudo "$@"
  else die "нужен root или sudo (или поставьте пакет вручную)"; fi
}

if command -v apt-get >/dev/null 2>&1; then pm=apt
elif command -v dnf >/dev/null 2>&1; then pm=dnf
elif command -v zypper >/dev/null 2>&1; then pm=zypper
elif command -v pacman >/dev/null 2>&1; then pm=pacman
else pm=""; fi

apt_updated=0
pkg_install() {
  case $pm in
    apt)
      if [ "$apt_updated" = 0 ]; then as_root apt-get update -qq; apt_updated=1; fi
      as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" >/dev/null ;;
    dnf)    as_root dnf install -y -q "$@" ;;
    zypper) as_root zypper --non-interactive --quiet install "$@" ;;
    pacman) as_root pacman -S --noconfirm --needed "$@" ;;
    *) die "не знаю менеджер пакетов этой системы: поставьте ${*} вручную" ;;
  esac
}

if [ "$skip_sig" != 1 ]; then
  [ "$MINISIGN_PUBKEY" != "REPLACE_WITH_MINISIGN_PUBLIC_KEY" ] || die "в установщик не вписан ключ подписи (проблема релиза, сообщите автору)"
  if ! command -v minisign >/dev/null 2>&1; then
    say "Ставлю minisign для проверки подписи..."
    pkg_install minisign || die "не удалось поставить minisign (на RHEL он в EPEL): поставьте вручную или используйте --skip-sig (только SHA256, небезопасно)"
    command -v minisign >/dev/null 2>&1 || die "minisign не появился после установки: поставьте вручную или используйте --skip-sig (только SHA256, небезопасно)"
  fi
else
  say "ВНИМАНИЕ: подпись не проверяется, только SHA256 с того же адреса."
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
name="lite-deploy-linux-$arch"

say "Скачиваю $name ($version)..."
fetch "$base/$name" "$tmp/$name" || die "не удалось скачать $base/$name"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "не удалось скачать SHA256SUMS"

if [ "$skip_sig" != 1 ]; then
  fetch "$base/SHA256SUMS.minisig" "$tmp/SHA256SUMS.minisig" || die "не удалось скачать SHA256SUMS.minisig"
  minisign -Vm "$tmp/SHA256SUMS" -P "$MINISIGN_PUBKEY" -x "$tmp/SHA256SUMS.minisig" >/dev/null \
    || die "подпись SHA256SUMS неверна: файлы могли подменить, установка отменена"
  say "Подпись верна."
fi

want=$(awk -v n="$name" '$2==n || $2=="*"n {print $1}' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "в SHA256SUMS нет записи для $name"
got=$(sha256sum "$tmp/$name" | awk '{print $1}')
[ "$want" = "$got" ] || die "контрольная сумма не совпала (ждали $want, получили $got)"
say "SHA256 совпала."

chmod 755 "$tmp/$name"
if [ -w "$prefix" ] 2>/dev/null || { [ ! -e "$prefix" ] && mkdir -p "$prefix" 2>/dev/null; }; then
  install -m 755 "$tmp/$name" "$prefix/lite-deploy"
elif command -v sudo >/dev/null 2>&1; then
  say "Нужны права для записи в $prefix, использую sudo."
  sudo install -d -m 755 "$prefix"
  sudo install -m 755 "$tmp/$name" "$prefix/lite-deploy"
else
  die "нет прав на запись в $prefix и нет sudo (запустите от root или задайте --prefix)"
fi

say "Установлено: $prefix/lite-deploy"
"$prefix/lite-deploy" version || true

[ "$server" = 1 ] || exit 0

# ---- сервер: Caddy, агент ----
install_caddy() {
  case $pm in
    apt)
      pkg_install debian-keyring debian-archive-keyring apt-transport-https curl gnupg
      fetch 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' "$tmp/caddy.gpg" || die "не удалось скачать ключ репозитория Caddy"
      gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg "$tmp/caddy.gpg"
      fetch 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' /etc/apt/sources.list.d/caddy-stable.list || die "не удалось скачать список репозитория Caddy"
      chmod o+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg /etc/apt/sources.list.d/caddy-stable.list
      as_root apt-get update -qq
      pkg_install caddy ;;
    dnf)
      pkg_install 'dnf-command(copr)'
      as_root dnf copr enable -y @caddy/caddy
      pkg_install caddy ;;
    zypper|pacman) pkg_install caddy ;;
    *) die "не умею ставить Caddy на этой системе: поставьте вручную и запустите установщик снова" ;;
  esac
}

if ! command -v caddy >/dev/null 2>&1; then
  say "Ставлю Caddy (ingress)..."
  install_caddy
fi

MARK='# managed-by: lite-deploy'
mkdir -p /etc/caddy/conf.d
if ! grep -qF "$MARK" /etc/caddy/Caddyfile 2>/dev/null; then
  [ -f /etc/caddy/Caddyfile ] && cp /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.bak.$(date +%Y%m%d%H%M%S)"
  cat > /etc/caddy/Caddyfile <<EOF
$MARK
# Каждый проект добавляет свой файл /etc/caddy/conf.d/<проект>.caddy. Не правьте этот файл руками.
import /etc/caddy/conf.d/*.caddy
EOF
fi
[ -f /etc/caddy/conf.d/00-placeholder.caddy ] || echo '# заглушка: чтобы маска import всегда находила файл' > /etc/caddy/conf.d/00-placeholder.caddy
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null 2>&1 || die "Caddyfile не проходит проверку: caddy validate --config /etc/caddy/Caddyfile"
systemctl enable --now caddy
systemctl reload caddy

say "Ставлю агента (systemd)..."
"$prefix/lite-deploy" agent install

say ""
say "Сервер готов. Управлять им могут администраторы системы (группа sudo или wheel):"
say "  на вашем компьютере: lite-deploy ui --host пользователь@этот-сервер"
