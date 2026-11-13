# Спецификация v0: lite-deploy

Сессия S2, 2026-11-03. Основа: `DECISIONS.md` (D-01…D-38), `ARCHITECTURE.md`, эталон `projects/fanvue-games/`, шаги 11–12 и 18 `WALKTHROUGH.md`. Новые решения этой спецификации записаны как D-39…D-46 со статусом «предложено в S2»: они вытекают из принятых решений, но их стоит увидеть глазами владельца до S3.

Объём v0 по D-28: режим А, команды `project create`, `deploy`, `rollback`, `status`; CLI с `--json` как базовый API; UI и MCP потом поверх тех же команд по SSH. Уточнения, найденные при реализации S3: D-48…D-51 в `DECISIONS.md`. Нет: демона, подтверждений подписью, Docker, оркестратора, плагинов сообщества, установки ПО на голый сервер (она в S11), туннеля Cloudflare (S7, до тех пор маршрут в туннеле создаётся вручную).

Критерий готовности v0: FanVue Games ставится и выкатывается через lite-deploy вместо ручных скриптов (раздел 12).

## 0. Соглашения

- Все файлы форматов: JSON, кодировка UTF-8, без комментариев. Неизвестные поля отклоняются везде (D-33).
- Схемы JSON Schema draft 2020-12 лежат в `schemas/`, генерируются из Go-типов и коммитятся; тест проверяет, что закоммиченные схемы совпадают с генерируемыми. Команда `lite-deploy schema <project|plan|result|error>` печатает схему.
- Сообщения для людей на русском. Идентификаторы, коды ошибок, имена полей и действий на английском.
- Время: RFC 3339, UTC.
- «Владелец»: человек с `sudo` на сервере. «Проектный пользователь»: системный пользователь проекта. «Исполнитель»: код, который меняет систему от root (в v0 это `sudo lite-deploy apply`, D-31).
- Один бинарник `lite-deploy` для всех ролей (CLI, исполнитель, привратник), Go без `cgo` и `unsafe` (D-23). Путь установки: `/usr/local/bin/lite-deploy`, владелец root, права 755. Проектный пользователь не может его менять.

## 1. Структура репозитория (реализована в S3)

```
lite-deploy/
  cmd/lite-deploy/main.go          # точка входа, разбор подкоманд
  internal/project/                # типы описания проекта, проверка, значения по умолчанию
  internal/plan/                   # построение плана, канонический JSON, хэш
  internal/actions/                # по файлу на действие исполнителя: params, Validate, Apply, DryRun
  internal/render/                 # шаблоны Quadlet, Caddy, sudoers, authorized_keys (встроены через embed)
  internal/secrets/                # шифрование и расшифровка значения (age), хранилище значений
  internal/host/                   # интерфейс Runner (запуск команд без оболочки), FS; подмены для тестов
  internal/deploy/                 # blue-green: deploy, rollback, status, привратник
  internal/cli/                    # команды, конверт JSON, коды выхода, удалённый режим по SSH
  internal/api/                    # (S6) типы и OpenAPI для /v1; в v0 только типы данных
  schemas/                         # project.schema.json, plan.schema.json, result.schema.json, error.schema.json
  testdata/fanvue-games/           # project.json и эталонные (golden) файлы сгенерированных юнитов и сайтов
  docs/
  SPEC-v0.md, DECISIONS.md, ...
```

Правила:
- Любая внешняя команда запускается только через `host.Runner` (массив аргументов, без оболочки, с таймаутом). Прямых `exec.Command` вне `internal/host` нет (проверяет линтер).
- Тесты в облачной среде идут на подменах `host.Runner` и временной файловой системе; применение к настоящей системе только на тестовом сервере (S4).
- `go test -race ./...`, `go vet`, `staticcheck`, проверка схем, проверка golden-файлов в CI.

## 2. Описание проекта (`project.json`)

Единственный вход для `project create`. Схема: `schemas/project.schema.json`, идентификатор `lite-deploy/project/v1`.

### 2.1. Поля

| Поле | Тип | Обязательно | Правила |
|---|---|---|---|
| `schema` | строка | да | ровно `"lite-deploy/project/v1"` |
| `name` | строка | да | `^[a-z][a-z0-9-]{1,30}$`; не из зарезервированных (`root`, `postgres`, `caddy`, `cloudflared`, `lite-deploy`, `www-data`, `daemon`, `systemd-*`, а также любое имя, совпадающее с существующим не управляемым нами пользователем, если не задано `adopt`, см. 4.2) |
| `description` | строка | нет | до 200 символов, одна строка |
| `adopt` | булево | нет, по умолчанию `false` | «взять под управление уже созданное вручную» (раздел 4.2) |
| `database` | объект | нет | если нет, база не создаётся |
| `registry` | объект | нет | вход в реестр образов |
| `services` | объект имя → сервис | да | 1…8 сервисов; имя `^[a-z][a-z0-9-]{0,15}$` |
| `sites` | массив | да | 1…8 сайтов |
| `deploy_key` | объект | нет | ключ CI для привратника |

`database`:
- `engine`: только `"postgres"` (в v0).
- `connection_format`: `"npgsql"` (`Host=/var/run/postgresql;Database=..;Username=..;Password=..`), `"url"` (`postgresql://роль:пароль@/база?host=/var/run/postgresql`) или `"libpq"` (`host=/var/run/postgresql dbname=.. user=.. password=..`). По умолчанию `"url"`.
- Имена роли и базы выводятся из `name` заменой `-` на `_` (для `fanvue-games` получается `fanvue_games`) и в схеме не задаются. Это убирает целый класс ошибок ввода.

`registry` (нужен, если образы приватные):
- `host`: `^[a-z0-9.-]+(:[0-9]{2,5})?$`, например `ghcr.io`.
- `username`: `^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`.
- `token_secret`: ссылка `secret://имя` (раздел 7). Токен только на чтение.

