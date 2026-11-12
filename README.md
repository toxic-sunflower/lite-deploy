# lite-deploy

Лёгкая система настройки сервера и деплоя для одного сервера с несколькими проектами (замена Kubernetes: Quadlet, Caddy и PostgreSQL на хосте, blue-green, ограниченный SSH-ключ).

- `VISION.md`: замысел. `ARCHITECTURE.md`, `SPEC-v0.md`: как устроено. `DECISIONS.md`: журнал решений. `PLAN.md`: очередь сессий, начинать с него.
- `DEPLOY-PATTERN.md`: общая схема деплоя и чек-лист изоляции проектов. `COMPARISON-PLAN.md`, `WSL-TEST.md`: сравнение и проверка в WSL.
- `cmd/`, `internal/`, `schemas/`, `testdata/`: код на Go. Проверки: `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...`.

Инструкция по настройке сервера FanvueGames, скрипты установки ПО и журнал ручной настройки лежат в соседнем репозитории `../server-setup`. Эталонные файлы из него скопированы в `testdata/fanvue-games/legacy/` для сверки с генерацией.

## Установка

Нужен Linux (amd64 или arm64) и `minisign` для проверки подписи:

```sh
sudo apt install minisign
curl -fsSL https://toxic-sunflower.github.io/lite-deploy/install.sh | sh
```

Команда каждый раз берёт **самую свежую выпущенную версию**, номер указывать не нужно. Когда выйдет новая версия, повторный запуск этой же команды обновит `lite-deploy`.

Установщик скачивает бинарник из GitHub Releases, проверяет подпись и контрольную сумму и кладёт `lite-deploy` в `/usr/local/bin`. Больше на сервере он ничего не меняет.

Нужна не последняя, а конкретная версия (например, чтобы на всех серверах стояла одна и та же)? Укажите её после `--version`, номера есть на странице [Releases](https://github.com/toxic-sunflower/lite-deploy/releases):

```sh
curl -fsSL https://toxic-sunflower.github.io/lite-deploy/install.sh | sh -s -- --version vX.Y.Z
```

Проверить, что встало:

```sh
lite-deploy version
```

Скрипт короткий, перед запуском его можно прочитать: [site/install.sh](site/install.sh).
