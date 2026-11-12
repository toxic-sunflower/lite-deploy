#!/bin/sh
# Установщик lite-deploy: скачивает бинарник из GitHub Releases, проверяет SHA256 и подпись minisign,
# кладёт в /usr/local/bin. Ничего больше на сервере не меняет.
#
#   curl -fsSL https://toxic-sunflower.github.io/lite-deploy/install.sh | sh
#   curl -fsSL https://toxic-sunflower.github.io/lite-deploy/install.sh | sh -s -- --version v0.1.0
#
# Параметры (или переменные окружения):
#   --version <тег>       LITE_DEPLOY_VERSION   версия (по умолчанию последняя)
#   --prefix <каталог>    LITE_DEPLOY_PREFIX    куда ставить (по умолчанию /usr/local/bin)
#   --skip-sig            LITE_DEPLOY_SKIP_SIG=1  не проверять подпись (только SHA256; небезопасно)
#   LITE_DEPLOY_BASE_URL  откуда качать (для проверки установщика; по умолчанию GitHub Releases)
#
# Открытый ключ minisign, которым подписаны релизы. Совпадает с minisign.pub в репозитории.
MINISIGN_PUBKEY="REPLACE_WITH_MINISIGN_PUBLIC_KEY"
REPO="toxic-sunflower/lite-deploy"

set -eu

say() { printf '%s\n' "$*"; }
die() { printf 'ОШИБКА: %s\n' "$*" >&2; exit 1; }

version=${LITE_DEPLOY_VERSION:-latest}
prefix=${LITE_DEPLOY_PREFIX:-/usr/local/bin}
skip_sig=${LITE_DEPLOY_SKIP_SIG:-0}

while [ $# -gt 0 ]; do
  case $1 in
    --version) [ $# -ge 2 ] || die "--version требует значение"; version=$2; shift ;;
    --prefix)  [ $# -ge 2 ] || die "--prefix требует значение"; prefix=$2; shift ;;
    --skip-sig) skip_sig=1 ;;
    -h|--help) sed -n '2,15p' "$0" 2>/dev/null || true; exit 0 ;;
    *) die "неизвестный параметр: $1" ;;
  esac
  shift
done

[ "$(uname -s)" = Linux ] || die "поддерживается только Linux"
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

if [ "$skip_sig" != 1 ]; then
  [ "$MINISIGN_PUBKEY" != "REPLACE_WITH_MINISIGN_PUBLIC_KEY" ] || die "в установщик не вписан ключ подписи (проблема релиза, сообщите автору)"
  command -v minisign >/dev/null 2>&1 || die "нужен minisign для проверки подписи: sudo apt install minisign (или --skip-sig, только SHA256, небезопасно)"
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
