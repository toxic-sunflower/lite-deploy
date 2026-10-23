# Журнал ручной установки сервера

Идём по шагам руками, всё записываем. Из этого журнала потом делаются настоящие скрипт и скилл. Черновики скриптов в корне папки до конца прохода не считаются проверенными.

## Исходные данные

- ОС: Ubuntu 26.04.1 LTS. Сервер пустой.
- Вход: сначала только под root.

## Гипотезы, которые надо проверить на сервере (могут изменить скрипты)

- `postgresql-18` есть в архиве Ubuntu 26.04: тогда репозиторий PGDG (`03-postgres.sh`) не нужен. Проверка: `apt-cache policy postgresql-18`.
- `sudo` может быть `sudo-rs` (по умолчанию с Ubuntu 25.10). Проверка: `sudo --version`.
- Версия podman в 26.04 (скрипт `02-podman.sh` ставит пакеты `passt`, `slirp4netns`: убедиться, что они есть).
- Службу SSH называет `ssh` или `ssh.socket`: убедиться, что `systemctl reload ssh` работает.

## Шаги

### 1. Обычный пользователь с ключом: сделано

Порядок, который сработал: ключ на своём компьютере (`ssh-keygen -t ed25519`), пользователь на сервере (`adduser`, `usermod -aG sudo`), открытый ключ с компьютера через `ssh-copy-id`, проверка `ssh` и `sudo whoami`.

Урок для скилла: копировать `authorized_keys` из root не нужно (работает только если root входит по ключу, и привязывает пользователя к ключам root). Правильно: ключ создаётся первым, на машине владельца, и кладётся новому пользователю через `ssh-copy-id`. Вход под root отключается в самом конце (шаг защиты SSH), а не сразу.

### 2. Обновление системы и проверка гипотез

Результаты проверок (Ubuntu 26.04.1, кодовое имя `resolute`):

- `sudo` это `sudo-rs 0.2.13` (гипотеза подтвердилась). Для скриптов `sudo bash script.sh` и `SUDO_USER` работают как обычно.
- `postgresql-18` 18.6 есть в основном архиве Ubuntu (`main`, обновления `resolute-updates` и `resolute-security`). **Репозиторий PGDG не нужен**: из `03-postgres.sh` убрать шаг PGDG, ставить `postgresql-18` из архива.
- `podman` 5.7.0 есть (`universe`). Подходит.
- `caddy` в архиве 2.6.2 (`universe`), старый. **Ставить из официального репозитория Caddy** (как в `04-caddy.sh`).
- `passt` и `slirp4netns` есть (`universe`). Ставить оба.
- Не получены: вывод `systemctl list-unit-files 'ssh*'`, `uname -r`, нужна ли перезагрузка после `apt upgrade`.

Официальные репозитории проектов (проверено по их сайтам, 2026-10-23):
- PostgreSQL: есть PGDG, включая `resolute-pgdg`. Не используем: версия 18.6 в `main` Ubuntu с обновлениями безопасности. Вернёмся, если понадобятся более свежие минорные версии.
- Caddy: есть официальный репозиторий (Cloudsmith). Используем, потому что в Ubuntu 2.6.2.
- Podman: отдельного репозитория для Ubuntu нет, проект сам рекомендует `apt install podman` из репозиториев дистрибутива.
- Принцип: каждый внешний репозиторий это ещё один ключ доверия и источник обновлений, подключаем только там, где пакет в Ubuntu заметно устарел.

Для скилла и скрипта: проверять доступность пакетов через `apt-cache policy` до установки и выбирать источник по результату, а не зашивать репозиторий.

Шаг 2 сделан: `sudo apt update`, `sudo apt upgrade -y`; перезагрузка не потребовалась.

### 3. Брандмауэр ufw: сделано

SSH слушает на 22 (IPv4 и IPv6), порт держит и systemd (pid 1): активация через `ssh.socket`.

Порядок, который сработал (сначала разрешающие правила, потом запрещающие): `ufw limit 22/tcp`, `ufw allow 80/tcp`, `443/tcp`, `443/udp`, `default allow outgoing`, `default deny incoming`, проверка `ufw show added`, `ufw enable`, `ufw status verbose`. Итог: `Default: deny (incoming), allow (outgoing), disabled (routed)`, правила дублируются для IPv6.

