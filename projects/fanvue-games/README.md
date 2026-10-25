# FanVue Games: сервер и деплой

Эта папка (`projects/fanvue-games/`) хранит всё, что относится к серверу и деплою проекта, и **нарочно лежит вне репозитория приложения**: репозиторий не должен подсказывать устройство инфраструктуры. Папка задумана как образец для будущей системы развёртывания разных проектов (`../../VISION.md`, общая схема `../../DEPLOY-PATTERN.md`, журнал ручной настройки сервера `../../WALKTHROUGH.md`). Состояние: `STATUS.md`, очередь: `TODO.md`, решения: `decisions.md`.

Боевой сервер: общий Linux-сервер, Caddy и PostgreSQL на хосте, приложение в rootless podman под отдельным пользователем `fanvue-games`. Трафик приходит через Cloudflare и туннель `cloudflared` (порты 80 и 443 для этого проекта не нужны). Адреса: `https://fanvue-games.prozorov.site` (сервис) и `https://admin.fanvue-games.prozorov.site` (админка). Домен пока свой у владельца, потом перепривязать (имена в файлах Caddy, в туннеле, в `Fanvue__RedirectUri`, в настройках приложения у Fanvue).

В репозитории приложения остаются только `Dockerfile`-ы, workflows `.github/workflows/deploy.yml` и `rollback.yml` (копии лежат в `github-actions/`) и локальный `compose.local.yaml`.

## Как это работает

```
слияние в main ─┐                                     ┌─ Cloudflare ─ туннель ─┬─ Caddy 127.0.0.1:18080 ─┬─ сервис: слот blue 18081 / green 18082
кнопка Run      ├─ GitHub Actions: тесты → образы → SSH ┘                        │                        │
workflow (main) ┘                                                                └─ Caddy 127.0.0.1:18090 ─┴─ админка: слот blue 18091 / green 18092
                                                                                    (имя admin.…, за Cloudflare Access)
```

Порты: 18080 и 18090 слушает Caddy (сюда приходит туннель); 18081/18082 и 18091/18092 слушают контейнеры (слоты). Все только на `127.0.0.1`.

- **Образы** собирает GitHub Actions (`deploy.yml`) и кладёт в приватный GHCR: теги `<полный хэш коммита>` и `latest`. Сервер исходников не хранит.
- **Blue-green** для сервиса и админки сразу. Две копии каждой части, активен один слот. Новая версия поднимается в свободном слоте, проходит проверку (`/health/ready` обеих частей отвечает версией именно этого коммита), и только потом Caddy переключается на неё. Не прошла проверка за 90 с: свободный слот останавливается, трафик остаётся на старой версии. Старый слот остаётся работать для отката. Состояние одно на обе части.
- **Автодеплой:** слияние в `main`. **По кнопке:** Actions → deploy → Run workflow, ветка `main` (из других веток первая задача пропускается). **Откат:** Actions → rollback → Run workflow: трафик возвращается на прошлый слот за секунды.
- **Ключ деплоя ограничен.** Он умеет на сервере только `deploy <хэш>`, `rollback`, `status` (принудительная команда привратника `fanvue-ssh-gate`), оболочки у него нет.
- **Выключатель выкатки на каждое окружение свой:** переменная репозитория с именем окружения. Сейчас `DEPLOY_PRODUCTION_ENABLED=true` (создана 2026-10-25); для стейджинга будет `DEPLOY_STAGING_ENABLED`. Проверка `if` у задачи видит только переменные репозитория, а переменные окружения GitHub ей недоступны (они доступны лишь внутри шагов, когда задача уже вошла в окружение и к его секретам). Пока переменной нет, `deploy.yml` только тестирует и собирает образы.

## Ветки и окружения GitHub (git flow, решение владельца 2026-10-25)

| Ветка | Назначение |
|---|---|
| `main` | Прод. Слияние = деплой на сервер. Кнопка Run workflow работает только из `main`. |
| `develop` | Ветка по умолчанию. Фичи ветвятся от неё (`feature/…`) и сливаются обратно через PR. На сервер не катится. |
| `release/<версия>` | Подготовка выпуска, от `develop`; потом PR в `main` и слияние обратно в `develop`. Позже: деплой на стейджинг по кнопке (окружение `staging` создано, workflow для него ещё нет). |
| `hotfix/…` | Срочная правка прода: от `main`, PR в `main`, потом слить `main` в `develop`. |