`services.<имя>`:
- `image`: имя образа без тега, `^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+$` (например `ghcr.io/toxic-sunflower/fanvue-games-api`). Тег при выкатке всегда полный хэш коммита (D-28, схема из `DEPLOY-PATTERN.md`).
- `container_port`: 1…65535.
- `host_ports`: необязательно, массив из двух чисел `[порт слота blue, порт слота green]` в диапазоне 18000–18999 (диапазон меняется в `config.json`). Если не задано, исполнитель выбирает ближайшую свободную пару и записывает её в состояние (раздел 9).
- `health`: объект `{ "path": "/health/ready", "version_field": "version", "timeout_seconds": 90 }`. Значения по умолчанию такие же. Контракт: GET `path` на порту слота возвращает 200 и JSON, в котором поле `version_field` равно полному хэшу выкатываемого коммита.
- `limits`: `{ "memory": "768m", "pids": 512 }`. `memory`: `^[1-9][0-9]*[mg]$`, максимум 16g; `pids`: 16…4096. Значения по умолчанию: `512m` и `256`.
- `env`: объект имя → строка (несекретные значения). Имя `^[A-Za-z_][A-Za-z0-9_]*$`, значение без перевода строки, до 1024 символов.
- `secrets`: объект имя переменной → ссылка `secret://имя`.
- `database_env`: имя переменной, куда кладётся строка подключения (нужно, если сервис использует базу). Например `ConnectionStrings__Default`.
- Сервис не может задать: пользователя, привилегии, возможности, тома, сеть, команду запуска. Эти настройки фиксирует шаблон Quadlet (раздел 4.3): не root, `DropCapability=ALL`, `NoNewPrivileges=true`, `ReadOnly=true`, `Tmpfs=/tmp`, порт только на `127.0.0.1`, том только каталог сокета PostgreSQL (если `database_env` задан). Нужен другой режим, это отдельная версия схемы, а не поле «опасный флаг».

`sites[]`:
- `name`: `^[a-z][a-z0-9-]{0,15}$`, уникально в проекте.
- `host`: DNS-имя строчными буквами.
- `service`: имя сервиса из `services`.
- `listen_port`: порт Caddy на `127.0.0.1` (в диапазоне 18000–18999; по умолчанию выбирается свободный). Сайты слушают только `127.0.0.1` (решение про туннель, `DEPLOY-PATTERN.md`).
- `publish`: `"tunnel"` (в v0 единственное значение: сайт ждёт туннель, маршрут создаётся вручную или в S6).
- `trusted_proxy`: `"cloudflare"` (тогда `X-Forwarded-For` берётся из `CF-Connecting-IP`) или `"none"` (по умолчанию).
- `max_body`: размер, `^[1-9][0-9]*(KB|MB)$`, по умолчанию `5MB`, максимум `1000MB`.
- `routes`: необязательный массив `{ "path": "/api/vault/upload", "max_body": "100MB" }` для особых лимитов тела запроса. `path` начинается с `/`, без пробелов и без `{}`.

`deploy_key`:
- `public_key`: строка открытого ключа `ssh-ed25519 AAAA… комментарий` (только ed25519, один ключ, без опций; опции добавляет шаблон).

### 2.2. Проверка

Выполняется одной функцией `project.Validate`, одинаково для CLI, UI и MCP. Ответ: список ошибок `{ "path": "/services/api/image", "code": "pattern", "message": "..." }`. Сверх схемы:
- каждая ссылка `service` в сайтах существует;
- `database_env` указан только если есть `database`;
- имена сайтов и порты не повторяются, `host_ports` и `listen_port` не пересекаются между сервисами и сайтами проекта;
- ссылки `secret://имя` в `secrets` и `registry.token_secret`: имя `^[a-z][a-z0-9_]{1,63}$`, не начинается с `_` (такие имена резервируются под служебные секреты, например `_db_password`);
- один и тот же хост в двух сайтах запрещён;
- размер файла до 64 КиБ.

### 2.3. Пример (эскиз для FanVue Games)

Имена секретов Fanvue (`Fanvue__*`) уточнить по `app.env` на сервере; здесь показан принцип.

```json
{
  "schema": "lite-deploy/project/v1",
  "name": "fanvue-games",
  "adopt": true,
  "database": { "engine": "postgres", "connection_format": "npgsql" },
  "registry": { "host": "ghcr.io", "username": "toxic-sunflower", "token_secret": "secret://ghcr_read_token" },
  "services": {
    "api": {
      "image": "ghcr.io/toxic-sunflower/fanvue-games-api",
      "container_port": 8080,
      "host_ports": [18081, 18082],
      "limits": { "memory": "768m", "pids": 512 },
      "env": { "ASPNETCORE_ENVIRONMENT": "Production", "Fanvue__UseMock": "false" },
      "secrets": {
        "Auth__SessionSecret": "secret://auth_session_secret",
        "Fanvue__WebhookSecret": "secret://fanvue_webhook_secret"
      },
      "database_env": "ConnectionStrings__Default"
    },
    "admin": {
      "image": "ghcr.io/toxic-sunflower/fanvue-games-admin",
      "container_port": 8080,
      "host_ports": [18091, 18092],
      "limits": { "memory": "384m", "pids": 256 },
      "env": { "ASPNETCORE_ENVIRONMENT": "Production" },
      "secrets": {
        "Admin__Username": "secret://admin_username",
        "Admin__Password": "secret://admin_password"
      },
      "database_env": "ConnectionStrings__Default"
    }
  },
  "sites": [
    {
      "name": "main", "host": "fanvue-games.prozorov.site", "service": "api",
      "listen_port": 18080, "publish": "tunnel", "trusted_proxy": "cloudflare",
      "max_body": "5MB", "routes": [ { "path": "/api/vault/upload", "max_body": "100MB" } ]
    },
    {
      "name": "admin", "host": "admin.fanvue-games.prozorov.site", "service": "admin",
      "listen_port": 18090, "publish": "tunnel", "trusted_proxy": "cloudflare", "max_body": "5MB"
    }
  ],
  "deploy_key": { "public_key": "ssh-ed25519 AAAA… github-actions" }
}
```

Все сервисы одного проекта выкатываются вместе: один хэш, одно состояние слотов, один переключатель (как сейчас сервис и админка FanVue Games). Раздельные выкатки сервисов не входят в v0.

## 3. План (`plan.json`)

Схема: `schemas/plan.schema.json`, идентификатор `lite-deploy/plan/v1` (D-34).

