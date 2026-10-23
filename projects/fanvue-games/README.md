# FanVue Games: сервер и деплой

Эта папка (`projects/fanvue-games/`) хранит всё, что относится к серверу и деплою проекта, и **нарочно лежит вне репозитория приложения**: репозиторий не должен подсказывать устройство инфраструктуры. Папка задумана как образец для будущей системы развёртывания разных проектов (`../../VISION.md`, общая схема `../../DEPLOY-PATTERN.md`, журнал ручной настройки сервера `../../WALKTHROUGH.md`).

Боевой сервер: общий Linux-сервер, Caddy и PostgreSQL на хосте, приложение в rootless podman под отдельным пользователем `fanvue-games`. Трафик приходит через Cloudflare и туннель `cloudflared` (порты 80 и 443 для этого проекта не нужны). Адрес: `https://fanvue-games.myapps.agency` (пока на домене владельца, потом перепривязать).

В репозитории приложения остаются только `Dockerfile`-ы, workflows `.github/workflows/deploy.yml` и `rollback.yml` (копии лежат в `github-actions/`) и локальный `compose.local.yaml`.

## Как это работает

```
push в main ─┐                                      ┌─ Cloudflare ─ туннель ─┬─ Caddy 127.0.0.1:18080 ─┬─ сервис: слот blue 18081 / green 18082
кнопка из    ├─ GitHub Actions: тесты → образы → SSH ┘                        │                        │
ветки release┘                                                                └─ Caddy 127.0.0.1:18090 ─┴─ админка: слот blue 18091 / green 18092
                                                                                 (имя admin.…, за Cloudflare Access)
```

- **Образы** собирает GitHub Actions (`deploy.yml`) и кладёт в приватный GHCR: теги `<полный хэш коммита>` и `latest`. Сервер исходников не хранит.
- **Blue-green** для сервиса и админки сразу. Две копии каждой части, активен один слот. Новая версия поднимается в свободном слоте, проходит проверку (`/health/ready` обеих частей отвечает версией именно этого коммита), и только потом Caddy переключается на неё. Не прошла проверка: трафик остаётся на старой версии. Старый слот остаётся работать для отката. Состояние одно на обе части.
- **Автодеплой:** слияние в `main`. **По кнопке:** Actions → deploy → Run workflow, выбрать ветку `release` (из других веток кнопка не работает). **Откат:** Actions → rollback → Run workflow: трафик возвращается на прошлый слот за секунды.
- **Ключ деплоя ограничен.** Он умеет на сервере только `deploy <хэш>`, `rollback`, `status` (принудительная команда привратника `fanvue-ssh-gate`), оболочки у него нет.
- Включение выкатки: переменная репозитория `DEPLOY_ENABLED=true`. Пока её нет, `deploy.yml` только тестирует и собирает образы.

## Что в папке и как оно попадает на сервер

GitHub по SSH присылает только команду «деплой такого-то коммита». Всё, что выполняется на сервере, ставится на него вручную и меняется вручную (позже это автоматизирует система развёртывания). Так ключ деплоя не может выполнить на сервере присланный им код: при взломе GitHub он сможет лишь запустить деплой образа из GHCR.

| Путь | Как попадает на сервер |
|---|---|
| `bin/fanvue-ssh-gate` (привратник ключа), права 700 | **Вручную** в `/home/fanvue-games/bin/` |
| `bin/fanvue-deploy` (скрипт blue-green), права 700 | **Вручную** в `/home/fanvue-games/bin/`; после правки скрипта скопировать заново |
| `quadlet/*.container` (четыре юнита: сервис и админка, blue и green), права 600 | **Вручную** в `/home/fanvue-games/.config/containers/systemd/`, затем `systemctl --user daemon-reload` (от имени `fanvue-games`) |
| `caddy/fanvue-games.caddy` и `caddy/fanvue-games-admin.caddy` (сайты) | **Вручную** в `/etc/caddy/conf.d/` (нужен root, менять приходится редко) |
| `caddy/fanvue-games.fallback.caddy.disabled` | Лежит только здесь, на сервер кладётся при сбое Cloudflare (см. ниже) |

