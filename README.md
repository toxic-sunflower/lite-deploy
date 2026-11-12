# lite-deploy

Лёгкая система настройки сервера и деплоя для одного сервера с несколькими проектами (замена Kubernetes: Quadlet, Caddy и PostgreSQL на хосте, blue-green, ограниченный SSH-ключ).

- `VISION.md`: замысел. `ARCHITECTURE.md`, `SPEC-v0.md`: как устроено. `DECISIONS.md`: журнал решений. `PLAN.md`: очередь сессий, начинать с него.
- `DEPLOY-PATTERN.md`: общая схема деплоя и чек-лист изоляции проектов. `COMPARISON-PLAN.md`, `WSL-TEST.md`: сравнение и проверка в WSL.
- `cmd/`, `internal/`, `schemas/`, `testdata/`: код на Go. Проверки: `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...`.

Инструкция по настройке сервера FanvueGames, скрипты установки ПО и журнал ручной настройки лежат в соседнем репозитории `../server-setup`. Эталонные файлы из него скопированы в `testdata/fanvue-games/legacy/` для сверки с генерацией.

## Установка

```sh
sudo apt install minisign
curl -fsSL https://toxic-sunflower.github.io/lite-deploy/install.sh | sh
```

Скрипт (`site/install.sh`) скачивает бинарник из GitHub Releases, проверяет подпись `SHA256SUMS` ключом minisign и SHA256, ставит только `/usr/local/bin/lite-deploy`. Версия: `--version v0.1.0`; без проверки подписи (небезопасно): `--skip-sig`.

### Выпуск релиза (владельцу)

1. Ключ создан (без пароля): закрытый `~/.config/lite-deploy/minisign.key` (не коммитить, копия в секрете репозитория `MINISIGN_KEY`), открытый `minisign.pub` в репозитории и в `MINISIGN_PUBKEY` в `site/install.sh`. Сменить: `minisign -G -W -p minisign.pub -s minisign.key -f`, затем обновить секрет и ключ в скрипте.
3. В настройках репозитория включить Pages (источник: GitHub Actions). Репозиторий должен быть публичным, иначе скачивание без входа не работает.
4. `git tag v0.1.0 && git push origin v0.1.0`.