- Ветки с именем `release` (без косой черты) быть не должно: git не допускает одновременно `release` и `release/1.0`.
- Окружение `production`: деплой только из `main`; секреты окружения `DEPLOY_SSH_KEY`, `DEPLOY_KNOWN_HOSTS`, `DEPLOY_HOST`, `DEPLOY_USER`.
- Окружение `staging`: деплой разрешён из `release/*`, секретов пока нет (понадобятся свой сервер или адрес, своя база, свои секреты и отдельный workflow).
- **Защита веток (rulesets и классическая) на бесплатном тарифе недоступна для приватного репозитория** (API: 403 «Upgrade to GitHub Pro or make this repository public»). Прямые пуши и force push в `main` не запрещены. Держимся на 2FA аккаунта и на том, что окружение `production` пускает только `main`. Решение (Pro или без защиты) отложено.
- Лимиты бесплатного тарифа (цифры из памяти, сверять в `github.com/settings/billing`): около 2000 минут Actions в месяц для приватных репозиториев; прогон занимает около 4 минут (около 500 прогонов). Приватные пакеты GHCR: около 500 МБ бесплатно, образы с тегом-хэшем копятся, периодически чистить старые версии.

## Что в папке и как оно попадает на сервер

GitHub по SSH присылает только команду «деплой такого-то коммита». Всё, что выполняется на сервере, ставится на него вручную и меняется вручную (позже это автоматизирует система развёртывания). Так ключ деплоя не может выполнить на сервере присланный им код: при взломе GitHub он сможет лишь запустить деплой образа из GHCR.

| Путь | Как попадает на сервер |
|---|---|
| `bin/fanvue-ssh-gate` (привратник ключа), права 700 | **Вручную** в `/home/fanvue-games/bin/` |
| `bin/fanvue-deploy` (скрипт blue-green), права 700 | **Вручную** в `/home/fanvue-games/bin/`; после правки скрипта скопировать заново |
| `quadlet/*.container` (четыре юнита: сервис и админка, blue и green), права 600 | **Вручную** в `/home/fanvue-games/.config/containers/systemd/`, затем `systemctl --user daemon-reload` (от имени `fanvue-games`) |
| `caddy/fanvue-games.caddy` и `caddy/fanvue-games-admin.caddy` (сайты) | **Вручную** в `/etc/caddy/conf.d/` (нужен root, менять приходится редко) |
| `caddy/fanvue-games.fallback.caddy.disabled` | Лежит только здесь, на сервер кладётся при сбое Cloudflare (см. ниже) |

Копирование: из WSL `scp файлы serafim@187.7.72.122:/tmp/`, потом на сервере `sudo install`/`cp` с нужным владельцем и правами и `rm` временных файлов в `/tmp`.