Рядом на сервере создаются руками (это секреты и настройки):
- `/home/fanvue-games/.config/fanvue-games/app.env`: `ConnectionStrings__Default` (уже создан), `Auth__SessionSecret` (случайная строка от 32 символов), `Fanvue__ClientId`, `Fanvue__ClientSecret`, `Fanvue__RedirectUri=https://fanvue-games.myapps.agency/api/auth/callback`, `Fanvue__AppUuid`, `Fanvue__WebhookSecret`.
- `/home/fanvue-games/.config/fanvue-games/admin.env`: `ConnectionStrings__Default` (та же), `Admin__Username`, `Admin__Password` (не короче 12 символов).
- `/home/fanvue-games/.config/fanvue-games/deploy.conf`: `IMAGE_API=ghcr.io/<владелец>/fanvue-games-api`, `IMAGE_ADMIN=ghcr.io/<владелец>/fanvue-games-admin`.
- Токен туннеля `cloudflared` (вводит владелец на сервере).
- Начальные файлы upstream для Caddy: `/etc/caddy/upstreams/fanvue-games.caddy` и `/etc/caddy/upstreams/fanvue-games-admin.caddy` (владелец `fanvue-games`; дальше их ведёт скрипт деплоя).

## База данных

Postgres общий на сервере, у каждого проекта своя роль и база; доступ контейнера через unix-сокет хоста (каталог `/var/run/postgresql` монтируется в контейнер).

Как сделано для этого проекта (в одной вкладке терминала, пароль нигде не печатается и не попадает в историю):
1. Пароль: `PW=$(openssl rand -base64 24 | tr -d '/+=')`.
2. Роль и база: `sudo -u postgres psql -v pw="$PW"` и команды `CREATE ROLE fanvue_games LOGIN PASSWORD :'pw'`, `CREATE DATABASE fanvue_games OWNER fanvue_games`, `REVOKE ALL ON DATABASE fanvue_games FROM PUBLIC`. Роль без `SUPERUSER`, `CREATEDB`, `CREATEROLE` (по умолчанию так и есть).
3. Правило в `/etc/postgresql/18/main/pg_hba.conf` **выше** общего `local all all peer`: `local fanvue_games fanvue_games scram-sha-256`; затем `sudo systemctl reload postgresql`. В правилах для проектов не использовать `all` ни в базе, ни в роли.
4. Проверка входа: `PGPASSWORD="$PW" psql -h /var/run/postgresql -U fanvue_games -d fanvue_games`.
5. Файл `/home/fanvue-games/.config/fanvue-games/app.env` (права 600, владелец `fanvue-games`, создавать от имени пользователя: `sudo -u fanvue-games sh -c 'umask 077; cat > ...'`) со строкой `ConnectionStrings__Default=Host=/var/run/postgresql;Database=fanvue_games;Username=fanvue_games;Password=...`. Затем `unset PW`.

Уроки: `pg_hba.conf` читает только `postgres` (`sudo grep`); перед `sudo -u пользователь` делать `cd /`; каталоги в чужом домашнем каталоге создавать от имени самого пользователя. Приложение само применяет миграции при старте под этой же ролью (она владелец базы). Админка схему не меняет.

## Разовая настройка

Проходится по шагам вручную, с проверкой после каждого. Подробный список с порядком: `TODO.md`.