```json
{
  "schema": "lite-deploy/plan/v1",
  "id": "01JC3Y8Q4N6R2T7V9K0M5B1XZA",
  "created_at": "2026-11-03T12:00:00Z",
  "expires_at": "2026-11-04T12:00:00Z",
  "server_id": "sha256:…",
  "kind": "project.create",
  "project": "fanvue-games",
  "project_hash": "sha256:…",
  "steps": [
    {
      "id": "s03",
      "action": "provision_postgres",
      "class": "normal",
      "params": { "project": "fanvue-games", "connection_format": "npgsql" },
      "summary": "Роль и база fanvue_games в PostgreSQL, правило pg_hba, пароль создаётся на сервере"
    }
  ],
  "hash": "sha256:…"
}
```

Поля:
- `id`: ULID. `created_at`, `expires_at`: срок жизни по умолчанию 24 часа, максимум 7 суток. Просроченный план не применяется.
- `server_id`: `sha256` от `/etc/machine-id` с солью `lite-deploy`. План применим только на сервере, для которого построен.
- `kind`: в v0 `project.create` (создание и обновление проекта, одна команда: повторное применение того же описания меняет только то, что отличается).
- `project_hash`: хэш канонического `project.json` (из которого построен план).
- `steps`: до 200 шагов. `id` уникальны (`s01`, `s02`, …), шаги применяются по порядку. `class`: `safe`, `normal` или `dangerous` (D-29); в v0 у всех шагов `normal`, а `dangerous` зарезервирован для будущих действий удаления.
- `summary`: человекочитаемое описание для dry-run, UI и MCP. Исполнитель его **не использует** для решений; он смотрит только на `action` и `params`.
- `hash`: `sha256` от канонического представления всего плана без поля `hash`. Канонический вид по RFC 8785 (JCS): ключи по алфавиту, без пробелов. Хэш виден при применении (D-34); `apply` пересчитывает его и отказывает при несовпадении.

Построение: `lite-deploy project plan project.json` на сервере (от обычного пользователя владельца, права root не нужны: читает только несекретное состояние из `/var/lib/lite-deploy/state/`, раздел 9). Порядок шагов для `project.create`:

1. `verify_host`
2. `create_project_user`
3. `set_secret` для `_db_password` (только режим `adopt`, если владелец передал существующий пароль; D-48)
4. `provision_postgres` (если есть `database`)
5. `set_secret` (по одному на каждое значение, которое передаёт владелец: шифртекст внутри `params`, раздел 7)
6. `registry_login` (если есть `registry`)
7. `write_service_env` (по сервису)
8. `install_quadlet` (по сервису × слот blue и green)
9. `reload_user_units`
10. `install_caddy_site` (по сайту)
11. `install_reload_rule`
12. `reload_caddy`
13. `probe_site` (по сайту)
14. `install_deploy_key` (если есть `deploy_key`)
15. `record_project`

Шаги нумеруются подряд `s01`, `s02`, …; номера в списке выше это порядок групп, а не id.

Секреты в плане только как шифртекст; шифртексты передаются в `project plan` флагами `--ciphertext имя=файл`, лишний шифртекст это ошибка `unused_secret` (D-50). Если нужный секрет уже записан на сервере (его имя есть в поле `secrets` файла состояния проекта, D-49), шаг `set_secret` для него не добавляется. Если секрета нет ни в плане, ни на сервере, построение плана завершается ошибкой `secret_missing` с именем секрета.

### Применение: `sudo lite-deploy apply plan.json`

1. Проверка: версия схемы, хэш, срок, `server_id`, размер (до 256 КиБ), число шагов.
2. Проверка каждого шага: действие входит в список исполнителя (раздел 4), `params` проходят проверку действия.
3. Вывод: id плана и хэш на экран (и в журнал).
4. Шаги выполняются по порядку. Первая ошибка останавливает применение; уже выполненные шаги остаются (откат не делается, действия идемпотентны, повторное применение продолжает). Итог: список шагов со статусом `changed`, `unchanged`, `skipped` или `failed`.
5. `--dry-run`: тот же путь, но без изменений; каждый шаг сообщает, что бы он сделал (`would_change` / `unchanged` / `would_fail` с причиной). Это читаемый режим D-37. Требует root, потому что сравнивает с файлами проектного пользователя.
6. `apply` без `--dry-run` в интерактивном терминале сначала показывает dry-run и просит ввести `yes`; в неинтерактивном режиме требует `--yes`.
7. Каждый запуск и каждый шаг пишется в журнал (раздел 9) до выполнения и после.

## 4. Действия исполнителя в v0

### 4.1. Общие правила

- Исполнитель принимает **типизированные параметры, а не содержимое файлов**: тексты юнитов, сайтов Caddy, sudoers и `authorized_keys` он строит сам из шаблонов, встроенных в бинарник (D-42). Клиент не может заставить его записать произвольный файл.
- Все пути вычисляются из проверенных `project`, `service`, `slot`, `site`; параметры-пути не принимаются.
- Команды без оболочки. Проверка имён до любого действия; значения, попавшие в аргументы команд, проходят тот же шаблон.
- Идемпотентность: действие сравнивает желаемое с текущим и меняет только разницу; повторный запуск возвращает `unchanged`.
- Перед заменой существующего файла, чей владелец не мы (нет метки управления, раздел 4.2), делается копия `<файл>.bak-<id плана>`.
- Журнал: исполнитель записывает действие, `project`, результат и хэш плана. Значения секретов в журнал и в вывод не попадают никогда.

Имя проекта везде проверяется `^[a-z][a-z0-9-]{1,30}$`. Обозначения: `P` проект, `U` системный пользователь `P` (совпадает с именем проекта), `H=/home/U`, `ROLE` = имя проекта с `-` → `_`.

### 4.2. Режим `adopt`

Нужен для критерия v0: FanVue Games уже настроен вручную. Если в проекте `adopt: true`:
- существующий пользователь `U`, роль и база не пересоздаются и не считаются ошибкой;
- существующие файлы (юниты, сайты Caddy, env) сравниваются с эталоном; при различии старый файл сохраняется как `.bak-<id плана>`, новый записывается, различие попадает в вывод;
- пароль существующей роли исполнитель узнать не может. Варианты: владелец заранее задаёт служебный секрет `_db_password` (`lite-deploy secret set fanvue-games _db_password`, ввод скрытый, значение существующего пароля) или в параметрах шага стоит `rotate_password: true`: пароль меняется на новый, env-файлы переписываются. Без одного из двух шаг `provision_postgres` в режиме `adopt` завершается ошибкой `adopt_password_unknown`;
- метка управления: файл `/var/lib/lite-deploy/state/projects/<P>.json` (создаёт `record_project`). Без `adopt` проект с существующим пользователем, но без метки, отклоняется (`project_exists_unmanaged`).

