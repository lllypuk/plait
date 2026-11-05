# Установка в закрытом контуре

Путь пилота на машине разработчика без интернета. Названия, адреса и учётные данные конкретной установки — только в локальном конфиге, не здесь.

## Сборка

Бинарь статический (`CGO_ENABLED=0`), поэтому собирать можно на любой машине с доступом к зеркалу Go-модулей и копировать готовый файл.

```sh
export GOPROXY=https://<зеркало-модулей>
export GOSUMDB=off            # если зеркало не проксирует sum.golang.org
export GOTOOLCHAIN=local      # иначе Go старше go.mod пойдёт скачивать toolchain
make build                    # CGO_ENABLED=0, версия из git describe
install -m 755 bin/plait ~/.local/bin/plait
```

Локальный Go должен быть не старше версии из `go.mod`. Без тегов в клоне версия — хеш коммита; она попадает во frontmatter skills, и `doctor` сверяет её с бинарём.

## Окружение

plait сам ходит в сеть только с `doctor --net`, через общий транспорт: прокси из `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY`, системный пул сертификатов плюс корпоративный CA.

Общие для машины настройки — в `~/.config/plait/config.yaml` или `/etc/plait/config.yaml`:

```yaml
network:
  ca_file: /etc/ssl/certs/corp-ca.pem      # PEM; относительный путь считается от корня репо. Пусто — SSL_CERT_FILE
  endpoints: [https://<forge>, https://<jira>]   # что ещё проверить в doctor --net
```

Агенты настраиваются своими средствами, plait только проверяет:

| | Claude Code | Qwen Code |
| --- | --- | --- |
| endpoint модели | `ANTHROPIC_BASE_URL` | `OPENAI_BASE_URL` |
| телеметрия выключена | `DISABLE_TELEMETRY=1` или `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` в env или в `env` из `~/.claude/settings.json` | `"privacy": {"usageStatisticsEnabled": false}` в `~/.qwen/settings.json`; `QWEN_USAGE_STATISTICS_ENABLED` в env важнее файла |

Обёртка или форк агента описывается в `~/.config/plait/config.yaml` наследованием, без правки кода:

```yaml
agents:
  my-agent:
    base: claude
    cmd: my-agent
```

## Установка

```sh
plait setup --dry-run      # план: create | update | skip | conflict
plait setup                # skills и запрет approve/reject для найденных агентов, затем проверки doctor
plait init                 # тимлид, один раз на репо: .plait/config.yaml, project-skills, AGENTS.md, .gitignore
plait doctor               # без сети; exit 4 при fail, у каждой проверки fix
plait doctor --net         # HTTPS к endpoint'ам моделей и network.endpoints
```

`setup` без `--agent` ставит только агентов из `PATH`; `--agent my-agent` — конкретного, в том числе производного. Файлы skills без метки plait не перезаписываются: это `conflict` и exit 4. `setup --uninstall` удаляет только помеченные файлы и свои правила.

Что значат ошибки `doctor --net`: неизвестный CA — задать `network.ca_file`; сертификат выдан другому хосту — проверить адрес в конфиге; недоступность через прокси — пропустить хост на прокси или добавить в `NO_PROXY`, без прокси — проверить адрес и `HTTPS_PROXY`.

После установки в агенте вызывается `/plait-review`; дальше агент идёт по `plait next` и `plait guide`.
