# Проверка S4 в WSL (тестовый сервер)

Цель: убедиться, что `lite-deploy project create` создаёт проект с нуля, повторный `apply` даёт `unchanged`, и найти расхождения между подменами в тестах и настоящей системой. Проект `demo-app` (`testdata/wsl-test/project.json`) без реестра и ключа CI: образа у него нет, юниты только записываются и перечитываются, не запускаются.

**Только тестовая машина.** Боевой сервер (`187.7.72.122`) не трогать.

## 0. Что должно быть в WSL

| Что | Проверка | Если нет |
|---|---|---|
| systemd включён | `cat /etc/wsl.conf` содержит `[boot]` и `systemd=true`; `systemctl is-system-running` отвечает `running` или `degraded` | добавить в `/etc/wsl.conf`, затем в PowerShell `wsl --shutdown` |
| Ubuntu 24.04+ | `lsb_release -rs` | |
| podman 5.x | `podman --version` | в Ubuntu 24.04 из репозитория 4.9: `verify_host` упадёт (см. вопрос в сессии) |
| Caddy с API на `localhost:2019` | `curl -s localhost:2019/config/ \| head -c 50` | `sudo bash install.sh --only caddy` |
| `Caddyfile` с импортом | `grep conf.d /etc/caddy/Caddyfile` показывает `import /etc/caddy/conf.d/*.caddy` | то же |
| PostgreSQL | `sudo -u postgres psql -c 'select 1'` | `sudo bash install.sh --only postgres` |
| ufw (необязательно) | `sudo ufw status` | только предупреждение |
| Go 1.24 | `go version` | для сборки |

## 1. Сборка и установка

```bash
git fetch origin claude/determined-bardeen-w8wa3y && git checkout claude/determined-bardeen-w8wa3y
go build -o lite-deploy ./cmd/lite-deploy
sudo install -m 755 lite-deploy /usr/local/bin/lite-deploy
sudo lite-deploy init
```

## 2. Секрет и план

Значение секрета читается со стандартного ввода (в аргументы и историю не попадает):

```bash
printf 'любое-тестовое-значение' | sudo lite-deploy secret encrypt demo-app app_secret > /tmp/ct_app_secret
sudo lite-deploy project plan testdata/wsl-test/project.json --ciphertext app_secret=/tmp/ct_app_secret --out /tmp/plan.json
```

Ожидается 13 шагов (`s01 verify_host` … `s13 record_project`). Изменений на сервере нет.

## 3. Dry-run

```bash
sudo lite-deploy apply /tmp/plan.json --dry-run --project-file testdata/wsl-test/project.json
```

Ожидается: почти все шаги `would_change`, ни одного `would_fail`. Если `verify_host` пишет `would_fail`, в тексте ошибки перечислено, чего не хватает (пункт 0). Проверьте, что dry-run ничего не создал: `id demo-app` должен сказать «нет такого пользователя».

## 4. Применение

```bash
sudo lite-deploy apply /tmp/plan.json --project-file testdata/wsl-test/project.json
```

Введите `yes`. Ожидается: все шаги `changed`. При ошибке `apply` остановится на первом упавшем шаге (остальные `skipped`); исправьте причину и запустите ту же команду снова (план живёт 24 часа).

Проверки результата:

```bash
id demo-app && ls -la /home/demo-app/.config/lite-deploy/env /home/demo-app/.config/containers/systemd
sudo ls -la /var/lib/lite-deploy/secrets/demo-app /etc/caddy/conf.d /etc/caddy/upstreams
cat /var/lib/lite-deploy/state/projects/demo-app.json
sudo cat /etc/sudoers.d/lite-deploy-demo-app
curl -si -H 'Host: demo.example.com' http://127.0.0.1:18002/    # 503 not deployed
sudo tail -5 /var/lib/lite-deploy/journal.jsonl
sudo grep -B1 -A3 'lite-deploy:begin' /etc/postgresql/*/main/pg_hba.conf
```

Номера портов смотрите в плане (`s09`), 18002 это пример.

## 5. Повтор (главная проверка)

```bash
sudo lite-deploy apply /tmp/plan.json --yes --project-file testdata/wsl-test/project.json
sudo lite-deploy apply /tmp/plan.json --dry-run
```

Ожидается: везде `unchanged`. Любой `changed` или `would_change` это ошибка идемпотентности: пришлите вывод.

## 6. Что прислать

Выводы шагов 3, 4 и 5 целиком (секретов в них нет; значения не печатаются) и результат команд проверки из шага 4. Особенно интересны: `systemd-run --machine` (шаг `reload_user_units`), `list-unit-files` для Quadlet, `runuser -u postgres psql`, `visudo -cf`.

## 7. Очистка

```bash
sudo rm -f /etc/caddy/conf.d/demo-app-*.caddy /etc/caddy/upstreams/demo-app-*.caddy /etc/sudoers.d/lite-deploy-demo-app
sudo systemctl reload caddy
sudo -u postgres psql -c 'DROP DATABASE demo_app' -c 'DROP ROLE demo_app'
sudo sed -i '/lite-deploy:begin demo_app/,/lite-deploy:end demo_app/d' /etc/postgresql/*/main/pg_hba.conf && sudo systemctl reload postgresql
sudo loginctl disable-linger demo-app && sudo deluser --remove-home demo-app
sudo rm -rf /var/lib/lite-deploy/secrets/demo-app /var/lib/lite-deploy/state/projects/demo-app.json /etc/lite-deploy/projects/demo-app.json
```