### 4.3. Список действий

Таблица: имя, параметры (все обязательны, если не сказано иное), что делает, повторный запуск.

**`verify_host`**: `{}`. Только чтение. Проверяет: Ubuntu 24.04 или новее; установлены `podman` (4.4 или новее; ниже 5.0 предупреждение «проверено только на 5.x»), `caddy`, `postgresql` (если в плане есть база), `loginctl`, `visudo`; `ufw` активен (предупреждение, не ошибка); главный `Caddyfile` содержит `import /etc/caddy/conf.d/*`; существует `/etc/caddy/conf.d`; Caddy слушает API на `localhost:2019` (нужен для reload, `DEPLOY-PATTERN.md`). Ошибка: `precondition_failed` со списком.

**`create_project_user`**: `{project}`.
1. Пользователь `U` без пароля и без `sudo`: `adduser --disabled-password --gecos "" U` (нет, если есть).
2. `loginctl enable-linger U`.
3. Права `H`: 750.
4. Проверка диапазонов `/etc/subuid` и `/etc/subgid` для `U` (ошибка, если нет).
5. Создание от имени `U`: `~/.config/containers/systemd`, `~/.config/lite-deploy`, `~/.local/state/lite-deploy`.

**`provision_postgres`**: `{project, connection_format, rotate_password?: bool}`.
1. Если роли `ROLE` нет: пароль 24 байта из `crypto/rand`, base64 без `/+=`; `CREATE ROLE ROLE LOGIN PASSWORD …` (без `SUPERUSER`, `CREATEDB`, `CREATEROLE`), `CREATE DATABASE ROLE OWNER ROLE`, `REVOKE ALL ON DATABASE ROLE FROM PUBLIC`. Пароль передаётся в `psql` через переменную `-v pw=…` на стандартном вводе, не в аргументах.
2. Пароль сохраняется как служебный секрет `_db_password` (root 600, раздел 7).
3. Правило в `pg_hba.conf` строго для пары: `local ROLE ROLE scram-sha-256`, между маркерами `# lite-deploy:begin ROLE` и `# lite-deploy:end ROLE`, выше общего `local all all peer`; `systemctl reload postgresql`. Правила с `all` не создаются и при проверке `verify_host` предупреждение выдаётся, если в `pg_hba.conf` есть такие правила выше общего.
4. Проверка входа: подключение по сокету под `ROLE` с паролем.
Повтор: роль и база есть, правило есть: `unchanged`. Смена пароля только при `rotate_password`.

**`set_secret`**: `{project, name, ciphertext}`. `name` по шаблону `^_?[a-z][a-z0-9_]{1,63}$` (служебные `_…` разрешены только исполнителю, из плана `_db_password` задавать можно лишь в режиме `adopt`). Расшифровывает (раздел 7), проверяет привязку `project` и `name`, записывает значение в `/var/lib/lite-deploy/secrets/<project>/<name>` (владелец root, 600, каталог 700). Сообщает только `changed` или `unchanged` (сравнение хэшей значений).

**`registry_login`**: `{project, registry, username, secret}`. Выполняет `podman login --authfile ~/.config/lite-deploy/auth.json` от имени `U` с токеном на стандартном вводе. Файл `auth.json` 600. Повтор: переписывает только если токен изменился.

**`write_service_env`**: `{project, service, env: [{name, kind, value|secret}]}` где `kind`: `literal` (значение), `secret` (ссылка на имя), `database` (формат строки подключения). Собирает файл `~/.config/lite-deploy/env/<service>.env` (владелец `U`, 600) из строк `NAME=value` (без кавычек, это формат `EnvironmentFile` в Quadlet). Значения секретов берёт из хранилища; значение с переводом строки отклоняется. Повтор: сравнивает по хэшу итогового файла.

**`install_quadlet`**: `{project, service, slot, host_port, container_port, memory, pids, database_socket}`. `slot`: `blue` или `green`. Пишет `~/.config/containers/systemd/<project>-<service>-<slot>.container` (владелец `U`, 600) по шаблону, эквивалентному текущим юнитам FanVue Games:

```
[Unit]
Description=<project> <service> (слот <slot>)
After=network-online.target
Wants=network-online.target

[Container]
ContainerName=<project>-<service>-<slot>
Image=localhost/<project>-<service>:<slot>
PublishPort=127.0.0.1:<host_port>:<container_port>
EnvironmentFile=%h/.config/lite-deploy/env/<service>.env
Volume=/var/run/postgresql:/var/run/postgresql      # только при database_socket=true
ReadOnly=true
Tmpfs=/tmp
NoNewPrivileges=true
DropCapability=ALL
PidsLimit=<pids>
Memory=<memory>

[Service]
Restart=always
RestartSec=5
TimeoutStartSec=120

[Install]
WantedBy=default.target
```

Образ `localhost/<project>-<service>:<slot>` создаёт деплой командой `podman tag` (как сейчас); юнит версии не знает.

**`reload_user_units`**: `{project}`. Выполняет `systemctl --user daemon-reload` в сессии пользователя `U` (`systemd-run --machine=U@.host --user …` или `machinectl shell`; способ уточняется в S4, обычный `sudo -u` для rootless podman не годится, `DEPLOY-PATTERN.md`). Затем проверяет, что юниты видны (`systemctl --user list-unit-files`).