1. **Привратник, скрипт `fanvue-deploy` и юниты Quadlet** на сервере и ключ деплоя: пара ключей, открытая часть в `authorized_keys` пользователя `fanvue-games` с принудительной командой и `restrict`, закрытая в секретах GitHub.
2. **Настройки на сервере** (секреты и параметры): `app.env`, `admin.env`, `deploy.conf`.
3. **Право переключать Caddy:** одно правило `sudoers`, разрешающее пользователю `fanvue-games` только `/usr/bin/systemctl reload caddy`.
4. **Вход сервера в GHCR** (токен только на чтение пакетов) под пользователем `fanvue-games`.
5. **GitHub:** окружение `production` (ветки `main` и `release`), секреты окружения `DEPLOY_SSH_KEY`, `DEPLOY_KNOWN_HOSTS`, `DEPLOY_HOST`, `DEPLOY_USER`, переменная репозитория `DEPLOY_ENABLED=true`, ветка `release`.
6. **Cloudflare, туннель и Caddy** (решение 2026-10-24: туннель, ручной запасной план):
   1. Установить `cloudflared` на сервере (официальный репозиторий) как службу.
   2. В Cloudflare Zero Trust → Networks → Tunnels создать туннель, токен установить на сервере самому (`read -rs`).
   3. Добавить в туннель два имени: `fanvue-games.myapps.agency` → `http://localhost:18080` и `admin.fanvue-games.myapps.agency` → `http://localhost:18090`. DNS-записи создаются автоматически.
   4. Положить файлы сайтов в `/etc/caddy/conf.d/` и начальные файлы upstream в `/etc/caddy/upstreams/`, затем `systemctl reload caddy`.
   5. Zero Trust → Access → Applications: приложение на имя админки, правило «пускать такие-то почты». Источник защищён тем, что сайты слушают только localhost.
   6. Проверить: запрос на IP сервера с подстановкой имени ничего не отдаёт, сайт по имени работает, админка просит вход через Access.
7. **Первая выкатка** и проверка blue-green и отката.

## Ограничения и что учесть

- **Миграции базы.** Сервис применяет миграции при старте, а прежняя версия ещё работает на той же базе. Каждая миграция должна быть совместима с предыдущей версией кода (добавлять, не ломать и не удалять сразу). Откат переключает только код, базу назад не возвращает.
- **Загрузка файлов через Cloudflare** на бесплатном тарифе до 100 МБ на запрос (в приложении лимит 1,5 ГБ, в Caddy стоит 100 МБ). Для больших файлов нужно другое решение (отдельный адрес без прокси или загрузка напрямую во Fanvue).
- **Cloudflare и вебхуки Fanvue.** Если включена защита от ботов или WAF, нужно правило, которое пропускает `POST /api/webhooks/fanvue` без проверки.
- **Защита источника.** Сайты слушают только `127.0.0.1`, поэтому в обход туннеля до них не добраться, и заголовок `CF-Connecting-IP` подделать нельзя (его ставит Cloudflare). Если запасной файл включён, этой защиты нет.
- Пока приложение не получило свой `Fanvue__*` и `Auth__SessionSecret`, оно в боевом режиме не запустится: это сделано намеренно.

## Если Cloudflare недоступен (ручной запасной план)

Включать только на время сбоя, возвращать сразу после. Нужно около минуты:
1. В Cloudflare DNS заменить запись `fanvue-games` (CNAME на туннель) на запись A с адресом сервера, прокси выключен («только DNS», серое облако).
2. Скопировать `caddy/fanvue-games.fallback.caddy.disabled` на сервер и положить как `/etc/caddy/conf.d/fanvue-games-fallback.caddy`, затем `sudo systemctl reload caddy`. Caddy сам получит публичный сертификат (нужны открытые 80 и 443 и DNS на сервер).
3. Возврат: удалить `/etc/caddy/conf.d/fanvue-games-fallback.caddy`, `sudo systemctl reload caddy`, в DNS вернуть CNAME на туннель с включённым прокси.

Админка в этом режиме остаётся только через SSH-туннель (Cloudflare Access не работает). Лимит 100 МБ на загрузку на время сбоя не действует (в запасном файле 1500 МБ, как в приложении).
