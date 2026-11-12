# lite-deploy

Лёгкая система настройки сервера и деплоя для одного сервера с несколькими проектами (замена Kubernetes: Quadlet, Caddy и PostgreSQL на хосте, blue-green, ограниченный SSH-ключ).

- `VISION.md`: замысел. `ARCHITECTURE.md`, `SPEC-v0.md`: как устроено. `DECISIONS.md`: журнал решений. `PLAN.md`: очередь сессий, начинать с него.
- `DEPLOY-PATTERN.md`: общая схема деплоя и чек-лист изоляции проектов. `COMPARISON-PLAN.md`, `WSL-TEST.md`: сравнение и проверка в WSL.
- `cmd/`, `internal/`, `schemas/`, `testdata/`: код на Go. Проверки: `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...`.

Инструкция по настройке сервера FanvueGames, скрипты установки ПО и журнал ручной настройки лежат в соседнем репозитории `../server-setup`. Эталонные файлы из него скопированы в `testdata/fanvue-games/legacy/` для сверки с генерацией.