**`install_caddy_site`**: `{project, site, host, listen_port, upstream_initial, trusted_proxy, max_body, routes[]}`.
1. Файл сайта `/etc/caddy/conf.d/<project>-<site>.caddy` (root:root, 644) по шаблону: `http://<host>:<listen_port>`, `bind 127.0.0.1`, `encode zstd gzip`, заголовки (`Strict-Transport-Security "max-age=31536000"`, `X-Content-Type-Options nosniff`, `Referrer-Policy strict-origin-when-cross-origin`, `-Server`), `request_body max_size` по `max_body` и блоки `handle` для `routes`, `import /etc/caddy/upstreams/<project>-<site>.caddy`, журнал в `/var/log/caddy/<project>-<site>.log` (ротация 50 МиБ, 5 файлов). Если `trusted_proxy=cloudflare`, эти заголовки добавляет файл upstream (ниже), как в нынешнем `fanvue-deploy`.
2. Каталог `/etc/caddy/upstreams` (владелец root, 755) и файл `/etc/caddy/upstreams/<project>-<site>.caddy` (владелец `U`, 644). Начальное содержимое при первом создании: `respond "not deployed" 503`. Если файл уже есть (проект уже выкатывался), содержимое **не трогается**.
3. Каталог `/var/log/caddy` (владелец `caddy`).
4. Не перезагружает Caddy (это `reload_caddy`).

Имена файлов отличаются от нынешних (`fanvue-games.caddy` и `fanvue-games-admin.caddy`): при `adopt` старые файлы с тем же `host` заменяются, чтобы не получить два сайта на один адрес (шаг сообщает об этом).

**`install_reload_rule`**: `{project}`. Записывает `/etc/sudoers.d/lite-deploy-<project>` с единственной строкой `U ALL=(root) NOPASSWD: /usr/bin/systemctl reload caddy`. Файл сначала пишется во временный, проверяется `visudo -cf`, затем ставится с правами 440. Это остаётся до S6: в режиме Б переключение Caddy перейдёт на сокет исполнителя, а sudoers исчезнет.

**`reload_caddy`**: `{}`. Проверка конфигурации от пользователя `caddy` (`sudo -u caddy caddy validate --config /etc/caddy/Caddyfile`, не от root: иначе появятся файлы с владельцем root, `DEPLOY-PATTERN.md`), затем `systemctl reload caddy`. Ошибка проверки: файлы сайтов проекта, записанные в этом применении, возвращаются к прежнему состоянию из `.bak` (или удаляются, если были новыми), reload не выполняется.

**`probe_site`**: `{host, listen_port, expect}`. Запрос `GET http://127.0.0.1:<listen_port>/` с заголовком `Host: <host>`. `expect`: `"not_deployed"` (503 и тело `not deployed`) для нового проекта, `"deployed"` (любой код, кроме 503 `not deployed`, и без пустого `200` с `Server: Caddy` без заголовков сайта) для выкаченного. Ловит случай «Caddy не подхватил имя» (`DEPLOY-PATTERN.md`). Планировщик подбирает `expect` по состоянию из `state/`.

**`install_deploy_key`**: `{project, public_key}`. Добавляет в `~/.ssh/authorized_keys` пользователя `U` (владелец `U`, 600, каталог `.ssh` 700) одну строку, помеченную комментарием `lite-deploy:<project>`:

```
command="/usr/local/bin/lite-deploy gate --project <project>",restrict ssh-ed25519 AAAA… lite-deploy:<project>
```

Старая строка с той же меткой заменяется. Строки без метки не трогаются. Ключ обязан быть ed25519 в одну строку без опций.

**`record_project`**: `{project, project_hash, ports}`. Пишет `/var/lib/lite-deploy/state/projects/<project>.json` (644, не секретно): `hash`, `ports`, `secrets` (имена секретов из каталога секретов, значений нет), `deployed` (D-49; ставит выкатка, S5). Это метка управления (4.2) и источник «текущего состояния» для следующего `plan`. Сам `project.json` копируется в `/etc/lite-deploy/projects/<project>.json` (644).

### 4.4. Действия, не входящие в v0 (зарезервированы)

`delete_project`, `delete_database`, `delete_site`, `rotate_deploy_key`, `trust_key_add` и `trust_key_remove`, `install_plugin`, `update_agent`: класс `dangerous` (D-29). Исполнитель v0 их не знает и отклоняет (`unknown_action`).

## 5. Выкатка, откат, состояние (`deploy`, `rollback`, `status`)

В v0 это **не шаги плана**, а операции проектного пользователя, как в нынешнем `fanvue-deploy`. Они перенесены в Go (`internal/deploy`) и вызываются привратником. Описание ниже фиксирует поведение, которое уже проверено вручную и обязано сохраниться.

### 5.1. Слоты и порты

Два слота `blue` и `green`; у каждого сервиса по порту на слот (`host_ports`). Состояние проекта: `~/.local/state/lite-deploy/active` (слот `blue`, `green` или `none`), `history.log`, блокировка `lock`.

### 5.2. `deploy <sha>`

1. `<sha>` обязан совпасть с `^[0-9a-f]{40}$`.
2. Блокировка `flock` на `~/.local/state/lite-deploy/lock`; если занята, выход с кодом 6 (`deploy_in_progress`). Этого в старом скрипте не было, два одновременных деплоя там портили состояние.
3. `idle` = слот, противоположный активному (`blue`, если активного нет).
4. Для каждого сервиса: `podman pull <image>:<sha>` с `--authfile ~/.config/lite-deploy/auth.json`, затем `podman tag <image>:<sha> localhost/<project>-<service>:<idle>`.
5. `systemctl --user restart <project>-<service>-<idle>.service` для всех сервисов.
6. Ожидание готовности: для каждого сервиса опрос `GET http://127.0.0.1:<порт слота><health.path>` каждые 2 секунды до `health.timeout_seconds` (по умолчанию 90); готов, когда `version_field` в ответе равен `<sha>`.
7. Не готов: `podman logs --tail 40`, остановка слотов `idle`, запись `deploy-failed`, выход с кодом 10. Трафик остаётся на прежнем слоте.
8. Готов: переключение. Для каждого сайта пишется `/etc/caddy/upstreams/<project>-<site>.caddy` (временный файл и `mv`):

```
reverse_proxy 127.0.0.1:<порт слота сервиса> {
	header_up X-Forwarded-For {header.CF-Connecting-IP}   # только при trusted_proxy=cloudflare
	header_up X-Forwarded-Proto https
}
```

   Затем `sudo -n /usr/bin/systemctl reload caddy`. Если reload не удался, файлы возвращаются из копий, reload выполняется ещё раз, выход с кодом 1 (`caddy_reload_failed`).
9. `active` = `idle`; запись в `history.log`: `<время> deploy <sha> slot=<idle> previous=<прежний>`. Прежний слот остаётся запущенным для отката.

