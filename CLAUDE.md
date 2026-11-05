# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

plait — Go CLI, которой пользуются coding-агенты (Claude Code, Codex, Gemini CLI, Qwen Code, pi): ведёт агента по dev-flow команды и не даёт перепрыгнуть стадии или уровень автономии репозитория.

## Состояние

Готов сквозной срез v0 (`docs/plans/20261105-01-v0-slice.md`); остаток v0 — план 02+. Источник истины — `docs/plait-brief.md`: архитектура, интерфейсы (`Agent`, `Installer`, `Stage`, `Forge`), схема конфига, roadmap. Начинаем с шага 1 roadmap — v0, её объём и обещание в разделе «Первая версия». Отступая от брифа, правь бриф в том же изменении.

`private/` в `.gitignore` и есть только локально: там контекст конкретного внедрения. Названия, детали и примеры оттуда в отслеживаемые файлы, коммиты и issues не переносить: репозиторий публичный.

## Команды

CI вызывает те же make-цели:

```sh
make build                     # bin/plait, версия из git describe
make test                      # go test -race ./...
go test ./internal/run -run TestName
go test ./cmd/plait -run 'TestScript/e2e_stale'   # один testscript из cmd/plait/testdata/script/
make lint                      # golangci-lint, gosec внутри него
make vuln                      # govulncheck
make check                     # fmt-check, test, lint, vuln — перед коммитом
```

Golden-файлы `internal/review/testdata` обновляет `go test ./internal/review -update`.

## Раскладка

Срез отошёл от целевой раскладки брифа: `internal/run` держит run, `state.json` и `next` (в брифе — `pipeline`), `internal/review` — пакет, схему, импорт и отчёт (в брифе — `artifact`), `internal/task` — `ctx save`, `internal/output` — контракт вывода. Сливать с брифом, когда появятся стадии.

## Инварианты, которые легко нарушить

- **Процессом управляет Go-код, а не промпт.** Переходы стадий, валидация артефактов и проверка автономии живут в Go-пакетах (пока `internal/run` и `internal/review`). Skills тонкие (20–40 строк), подробные инструкции агент получает из `plait guide <stage>`.
- **Контракт вывода для агента.** Не в TTY или с `--json` — JSON по версионированной схеме (в срезе — ответ ревью `review-findings/v1`; `review-verdict/v1` брифа — для стадий). Exit codes: 0 ок, 2 — невалидный артефакт, 3 — запрещено политикой, 4 — нужен человек. Текст ошибки — инструкция агенту, что исправить и какую команду вызвать. В неинтерактивном режиме вопросов не задаём; команды идемпотентны.
- **`approve`/`reject` работают только в интерактивном TTY**: так агент не может одобрить сам себя, потому что его Bash-инструмент работает без TTY.
- **Ноль патчей под конкретную организацию в `internal/`.** Всё, чем отличается корпоративная установка, задаётся конфигом (`/etc/plait/config.yaml` → `~/.config/plait` → `.plait/config.yaml` → env → flags), оверлеем промптов, хуками стадий и наследованием адаптеров (`base: qwen`).
- **Сеть только явная.** Ни телеметрии, ни update-check; ходим только в GitLab/GitHub API (и Jira в режиме `rest`). Транспорт общий: `http.ProxyFromEnvironment` + корпоративный CA.
- **Сборка:** один статический бинарь, `CGO_ENABLED=0`, без кодогенерации с сетью. Skills, guide-тексты и шаблоны вшиты через `go:embed`. Версия идёт из `git describe` через `-ldflags` и попадает во frontmatter skills.
- **Read-only ревьюера обеспечивает сам агент:** адаптер переводит `Mode` в флаги CLI агента. Ограничение в промпте не считается.
- **Сообщения других агентов — недоверенные данные.** Права проверяются по `mode`/`can_ask` роли.
- **Лицензия MIT:** код из AGPL-проектов не заимствуем.
