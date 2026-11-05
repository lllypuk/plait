# Trellis

Разобрано 2026-11-05: репозиторий [mindfold-ai/Trellis](https://github.com/mindfold-ai/Trellis) на коммите `f089cb3` (CLI 0.6.17), документация [mindfold-ai/docs](https://github.com/mindfold-ai/docs) на `ec975c3` (= [docs.trytrellis.app](https://docs.trytrellis.app)). Ссылки на код ниже ведут на этот коммит.

## Что делает

Слой проекта поверх coding-агента: specs, задачи и журналы сессий хранятся в репо и подмешиваются в каждую сессию.

- **Установка:** `npm install -g @mindfoldhq/trellis`, затем `trellis init` генерирует файлы под выбранные агенты ([README](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/README.md)).
- **Задачи** живут в `.trellis/tasks/<MM-DD-name>/`: `task.json`, `prd.md`, по желанию `design.md`/`implement.md`, манифесты контекста `implement.jsonl`/`check.jsonl` для субагентов. Управляет ими `python3 .trellis/scripts/task.py` ([workflow.md](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/templates/trellis/workflow.md)).
- **Workflow:** Plan → Execute → Finish, шаги помечены `[required · once]` и т. п. Описан прозой в `workflow.md`; хук на каждый промпт вставляет в контекст напоминание о текущем шаге ([inject-workflow-state.py](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/templates/shared-hooks/inject-workflow-state.py)).
- **Память:** журналы в `.trellis/workspace/<dev>/`; `trellis mem` читает локальные транскрипты Claude Code, Codex и Pi и ничего не выгружает ([trellis-meta skill](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/templates/common/bundled-skills/trellis-meta/SKILL.md)).
- **Агенты:** 22 платформы, включая Claude Code, Codex, Gemini CLI, Pi. Qwen Code в списке нет, но `.agents/skills/` пишется для всех платформ как общий слой ([multi-platform](https://docs.trytrellis.app/advanced/multi-platform)).
- **Гейты.** Переходы между фазами не проверяются кодом: порядок шагов держится на `workflow.md` и напоминании хука. Жёсткая проверка одна: `task.py start` с кодом 1 отказывается стартовать, если манифесты контекста пустые ([task.py#L194](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/templates/trellis/scripts/task.py#L194-L214)). Коммит агент делает после подтверждения человеком в чате, это правило промпта ([workflow.md, 3.4](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/templates/trellis/workflow.md)). Политики автономии нет.
- **Ревью.** Шаг 2.2 запускает субагента `trellis-check`: он сверяет дифф со specs и `prd.md`, гоняет lint/тесты и сам исправляет найденное ([trellis-check](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/.agents/skills/trellis-check/SKILL.md)). Ревьюер того же агента, находки нигде не сохраняются.
- **Ревью другим агентом есть с v0.6:** `trellis channel` запускает воркеров Claude или Codex в отдельной сессии, ведёт многораундовое и параллельное ревью через JSONL-лог событий ([channel](https://docs.trytrellis.app/advanced/channel)). Воркер Claude запускается с `--permission-mode bypassPermissions` ([claude.ts#L276](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/commands/channel/adapters/claude.ts#L276-L280)), у Codex по умолчанию `workspace-write`, `read-only` включается флагом `--sandbox` ([codex.ts#L665](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/commands/channel/adapters/codex.ts#L665-L700)). Структурированного вердикта, сведения находок разных ревьюеров и привязки к снимку диффа в документации канала и адаптерах не нашёл.
- **MR/PR.** `workflow.md` перечисляет `task.py create-pr`, но в `task.py` 0.6.17 такой подкоманды нет: документация разошлась с кодом. Описания MR/PR не генерирует, в GitHub/GitLab API не ходит. GitLab, в том числе self-hosted, встречается только как источник шаблонов specs в `trellis init -r` ([template-fetcher.ts](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/utils/template-fetcher.ts)).
- **Трекер:** опциональный хук синхронизирует задачи с Linear через CLI `linearis` ([linear_sync.py](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/.trellis/scripts/hooks/linear_sync.py)). Jira нет.

## Стек, окружение, лицензия

- Node.js ≥ 18 и Python ≥ 3.9 ([README](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/README.md#L49)). CLI на TypeScript, скрипты на Python копируются в `.trellis/scripts/` каждого репо.
- Сеть: `trellis init` по умолчанию тянет индекс шаблонов с `raw.githubusercontent.com/mindfold-ai/marketplace` ([template-fetcher.ts#L20](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/utils/template-fetcher.ts#L20)), `trellis update` ходит в `registry.npmjs.org` ([update.ts#L1373](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/commands/update.ts#L1373)). Проверка версии при старте CLI локальная, сравнивает с `.trellis/.version` ([cli/index.ts#L28](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/cli/index.ts#L28-L54)).
- Прокси: `HTTP(S)_PROXY`/`ALL_PROXY` через undici ([proxy.ts](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/packages/cli/src/utils/proxy.ts)). Корпоративный CA — не проверено.
- Телеметрии в `packages/cli/src` и `packages/core/src` не нашёл (поиск по `telemetry`, `analytics`, `posthog`).
- **Лицензия AGPL-3.0-only** ([LICENSE](https://github.com/mindfold-ai/Trellis/blob/f089cb3286071199e84118dee86b6d76aab032f1/LICENSE)). Код не заимствуем, только идеи.

## Что взять (идеи, не код)

- `trellis ablate` / `trellis restore`: временно убрать инструмент из репо и сравнить сессии с ним и без. Это готовая схема для A/B в замере 1.
- Гейт на пустой манифест контекста — тот же приём, что валидация пакета ревью в plait: отказ с точной командой исправления.
- `.agents/skills/` как общий слой — уже в брифе, подтверждается.
- Урок из [changelog v0.4.0](https://docs.trytrellis.app/changelog/v0.4.0): контекст из SessionStart больше ~20 KB Claude Code молча обрезал, и состояние задачи терялось. Вывод `plait guide` и пакет ревью держать под этим порогом.
- Codex 0.129+ требует один раз одобрить хуки через `/hooks`, иначе они молча не работают ([multi-platform](https://docs.trytrellis.app/advanced/multi-platform)). Учесть в `plait install`.

## Пересечение с v0

| v0 | Trellis | Разница |
| --- | --- | --- |
| `ctx`: агент работает от критериев приёмки | `prd.md` задачи и specs подмешиваются в каждую сессию, журналы переживают смену сессии | Покрывает и шире. plait-у не нужно повторять память сессий |
| `review` в свежей сессии | `trellis-check` (тот же агент, правит сам) и `trellis channel` (Claude/Codex в отдельной сессии) | Нет снимка диффа, сравнения находок между прогонами, метки «по критериям / только дифф», сведённого списка, вердикта по JSON-схеме и кодов выхода. Ревьюер Claude пишет в репо |
| `mr describe` | Нет; `create-pr` описан, но не реализован | Не покрыто |

## Вывод для v0

Строка брифа устарела: ревью другим агентом в Trellis есть (`trellis channel`), значит, отличаться этим plait не может. Из трёх отличий брифа в v0 работает одно: Go-бинарь без Node/Python и без сетевых вызовов по умолчанию. Это довод для закрытого контура, но не польза для разработчика. Гейты и политика автономии в v0 не входят, GitLab self-hosted в v0 не нужен: MR v0 не создаёт.

Для v0 отличие подтверждается частично и держится на трёх вещах, которых в Trellis нет: ревью привязано к снимку диффа, находки сравниваются между прогонами, описание MR собирается из тех же данных. Это ровно гипотеза из «Первой версии»: если это не экономит время по сравнению со skill с шаблоном, упрощаем продукт. Отличие от Trellis по полному roadmap (детерминированные стадии, политика автономии, GitLab наравне с GitHub) сильнее, но v0 его не проверяет.