Рядом на сервере создаются руками (это секреты и настройки):
- `/home/fanvue-games/.config/fanvue-games/app.env`: `ConnectionStrings__Default`, `Auth__SessionSecret` (случайная строка, генерируется на сервере: `openssl rand -hex 32`), `Fanvue__ClientId`, `Fanvue__ClientSecret`, `Fanvue__RedirectUri=https://fanvue-games.prozorov.site/api/auth/callback` (должен совпадать с адресом в настройках приложения у Fanvue), `Fanvue__AppUuid`, `Fanvue__WebhookSecret`.
- `/home/fanvue-games/.config/fanvue-games/admin.env`: `ConnectionStrings__Default` (та же строка), `Admin__Username`, `Admin__Password` (не короче 12 символов, без пробелов, кавычек, `#` и `\`).
- `/home/fanvue-games/.config/fanvue-games/deploy.conf`: `IMAGE_API=ghcr.io/toxic-sunflower/fanvue-games-api`, `IMAGE_ADMIN=ghcr.io/toxic-sunflower/fanvue-games-admin`.
- `/home/fanvue-games/.config/containers/auth.json`: вход в GHCR (создаёт `podman login --authfile`, см. ниже).
- `/etc/cloudflared/tunnel.env` и `/etc/systemd/system/cloudflared.service`: туннель (см. ниже).
- Правило `/etc/sudoers.d/fanvue-games-caddy`.
- Начальные файлы upstream для Caddy: `/etc/caddy/upstreams/fanvue-games.caddy` и `/etc/caddy/upstreams/fanvue-games-admin.caddy` (владелец `fanvue-games`; дальше их ведёт скрипт деплоя). Начальное содержимое: `respond "not deployed" 503` (валидный сниппет, сайт отвечает заглушкой до первой успешной выкатки).

Все файлы с секретами: права 600, владелец `fanvue-games` (кроме файлов туннеля: root). Секреты вводит владелец сам скрытым вводом (`read -rs`), в чат они не попадают. Пример ввода нескольких значений с записью в файл от имени пользователя проекта:

```bash
for k in Fanvue__ClientId Fanvue__ClientSecret Fanvue__RedirectUri Fanvue__AppUuid Fanvue__WebhookSecret; do read -rs -p "$k: " v; echo; printf '%s=%s\n' "$k" "$v" | sudo -u fanvue-games tee -a /home/fanvue-games/.config/fanvue-games/app.env > /dev/null; done; unset v
```

Проверка без показа значений: `sudo -u fanvue-games cut -d= -f1 /home/fanvue-games/.config/fanvue-games/app.env` (только имена ключей). Строку подключения для `admin.env` берём из `app.env` без показа на экране: `sudo -u fanvue-games sh -c 'umask 077; grep "^ConnectionStrings__Default=" …/app.env > …/admin.env'`.

Откуда значения Fanvue: `Fanvue__AppUuid` из кабинета разработчика (в адресе страницы приложения; в списке приложений он может не выделяться: открыть приложение или «Просмотреть код»). `Fanvue__WebhookSecret` выдаёт Fanvue (по стороннему описанию: вкладка Events приложения «View signing secret», либо поле `signingSecret` в ответе на создание подписки через API, видно один раз; вид `whsec_` плюс 64 символа, вводить целиком). Вебхук можно зарегистрировать только когда у сайта есть публичный адрес; до этого поле можно оставить пустым, но продажа билетов работать не будет. Официальной документации Fanvue для этого мы не проверяли.

## База данных

Postgres общий на сервере, у каждого проекта своя роль и база; доступ контейнера через unix-сокет хоста (каталог `/var/run/postgresql` монтируется в контейнер).

Как сделано для этого проекта (в одной вкладке терминала, пароль нигде не печатается и не попадает в историю):
1. Пароль: `PW=$(openssl rand -base64 24 | tr -d '/+=')`.
2. Роль и база: `sudo -u postgres psql -v pw="$PW"` и команды `CREATE ROLE fanvue_games LOGIN PASSWORD :'pw'`, `CREATE DATABASE fanvue_games OWNER fanvue_games`, `REVOKE ALL ON DATABASE fanvue_games FROM PUBLIC`. Роль без `SUPERUSER`, `CREATEDB`, `CREATEROLE` (по умолчанию так и есть).
3. Правило в `/etc/postgresql/18/main/pg_hba.conf` **выше** общего `local all all peer`: `local fanvue_games fanvue_games scram-sha-256`; затем `sudo systemctl reload postgresql`. В правилах для проектов не использовать `all` ни в базе, ни в роли.
4. Проверка входа: `PGPASSWORD="$PW" psql -h /var/run/postgresql -U fanvue_games -d fanvue_games`.
5. Файл `/home/fanvue-games/.config/fanvue-games/app.env` (права 600, владелец `fanvue-games`, создавать от имени пользователя: `sudo -u fanvue-games sh -c 'umask 077; cat > ...'`) со строкой `ConnectionStrings__Default=Host=/var/run/postgresql;Database=fanvue_games;Username=fanvue_games;Password=...`. Затем `unset PW`.

Уроки: `pg_hba.conf` читает только `postgres` (`sudo grep`); перед `sudo -u пользователь` делать `cd /`; каталоги в чужом домашнем каталоге создавать от имени самого пользователя; **звёздочку в пути чужого домашнего каталога раскрывает оболочка `serafim`, а не `sudo`**, поэтому `sudo chown … /home/другой/*.x` не находит файлов, нужно `sudo sh -c 'chown … /home/другой/*.x'`.

### Миграции базы

Сейчас одна роль `fanvue_games` (владелец базы) на всё: сервис (он же применяет миграции), админка. Отдельной роли для миграций нет, отложено (см. `TODO.md`, «Позже»).

- **Кто мигрирует:** сервис (API) вызывает `Database.Migrate()` при старте, до начала обработки запросов. Админка схему не меняет, только пишет в лог предупреждение, если в базе есть непримёненные миграции.
- **Если миграция упала:** сервис падает при старте и не отвечает на `/health/ready`, юнит перезапускается (`Restart=always`, пауза 5 с) и снова пробует. Скрипт деплоя за 90 с не дожидается готовности, останавливает свободный слот и **оставляет трафик на старой версии**. Это следует из кода и скрипта, живой проверкой сбоя миграции пока не подтверждено (в `TODO.md` пункт «учебный сбой»).
- **Что не откатывается:** миграции, которые уже успели примениться (в том числе если сбой случился позже по другой причине, как с админкой в первой выкатке), остаются в базе, и старая версия работает уже на новой схеме. Поэтому каждая миграция обязана быть обратно совместимой с предыдущей версией кода. Откат деплоя возвращает только код. Каждая отдельная миграция выполняется EF в транзакции (в PostgreSQL DDL транзакционный), так что упавшая миграция откатывается целиком.
- **Идея на потом** (владелец 2026-10-25): отдельный запуск миграций до старта слотов под своей ролью (режим `migrate` у того же образа, шаг в `fanvue-deploy`, вторая роль БД с `GRANT` рабочей роли и `ALTER DEFAULT PRIVILEGES`). Не делаем до запуска.

## Туннель Cloudflare (`cloudflared`)

Сделано 2026-10-25. Служба запущена на сервере, у туннеля `fanvue-games` в панели статус Healthy.

1. **Установка** из официального репозитория Cloudflare (подпись пакетов проверяется ключом):
   ```bash
   sudo mkdir -p --mode=0755 /usr/share/keyrings
   curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg > /dev/null
   echo 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' | sudo tee /etc/apt/sources.list.d/cloudflared.list
   sudo apt-get update && sudo apt-get install -y cloudflared
   ```
2. **Туннель в панели:** https://one.dash.cloudflare.com → Networks → Tunnels → создать (тип Cloudflared, имя `fanvue-games`); на странице установки выбрать Debian, взять токен из показанной команды. Токен никуда не присылать.
3. **Служба.** Команду из панели (`cloudflared service install <токен>`) **не используем**: она кладёт токен в файл службы, читаемый всеми, и в список процессов, а сервер общий. Вместо этого отдельный пользователь без прав и закрытый файл с токеном:
   ```bash
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin cloudflared
   read -rs -p 'Токен туннеля: ' T; echo
   sudo mkdir -p /etc/cloudflared && printf 'TUNNEL_TOKEN=%s\n' "$T" | sudo sh -c 'umask 077; cat > /etc/cloudflared/tunnel.env'; unset T
   ```
   Файл `/etc/systemd/system/cloudflared.service`: `User=cloudflared`, `EnvironmentFile=/etc/cloudflared/tunnel.env`, `ExecStart=/usr/bin/cloudflared --no-autoupdate tunnel run`, `Restart=on-failure`, `RestartSec=5`, `NoNewPrivileges=true`, `ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`, `WantedBy=multi-user.target`. Затем `sudo systemctl daemon-reload && sudo systemctl enable --now cloudflared`, проверка `systemctl status cloudflared`. Токен читает root (systemd) при запуске, сам процесс работает от `cloudflared`.
4. **Маршруты** (в панели: туннель → Edit → вкладка Published application routes → Add a published application route; в поле Service URL **полный адрес с протоколом**, типа сервиса в новом интерфейсе нет):
   - `fanvue-games.prozorov.site` → `http://localhost:18080`
   - `admin.fanvue-games.prozorov.site` → `http://localhost:18090`
   DNS-записи Cloudflare создаёт сам (запись показывается как Tunnel или CNAME на `<id туннеля>.cfargotunnel.com`). Внутри контура протокол `http`: шифрование заканчивается на Cloudflare, Caddy слушает `http://…:18080` на `127.0.0.1`.
5. **Грабли при создании маршрутов:**
   - Если у имени уже была DNS-запись от прежнего туннеля (CNAME на другой `…cfargotunnel.com`), Cloudflare её не перезаписывает: сайт отвечает **ошибкой 1033** при статусе туннеля Healthy. Удалить старую запись в DNS (zone → DNS → Records), потом удалить и заново создать маршрут.
   - После удаления записи браузер или Windows могут держать в кэше «имя не найдено» («Сервер не найден»). Проверка в **PowerShell** (не в WSL): `ipconfig /flushdns` и `Resolve-DnsName fanvue-games.prozorov.site -Server 1.1.1.1`; потом перезапуск браузера и окно инкогнито.
   - Записи `www.…` с жёлтым значком появились сами (откуда, не выяснено), пока не трогаем. Корневой `prozorov.site` и записи `_domainconnect`, `_dmarc` относятся к другому (заглушка GoDaddy и почта), не трогать.
6. **Cloudflare Access для админки** (шаг впереди, не сделано): Zero Trust → Access → Applications → приложение на `admin.fanvue-games.prozorov.site`, правило «пускать такие-то почты». Пока Access нет, админка за туннелем доступна всем до формы своего логина: имя админки не открывать.

## Caddy: проверка и перезагрузка

- После любого изменения файлов сайтов: проверка конфига **от пользователя `caddy`** (проверка от root может создать файлы логов с владельцем root, и работающий Caddy потом не сможет в них писать), затем перечитать конфиг:
  ```bash
  cd / && sudo -u caddy caddy validate --config /etc/caddy/Caddyfile
  sudo systemctl reload caddy
  ```
- **`reload` не перезапускает службу.** `systemctl status caddy` показывает время запуска процесса и не меняется при `reload`, поэтому по нему нельзя понять, перечитан ли конфиг. Если файлы правили после последнего `reload`, Caddy работает на старом конфиге (так было с заменой домена `sed`-ом 2026-10-25).
- **Признак того, что Caddy не знает имя сайта:** пустой ответ `200` с заголовком `Server: Caddy` (наши сайты убирают этот заголовок) и без `Strict-Transport-Security`. Нормальный ответ до первого деплоя: `503` и текст `not deployed`; после деплоя JSON с `"version"`.
- Проверка на сервере, не выходя наружу:
  ```bash
  curl -i -H 'Host: fanvue-games.prozorov.site' http://127.0.0.1:18080/health/ready
  curl -i -H 'Host: admin.fanvue-games.prozorov.site' http://127.0.0.1:18090/health/ready
  curl -i -H 'Host: чужое.example' http://127.0.0.1:18080/   # пусто: чужое имя ничего не отдаёт
  ```
- Когда Caddy перечитывал конфиг: `sudo journalctl -u caddy --since today | grep -i -E 'reload|load'`.
- `admin off` в Caddy ставить нельзя: `systemctl reload caddy` использует это API.

## Разовая настройка (порядок и состояние)

Проходится по шагам вручную, с проверкой после каждого. Список со статусом: `TODO.md`.

1. **Привратник, скрипт `fanvue-deploy` и юниты Quadlet** на сервере и ключ деплоя: пара ключей, открытая часть в `authorized_keys` пользователя `fanvue-games` с принудительной командой и `restrict`, закрытая в секретах GitHub (файл на компьютере удалить).
2. **Настройки на сервере** (секреты и параметры): `app.env`, `admin.env`, `deploy.conf`.
3. **Право переключать Caddy:** одно правило `sudoers`. Записывается через проверяемый временный файл, чтобы ошибка не сломала `sudo`:
   ```bash
   echo 'fanvue-games ALL=(root) NOPASSWD: /usr/bin/systemctl reload caddy' > /tmp/sudoers-fv && sudo visudo -cf /tmp/sudoers-fv
   sudo install -m 440 -o root -g root /tmp/sudoers-fv /etc/sudoers.d/fanvue-games-caddy && rm /tmp/sudoers-fv
   sudo -l -U fanvue-games   # проверка
   ```
4. **Вход сервера в GHCR** под пользователем `fanvue-games`. Токен на GitHub: классический (Settings → Developer settings, внизу левого меню; прямая ссылка `github.com/settings/tokens`), только право `read:packages`, поставить напоминание о сроке (после истечения деплой не скачает образы). Команда спросит пароль сама, вставить токен:
   ```bash
   sudo machinectl shell fanvue-games@ /usr/bin/podman login ghcr.io -u toxic-sunflower --authfile /home/fanvue-games/.config/containers/auth.json
   ```
   `--authfile` нужен, чтобы вход пережил перезагрузку (по умолчанию podman хранит его во временном каталоге).
5. **Каталоги Caddy и файлы сайтов:** `/etc/caddy/upstreams` (владелец `fanvue-games`), `/var/log/caddy` (владелец `caddy`), начальные upstream-файлы, файлы сайтов в `/etc/caddy/conf.d/` (главный `Caddyfile` должен содержать `import /etc/caddy/conf.d/*`), затем проверка и `reload`.
6. **GitHub:** окружения `production` и `staging`, секреты окружения `production`, ветки `develop` и `main`, переменная `DEPLOY_PRODUCTION_ENABLED=true`. Секрет `DEPLOY_KNOWN_HOSTS`: строку берём из `ssh-keyscan -t ed25519 <адрес>` **после сверки отпечатка** (`ssh-keyscan … | ssh-keygen -lf -` в WSL и `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` на сервере: оба `SHA256:…` должны совпасть).
7. **Cloudflare, туннель:** см. раздел выше.
8. **Первая выкатка** и проверка blue-green и отката. Первая попытка 2026-10-25 (см. ниже).

### Первая выкатка 2026-10-25

Цепочка заработала: привратник принял команду, скрипт скачал образы, поднял слот blue, сервис ответил `/health/ready`. Админка не стала готовой: в её образе был `Microsoft.EntityFrameworkCore.Relational` 10.0.4, а общий проект собран под 10.0.12 (версию 10.0.4 тянул провайдер Npgsql, а 10.0.12 приходила к общему проекту только через закрытый пакет `EntityFrameworkCore.Design`). Скрипт остановил слот, трафик остался на старом состоянии (заглушка `not deployed`). Исправление: версия пакета указана явно в общем проекте. **Урок для утилиты:** образ нужно проверять по `/health/ready` каждой части (так и сделано), а такой сбой версий тестами не ловится (тесты используют каталог сборки, а не публикацию); в `dotnet build` его выдаёт предупреждение `MSB3277`.

## Ограничения и что учесть

- **Загрузка файлов через Cloudflare** на бесплатном тарифе до 100 МБ на запрос (в приложении лимит 1,5 ГБ, в Caddy стоит 100 МБ). Для больших файлов нужно другое решение (отдельный адрес без прокси или загрузка напрямую во Fanvue).
- **Cloudflare и вебхуки Fanvue.** Если включена защита от ботов или WAF, нужно правило, которое пропускает `POST /api/webhooks/fanvue` без проверки.
- **Защита источника.** Сайты слушают только `127.0.0.1`, поэтому в обход туннеля до них не добраться, и заголовок `CF-Connecting-IP` подделать нельзя (его ставит Cloudflare). Если запасной файл включён, этой защиты нет.
- Пока приложение не получило свой `Fanvue__*` и `Auth__SessionSecret`, оно в боевом режиме не запустится: это сделано намеренно.
- Внутри туннеля порты общие на хосте (18080–18092 заняты нашим проектом); для другого проекта выбирать другие.

## Если Cloudflare недоступен (ручной запасной план)

Включать только на время сбоя, возвращать сразу после. Нужно около минуты. Ни разу не прогонялся (пункт в `TODO.md`):
1. В Cloudflare DNS заменить запись `fanvue-games` (CNAME на туннель) на запись A с адресом сервера, прокси выключен («только DNS», серое облако).
2. Скопировать `caddy/fanvue-games.fallback.caddy.disabled` на сервер и положить как `/etc/caddy/conf.d/fanvue-games-fallback.caddy`, затем `sudo systemctl reload caddy`. Caddy сам получит публичный сертификат (нужны открытые 80 и 443 и DNS на сервер).
3. Возврат: удалить `/etc/caddy/conf.d/fanvue-games-fallback.caddy`, `sudo systemctl reload caddy`, в DNS вернуть CNAME на туннель с включённым прокси.

Админка в этом режиме остаётся только через SSH-туннель (Cloudflare Access не работает). Лимит 100 МБ на загрузку на время сбоя не действует (в запасном файле 1500 МБ, как в приложении).