### 5.3. `rollback`

Отказ, если ещё ничего не выкатывали (`nothing_deployed`) или прежний слот не отвечает (`rollback_unavailable`, подсказка: выкатить нужную версию заново). Иначе переключение на прежний слот (шаг 8 выше), `active` меняется, запись `rollback`. Образы и данные базы не откатываются: миграции обязаны быть обратно совместимы (`DEPLOY-PATTERN.md`).

### 5.4. `status`

Активный слот, для каждого сервиса и слота: порт и версия из `health`, либо «не отвечает»; последние 5 строк `history.log`. JSON-вид в разделе 6.

### 5.5. Привратник: `lite-deploy gate --project <P>` (D-39)

Принудительная команда в `authorized_keys` (4.3, `install_deploy_key`). Читает `SSH_ORIGINAL_COMMAND` и разрешает ровно:
- `deploy <40 hex>`;
- `rollback`;
- `status` и `status --json`.

Всё остальное: код 2, сообщение «команда не разрешена». Привратник вызывает функции `internal/deploy` напрямую в том же процессе, без оболочки и без запуска скриптов. Раньше привратник и `fanvue-deploy` лежали в домашнем каталоге проектного пользователя (700, владелец `U`), то есть взломанный проект мог их переписать; бинарник в `/usr/local/bin` принадлежит root, проект его изменить не может. Квоты привратника: раздел 8.

## 6. CLI и формат ответов

### 6.1. Команды v0

| Команда | Кто | Класс | Что делает |
|---|---|---|---|
| `lite-deploy schema <project\|plan\|result\|error>` | любой | safe | печатает JSON Schema |
| `lite-deploy project validate <файл>` | любой | safe | проверяет описание, ничего не читает на сервере |
| `lite-deploy project plan <файл> [--out plan.json] [--ciphertext имя=файл]... [--rotate-password]` | владелец | safe | строит план, показывает читаемо (D-50) |
| `lite-deploy apply <plan.json или -> [--dry-run] [--yes]` | root | normal | применяет план |
| `lite-deploy project create <файл> [--yes]` | root | normal | `plan` + dry-run + `apply` одной командой (то же, что три команды выше) |
| `lite-deploy project list` | владелец | safe | проекты из `state/projects/` |
| `lite-deploy init` | root | normal | создаёт ключ исполнителя и каталоги; существующий ключ не трогает (D-52) |
| `lite-deploy secret pubkey` | любой | safe | открытый ключ исполнителя (`age1…`) |
| `lite-deploy secret encrypt <проект> <имя>` | любой | safe | читает значение со стандартного ввода или скрыто с терминала, печатает шифртекст (можно на машине владельца, без доступа к серверу, с `--pubkey`) |
| `lite-deploy secret set <проект> <имя>` | root | normal | то же, что encrypt + запись на месте (для `_db_password` и разовых случаев) |
| `lite-deploy secret list <проект>` | владелец | safe | имена и время записи; значений нет |
| `lite-deploy deploy <проект> <sha>` | проектный пользователь, привратник, владелец | normal | раздел 5.2 |
| `lite-deploy rollback <проект>` | то же | normal | раздел 5.3 |
| `lite-deploy status [проект]` | то же | safe | раздел 5.4 |
| `lite-deploy gate --project <P>` | SSH (ключ CI) | — | раздел 5.5 |
| `lite-deploy ui [--host <пользователь@сервер>] [--listen 127.0.0.1:8787]` | владелец | normal | локальный веб-интерфейс: состояние, выкатка, откат (поверх CLI, слушает только loopback, токен в адресе) |
| `lite-deploy version` | любой | safe | версия, версии схем |

Общие флаги: `--json` (машинный вывод), `--host <пользователь@адрес>` (клиентский режим: команда выполняется на сервере через `ssh <host> lite-deploy <аргументы> --json`, вход и вывод пробрасываются; так работают UI и MCP, D-28), `--quiet`. Права берутся от пользователя ОС: для `apply` при `--host` используется `ssh -t <host> sudo lite-deploy apply …`.

Команда `secret set` и значения секретов: никогда не принимаются в аргументах и не читаются из переменных окружения; только стандартный ввод или скрытый ввод с терминала.

### 6.2. Конверт ответа (`--json`)

Каждый ответ: один JSON-объект в одной строке (или несколько при `--stream`, не в v0).

Успех:
```json
{ "ok": true, "api_version": "v1", "command": "project.plan", "data": { }, "warnings": [ { "code": "ufw_inactive", "message": "ufw выключен" } ] }
```

Ошибка:
```json
{ "ok": false, "api_version": "v1", "command": "project.plan",
  "error": { "code": "invalid_project", "message": "описание проекта не прошло проверку",
             "details": [ { "path": "/services/api/image", "code": "pattern", "message": "…" } ],
             "hint": "исправьте поля из details" } }
```

`data` по командам:
- `project.validate`: `{ "valid": true }`.
- `project.plan` и `apply --dry-run`: `{ "plan": { … }, "steps": [ { "id": "s03", "state": "would_change", "detail": "…" } ] }` (при `plan` без `state`).
- `apply`: `{ "plan_id": "…", "plan_hash": "sha256:…", "steps": [ { "id": "s03", "state": "changed|unchanged|skipped|failed", "detail": "…", "error": null } ] }`.
- `deploy`: `{ "project": "…", "version": "<sha>", "slot": "green", "previous_slot": "blue", "switched": true }`.
- `rollback`: `{ "project": "…", "version": "<sha>", "slot": "blue", "previous_slot": "green" }`.
- `status`: `{ "project": "…", "active": "blue", "slots": { "blue": { "api": { "port": 18081, "version": "…" | null } } }, "history": [ { "time": "…", "event": "deploy", "version": "…", "slot": "…" } ] }`.
- `secret list`: `{ "secrets": [ { "name": "auth_session_secret", "updated_at": "…" } ] }`.

В текстовом режиме (без `--json`) тот же результат печатается для людей по-русски, с отступами, без цвета при выводе не в терминал.

### 6.3. Коды выхода и ошибок