Урок для скилла: перед включением проверять `ufw status` (должен быть inactive) и `ufw show added` (есть правило SSH), порт SSH определять через `ss -tlnp | grep ssh`, а не гадать. После включения проверять вход второй сессией.

### 4. fail2ban: сделано

`apt install fail2ban`, файл `/etc/fail2ban/jail.d/sshd.local` (`enabled`, `port = 22`, `backend = systemd`, `maxretry = 5`, `findtime = 10m`, `bantime = 1h`), `systemctl enable --now fail2ban`. Проверка: `fail2ban-client status sshd` показывает `Journal matches: _SYSTEMD_UNIT=ssh.service + _COMM=sshd`. `backend = systemd` обязателен: файла `auth.log` может не быть.

Уроки: адрес владельца динамический, поэтому `ignoreip` не используем; снять бан можно по списку из `fail2ban-client status sshd` или `unban --all`; узнать адрес своей сессии: `echo $SSH_CLIENT`. Риск самобана при многих ключах в агенте (`IdentitiesOnly yes`). Нужна запасная дверь: консоль хостера.

### 5. Автообновления безопасности: сделано

`apt install unattended-upgrades`, файл `/etc/apt/apt.conf.d/20auto-upgrades` (`Update-Package-Lists "1"`, `Unattended-Upgrade "1"`). Автоперезагрузка выключена намеренно (сервер общий): раз в одну-две недели проверять `/var/run/reboot-required`.

### 6. Время: ничего ставить не пришлось

`timedatectl`: часы синхронизированы, NTP активен, пояс `Etc/UTC`. Работает `chrony` (в Ubuntu 26.04 он стоит из коробки, `systemd-timesyncd` отсутствует). Урок: в скрипте не ставить `chrony`, а проверять `timedatectl` и ставить синхронизацию, только если её нет.

### 7. podman: сделано (владелец подтвердил, вывод проверок не присылал)

`apt install podman uidmap passt slirp4netns dbus-user-session systemd-container` (все из `universe`, podman 5.7.0). Проверки: `podman --version`, `grep serafim /etc/subuid /etc/subgid` (диапазоны есть), `podman run --rm docker.io/library/hello-world` от обычного пользователя. Для скилла: после установки обязательно запускать тестовый контейнер от обычного пользователя и проверять `subuid`/`subgid`.

### 8. PostgreSQL 18 из архива Ubuntu: сделано

`apt install postgresql-18` (18.6 из `main`), кластер `18/main` создаётся сам. Проверки: `pg_lsclusters`, `ss -tlnp | grep postgres` (только `127.0.0.1:5432` и `[::1]:5432`), `sudo -u postgres psql -c "select version();"`. В скрипте: PGDG не подключать, проверять, что порт слушается только на localhost.

### 9. Caddy из официального репозитория: сделано

Репозиторий Cloudsmith (ключ в `/usr/share/keyrings/caddy-stable-archive-keyring.gpg`, источник в `/etc/apt/sources.list.d/caddy-stable.list` с `signed-by`), `apt install caddy`. Главный `/etc/caddy/Caddyfile` только `import /etc/caddy/conf.d/*.caddy` (исходный сохранён как `Caddyfile.orig`), заглушка `conf.d/00-placeholder.caddy`. Проверка: `caddy validate`, `systemctl reload caddy`, `ss -tlnp | grep caddy` показывает только `127.0.0.1:2019` (80 и 443 не слушаются, пока нет сайтов).

### 10. Запрет входа под root по SSH: сделано

Файл `/etc/ssh/sshd_config.d/00-hardening.conf` с одной строкой `PermitRootLogin no` (имя с `00-`, потому что первое найденное значение побеждает), проверка `sshd -t`, `systemctl reload ssh`, проверка `sshd -T | grep -i permitrootlogin`. Запрещён только вход по SSH: пользователь root остаётся (`sudo -i`, консоль хостера). Вход по паролю для обычных пользователей пока не отключали.

Урок для скилла: при `PermitRootLogin no` клиент всё равно видит приглашение `password:`, отказ приходит после ввода (так задумано, чтобы не раскрывать существование пользователя). Надёжная проверка: `sshd -T | grep -i permitrootlogin` или `ssh -o PreferredAuthentications=publickey root@сервер` (должно быть `Permission denied (publickey)`). Откат: удалить файл и `systemctl reload ssh` из уже открытой сессии.

