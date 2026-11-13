# lite-deploy

Лёгкая система настройки сервера и деплоя для одного сервера с несколькими проектами (замена Kubernetes: Quadlet, Caddy и PostgreSQL на хосте, blue-green, ограниченный SSH-ключ).

- `VISION.md`: замысел. `ARCHITECTURE.md`, `SPEC-v0.md`: как устроено. `DECISIONS.md`: журнал решений. `PLAN.md`: очередь сессий, начинать с него.
- `DEPLOY-PATTERN.md`: общая схема деплоя и чек-лист изоляции проектов. `COMPARISON-PLAN.md`, `WSL-TEST.md`: сравнение и проверка в WSL.
- `cmd/`, `internal/`, `schemas/`, `testdata/`: код на Go. Проверки: `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...`.

Инструкция по настройке сервера FanvueGames, скрипты установки ПО и журнал ручной настройки лежат в соседнем репозитории `../server-setup`. Эталонные файлы из него скопированы в `testdata/fanvue-games/legacy/` для сверки с генерацией.

## Установка

Нужен Linux (amd64 или arm64). Одна команда:

```sh
curl -fsSL https://lite-deploy.prozorov.me/install.sh | sh
```

Команда каждый раз берёт **самую свежую выпущенную версию**, номер указывать не нужно. Когда выйдет новая версия, повторный запуск этой же команды обновит `lite-deploy`.

Установщик скачивает бинарник из GitHub Releases, проверяет подпись и контрольную сумму и кладёт `lite-deploy` в `/usr/local/bin`. Для проверки подписи он при необходимости сам ставит `minisign` через `apt` (на Debian и Ubuntu). Больше на сервере он ничего не меняет.

Нужна не последняя, а конкретная версия (например, чтобы на всех серверах стояла одна и та же)? Укажите её после `--version`, номера есть на странице [Releases](https://github.com/toxic-sunflower/lite-deploy/releases):

```sh
curl -fsSL https://lite-deploy.prozorov.me/install.sh | sh -s -- --version vX.Y.Z
```

Проверить, что встало:

```sh
lite-deploy version
```

Скрипт короткий, перед запуском его можно прочитать: [site/install.sh](site/install.sh).

## Выкатка и интерфейс

После `project create` проект выкатывается по хэшу коммита (образ `<image>:<хэш>` должен лежать в реестре):

```sh
sudo lite-deploy deploy <проект> <хэш из 40 символов>
sudo lite-deploy status
sudo lite-deploy rollback <проект>
```

Веб-интерфейс с кнопками «Выкатить» и «Откатить» запускается на вашем компьютере и ходит на сервер по SSH:

```sh
lite-deploy ui --host admin@ваш-сервер
```

Команда печатает адрес вида `http://127.0.0.1:8787/?t=…`, его нужно открыть в браузере. Интерфейс слушает только `127.0.0.1`. На сервере у пользователя должен работать `sudo -n lite-deploy` (без пароля).

### Интерфейс постоянно запущенным сервисом

Чтобы интерфейс был доступен всегда, запустите его пользовательским сервисом systemd на своём компьютере (или на сервере, тогда без `--host`). Токен хранится в файле и не меняется при перезапусках, поэтому закладка в браузере продолжает работать.

```sh
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/lite-deploy-ui.service <<'EOF'
[Unit]
Description=lite-deploy UI

[Service]
ExecStart=/usr/local/bin/lite-deploy ui --host admin@ваш-сервер --token-file %h/.config/lite-deploy/ui-token
Restart=on-failure

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload
systemctl --user enable --now lite-deploy-ui
cat ~/.config/lite-deploy/ui-token
```

Адрес: `http://127.0.0.1:8787/?t=<содержимое файла ui-token>`. Если вы ставили с другим `--prefix`, подставьте в `ExecStart` путь из `command -v lite-deploy`. Чтобы сервис работал и без открытого сеанса, выполните `loginctl enable-linger $USER`. Для доступа по SSH нужен ключ без пароля или запущенный агент.

### Файервол

Раздел «Файервол» в интерфейсе (и команда `sudo lite-deploy firewall status|allow|remove|enable`) показывает правила `ufw`, открывает и закрывает порты. Порты SSH, 80 и 443 закрыть нельзя, любое изменение подтверждается. `ufw` должен быть установлен на сервере.

## Лицензия

MIT, см. [LICENSE](LICENSE).