| Код выхода | Смысл | Коды ошибок (`error.code`) |
|---|---|---|
| 0 | успех | |
| 1 | общая ошибка | `internal`, `caddy_reload_failed`, `io_error` |
| 2 | неверный ввод | `invalid_project`, `invalid_plan`, `unknown_action`, `usage`, `command_not_allowed`, `invalid_sha`, `invalid_secret`, `unused_secret` |
| 3 | не выполнено предусловие | `precondition_failed`, `secret_missing`, `project_exists_unmanaged`, `adopt_password_unknown` |
| 4 | нет прав | `permission_denied`, `confirmation_required` (зарезервирован для режима Б) |
| 5 | шаг плана завершился ошибкой (применено частично) | `step_failed` |
| 6 | конфликт | `plan_hash_mismatch`, `plan_expired`, `plan_wrong_server`, `deploy_in_progress` |
| 7 | не найдено | `project_not_found`, `secret_not_found` |
| 10 | выкатка не прошла, трафик не менялся | `deploy_unhealthy`, `deploy_failed` (не скачался образ или не запустился юнит), `rollback_unavailable`, `nothing_deployed` |

## 7. Секреты (D-10, D-38)

Принцип: значение секрета в открытом виде существует только у владельца в момент ввода и у исполнителя в момент расшифровки. Агент, CLI на промежуточной стороне, UI, MCP, план и журнал видят только шифртекст.

- **Ключ исполнителя.** Пара X25519, формат `age` (библиотека `filippo.io/age`: X25519, ChaCha20-Poly1305). Создаётся при установке (S11; в v0 командой `sudo lite-deploy init`, одноразово): закрытый ключ `/etc/lite-deploy/exec.age` (root, 600), открытый `/etc/lite-deploy/exec.pub` (644, читается любым). Свой формат шифрования не изобретаем.
- **Шифрование на клиенте.** `lite-deploy secret encrypt <проект> <имя>` оборачивает значение в JSON `{ "v": 1, "project": "…", "name": "…", "value": "…", "created_at": "…" }`, шифрует открытым ключом исполнителя и печатает armor-строку. Привязка к проекту и имени нужна, чтобы шифртекст нельзя было подсунуть под другим именем (в age нет связанных данных).
- **Передача.** Шифртекст идёт в `params.ciphertext` шага `set_secret` внутри плана (до 16 КиБ; значение до 8 КиБ, без `\0`, `\n`, `\r` для значений, идущих в env). Хэш плана включает шифртекст.
- **Хранение.** `/var/lib/lite-deploy/secrets/<project>/<name>`: root, 600, каталоги 700. Проектные пользователи читают только готовые env-файлы своих сервисов (`~/.config/lite-deploy/env/<service>.env`, 600), но не хранилище.
- **Запись без чтения.** В API и CLI нет команды «показать значение» (D-10). `secret list` показывает имя и время. Прочитать значение можно только на сервере под root прямо из файла; действие считается `dangerous` (D-29), в журнал оно не попадает, пока нет демона, поэтому в v0 это просто доступ root.
- **Строка подключения к БД** собирается исполнителем из служебного секрета `_db_password` и не выводится.
- **Токен реестра** (`registry.token_secret`) хранится так же и попадает только в `auth.json` проектного пользователя и на стандартный ввод `podman login`.
- **Ссылки в описании:** `secret://имя`. Значения в `project.json` запрещены: схема принимает в полях секретов только строку этого вида.

## 8. Квоты по умолчанию (D-38)

Файл `/etc/lite-deploy/config.json`; значения ниже действуют, если поле не задано.

| Параметр | По умолчанию | Где действует |
|---|---|---|
| `limits.plan_bytes` | 262144 | `apply` |
| `limits.plan_steps` | 200 | `apply` |
| `limits.project_bytes` | 65536 | `validate`, `plan` |
| `limits.secret_bytes` | 8192 | `set_secret` |
| `limits.projects` | 50 | `create_project_user` |
| `limits.services_per_project` | 8 | схема |
| `limits.port_range` | `[18000, 18999]` | выбор портов |
| `limits.plan_ttl_hours` | 24 (максимум 168) | `plan`, `apply` |
| `quotas.deploy_per_10min_per_project` | 10 | `deploy`, `rollback` (привратник и CLI; счёт в `~/.local/state/lite-deploy/quota.log`) |
| `quotas.apply_per_hour` | 60 | `apply` (счёт в журнале исполнителя) |
| `quotas.secret_sets_per_hour` | 120 | `set_secret` |

Превышение: код выхода 6, `error.code` = `quota_exceeded`, в `details` лимит и время, когда можно повторить. В v0 без демона квоты защищают от ошибок и зацикленного CI; настоящий смысл они получат в режиме Б (S6), где счёт ведёт исполнитель по своему журналу, который агент изменить не может.

## 9. Состояние и журнал на сервере

```
/usr/local/bin/lite-deploy                        бинарник (root, 755)
/etc/lite-deploy/config.json                      настройки и квоты (root, 644)
/etc/lite-deploy/exec.age, exec.pub               ключ исполнителя (600 / 644)
/etc/lite-deploy/projects/<project>.json          применённое описание (644)
/var/lib/lite-deploy/state/projects/<project>.json   метка управления, порты, хэши (644)
/var/lib/lite-deploy/secrets/<project>/<name>     значения секретов (root, 600, каталоги 700)
/var/lib/lite-deploy/journal.jsonl                журнал исполнителя (root, 600; атрибут chattr +a)
/etc/caddy/conf.d/<project>-<site>.caddy          сайт (root, 644)
/etc/caddy/upstreams/<project>-<site>.caddy       куда идёт трафик (владелец проектный пользователь)
/home/<U>/.config/containers/systemd/*.container  юниты (600)
/home/<U>/.config/lite-deploy/env/<service>.env   настройки сервиса (600)
/home/<U>/.config/lite-deploy/auth.json           вход в реестр (600)
/home/<U>/.local/state/lite-deploy/               active, history.log, lock, quota.log
```

Запись журнала (одна строка JSON): `{ "ts", "run_id", "plan_id", "plan_hash", "step_id", "action", "project", "state", "error", "uid" }`. Значений секретов и шифртекстов в журнале нет.

Каталог `/var/lib/lite-deploy/state` читается всеми: там нет секретов, и из него `plan` определяет, что уже есть, а порты не пересекаются.

## 10. API агента `/v1` (форма для S6, в v0 только типы)