## Этап «проекты»

Принцип: у каждого проекта свой системный пользователь без пароля и без `sudo`. Под ним работают контейнеры проекта (rootless podman) и приходит деплой (ключ ограничим позже).

### 11. Пользователь проекта `fanvue-games`: сделано

`adduser --disabled-password --gecos "" fanvue-games` (в выводе безобидное `usermod: no changes`), `loginctl enable-linger fanvue-games` (службы пользователя живут без входа и стартуют при загрузке сервера). Проверки: диапазоны в `/etc/subuid` и `/etc/subgid` есть (`fanvue-games:165536:65536`), тестовый контейнер от этого пользователя: `sudo machinectl shell fanvue-games@ /usr/bin/podman run --rm docker.io/library/hello-world` (обычный `sudo -u` для rootless podman не годится: нужна сессия через `machinectl`). Не проверено: права домашней папки (`ls -ld /home/fanvue-games`, ожидаем `drwxr-x---`).

### 12. База проекта и доступ через unix-сокет: сделано

Выбран unix-сокет (владелец, 2026-10-23). Последовательность в одной вкладке: пароль `PW=$(openssl rand -base64 24 | tr -d '/+=')`; роль и база через `sudo -u postgres psql -v pw="$PW" <<'SQL'` (`CREATE ROLE fanvue_games LOGIN PASSWORD :'pw'`, `CREATE DATABASE fanvue_games OWNER fanvue_games`, `REVOKE ALL ON DATABASE fanvue_games FROM PUBLIC`); правило в `/etc/postgresql/18/main/pg_hba.conf` выше `local all all peer`: `local fanvue_games fanvue_games scram-sha-256` (вставлено через `sed -i '/pattern/i ...'`), `systemctl reload postgresql`; проверка входа `PGPASSWORD="$PW" psql -h /var/run/postgresql -U fanvue_games -d fanvue_games`; файл `/home/fanvue-games/.config/fanvue-games/app.env` (права 600, владелец `fanvue-games`, создан через `sudo -u fanvue-games sh -c 'umask 077; cat > ...'`) со строкой `ConnectionStrings__Default=Host=/var/run/postgresql;Database=fanvue_games;Username=fanvue_games;Password=...`; затем `unset PW`.

Уроки для скилла: `pg_hba.conf` читает только `postgres`, поэтому `grep` по нему нужен через `sudo`; создавать каталоги в чужом домашнем каталоге от имени самого пользователя (`sudo -u пользователь mkdir -p`), иначе промежуточные каталоги станут принадлежать root; перед `sudo -u пользователь` делать `cd /` (в домашнюю папку `serafim` у других пользователей доступа нет); пароль нигде не печатать и не класть в историю. Пароль БД хранится только на сервере (решение по остальным секретам ещё не принято).

## Итог этапа «установка ПО»

Сделано: пользователь с ключом, обновления, ufw (22/80/443), fail2ban для SSH, автообновления безопасности, время (chrony из коробки), podman 5.7.0, PostgreSQL 18.6 (только localhost), Caddy из официального репозитория (пустой, с conf.d).

Не сделано (намеренно, в конце): защита SSH (отключить вход по паролю и под root).

Следующий этап (проекты): пользователь проекта и rootless podman, роль и база PostgreSQL, доступ контейнера к базе, сайт в Caddy, службы Quadlet, вход в реестр GHCR, GitHub Actions.

## Данные сервера (не секреты)

- Адрес: 187.7.72.122, хост `srv2049578`, вход: `ssh serafim@187.7.72.122` (по ключу), под root по SSH вход закрыт.
- Пользователь проекта: `fanvue-games` (без пароля и без sudo, linger включён), домен проекта: `fanvue-games.myapps.agency` (Cloudflare не настроен).
- Состояние на 2026-10-23: шаги 1–12 выполнены. Не выполнено: отключение входа по паролю (ждём ответа про консоль хостера), Cloudflare, сайт Caddy.

## Следующие шаги (деплой проекта)

Список лежит в TODO.md репозитория проекта (раздел «Деплой на сервер»): ключ деплоя, привратник, sudoers, файлы настроек и секреты, вход в GHCR, каталоги Caddy, настройки GitHub, потом Cloudflare и первая выкатка.