Режим Б появится в S6, но формы запросов фиксируются сейчас, чтобы CLI с `--json` и API давали одинаковые данные (D-32, D-37). HTTP с JSON по `/run/lite-deploy/api.sock`, версия в пути, тело ответа это `data` или `error` из конверта выше (без обёртки `command`).

| Метод и путь | Эквивалент CLI | Класс |
|---|---|---|
| `GET /v1/status` | `status` для всех проектов | safe |
| `GET /v1/projects`, `GET /v1/projects/{name}` | `project list`, описание проекта | safe |
| `POST /v1/projects/{name}/plans` (тело: проект) | `project plan` | safe |
| `POST /v1/plans/{id}/apply` | `apply` | normal; dangerous: нужен `confirmation_id` |
| `POST /v1/projects/{name}/deployments` (тело: `{ "sha": "…" }`) | `deploy` | normal |
| `POST /v1/projects/{name}/rollback` | `rollback` | normal |
| `GET /v1/projects/{name}/status` | `status <проект>` | safe |
| `GET /v1/projects/{name}/secrets` | `secret list` | safe |
| `PUT /v1/projects/{name}/secrets/{secret}` (тело: `{ "ciphertext": "…" }`) | `secret set` | normal |
| `GET /v1/secrets/pubkey` | `secret pubkey` | safe |
| `GET /v1/confirmations/{id}`, `POST /v1/confirmations/{id}/signature` | нет в v0 | — |
| `GET /v1/schemas/{name}` | `schema` | safe |

Вызывающий определяется по `SO_PEERCRED` (uid) и сопоставляется с ролью (владелец, проектный пользователь, ИИ-клиент) в `config.json`. План всегда сначала `plans` (dry-run), потом `apply` (D-37). Описание OpenAPI генерируется из тех же Go-типов, что и JSON Schema.

## 11. Тесты, которые должен давать S3

- Проверка описания: примеры верных и неверных `project.json`, граничные значения шаблонов, неизвестные поля, запрещённые имена.
- Канонический JSON и хэш плана: стабильность при перестановке полей, изменение хэша при любой правке.
- Построение плана из `testdata/fanvue-games/project.json`: порядок шагов, параметры, отсутствие значений секретов.
- Golden-файлы: сгенерированные юниты Quadlet и файлы Caddy совпадают по смыслу с `projects/fanvue-games/quadlet/*` и `projects/fanvue-games/caddy/*` (комментарии и названия файлов могут отличаться, строки настроек совпадают).
- Привратник: все допустимые команды, неверные хэши, чужие команды, пустая команда, команды с `;`, `$()`, пробелами и переводами строк (аналог прежних 14 проверок).
- Шифрование: цикл «зашифровать, расшифровать», отказ при другом `project` или `name`, отказ при повреждённом шифртексте.
- Схемы: закоммитованные `schemas/*.json` равны генерируемым.
- Конверт и коды выхода для каждой ошибки раздела 6.3.

S4 добавляет проверки на тестовом сервере: `apply` создаёт проект с нуля, повторный `apply` даёт `unchanged` везде, `adopt` на копии боевой настройки FanVue Games ничего не ломает.

## 12. Соответствие ручным шагам FanVue Games (критерий v0)

| Ручной шаг | Где в v0 |
|---|---|
| 11. Пользователь, linger, subuid | `create_project_user` |
| 12. Роль, база, `pg_hba.conf`, `app.env` со строкой подключения | `provision_postgres`, `write_service_env` |
| 13. Ключ деплоя и привратник | `install_deploy_key`, `lite-deploy gate` |
| 14. Привратник, `fanvue-deploy`, юниты Quadlet, `daemon-reload` | бинарник `lite-deploy`, `install_quadlet`, `reload_user_units` |
| 15. Правило sudoers | `install_reload_rule` |
| 16. `deploy.conf`, `admin.env`, `app.env` | описание проекта, `set_secret`, `write_service_env` |
| 17. Вход в GHCR | `registry_login` |
| 18. Каталоги и файлы Caddy, начальные upstream, проверка, `reload` | `install_caddy_site`, `reload_caddy`, `probe_site` |
| 19. Окружения, секреты и переменные GitHub | вне v0 (остаётся ручным или `gh api`) |
| 20. Туннель `cloudflared`, маршруты, DNS | вне v0 (S6) |
| 21. Выкатка, откат, состояние | `deploy`, `rollback`, `status` |
| Права и изоляция из чек-листа `DEPLOY-PATTERN.md` | шаблоны действий (раздел 4.3); проверки доступа к `localhost:2019`, чужим базам и `/etc/environment` остаются в `verify` (S11) |

Что остаётся вне v0 и записывается в `TODO.md` проекта: отключение входа по паролю, Cloudflare Access, запасной план Cloudflare, правило WAF для вебхука, очистка образов в GHCR, миграции отдельной ролью.

## 13. Что не решено и уходит дальше

1. Способ запускать команды в сессии проектного пользователя выбран: `systemd-run --machine=U@.host --user` (D-53); проверить на тестовом сервере.
2. Формат манифеста шаблона приложения: после v0.
3. Подпись подтверждений и список доверенных ключей (`trust_key_*`): S6.
4. Переход переключения Caddy с sudoers на сокет исполнителя: S6.
5. Установка ПО на голый сервер и `lite-deploy init`: S11 (в v0 `init` делает только ключ и каталоги).
6. Порт по умолчанию и поведение для сервисов без HTTP (воркеры без порта и без health): не входит в v0, схема требует `container_port`.
7. Оркестратор и SaaS: после v1.

## 14. Решения

D-39…D-46 приняты (S2). Уточнения при реализации S3, D-48…D-51, предложены: порядок `_db_password` при `adopt`, поля состояния проекта, флаг `--ciphertext`, порядок проверки плана. Реализация S3 расходится с текстом выше только там, где это сказано в этих решениях (D-48…D-52) и в сноске ниже.

Состояние реализации на конец S4 (код и подмены; настоящий сервер не проверен): `apply`, `apply --dry-run` и `project create` реализованы (D-53…D-56); `deploy`, `rollback`, `status`, `gate` возвращают `not_implemented` (S4, S5); `init`, `secret pubkey|encrypt|set|list` и чтение `config.json` реализованы; ошибки разбора корня документа имеют `path` равный `/`.
