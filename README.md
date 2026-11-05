# plait

A Go CLI used by coding agents, not people. It walks Claude Code, Codex, Gemini CLI, Qwen Code or pi through a team's dev flow (task → review → merge request) and keeps the task's artifacts in the repository, so work survives a session or agent switch.

Process logic, checks and gates live in a deterministic binary, not in a prompt. The developer stays in their own agent and calls a skill such as `/plait-review`; the agent asks `plait next` what to do and saves results through `plait`.

> **Status:** pre-alpha. The first end-to-end slice of v0 works; setup, doctor and run comparison do not exist yet. Design docs are in Russian.

## First version (v0)

`ctx` → work as usual → `review` → `mr describe`, with a single agent:

- **`ctx <KEY>`** pulls the task and its acceptance criteria (via the agent's Jira access or a local description).
- **`review`** reviews an exact diff snapshot against the criteria and repo rules in a fresh session, and on re-run shows what was fixed and what is still open.
- **`mr describe`** builds the MR description from the same artifacts.

A second agent from another vendor reviewing read-only comes next, as an opt-in.

plait assists the process; it does not enforce it. An agent can edit code without calling plait. Making the flow mandatory for merge takes a separate CI gate, which v0 does not have.

## Try the slice

Needs Go and git. Run inside a git repository with a commit on `HEAD`; the diff base is `origin/HEAD`, `main` or `master`, or `--base`.

```sh
make build                              # bin/plait
plait run new order-retry --key PROJ-7
plait ctx save < task.md                # task and acceptance criteria; missing criteria are marked as a gap
plait review export                     # snapshot under refs/plait, package in .plait/runs/<id>/review-<n>/package.md
# open a fresh agent session, give it package.md, save its JSON answer
plait review import answer.json         # checked against review-findings/v1 and bound to the snapshot
plait review report                     # findings by severity, coverage label, stale mark
plait next --json                       # what to do next, at any step
```

`plait run list` and `plait run resume <id>` switch the current run. Run state lives in `.plait/` in the working copy: it is left out of snapshots, but add it to the repository's `.gitignore` yourself. Snapshots never touch your index or branches, and `refs/plait/*` are not pushed.

Config layers, later wins: built-in defaults → `/etc/plait/config.yaml` → `~/.config/plait/config.yaml` (honours `XDG_CONFIG_HOME`) → `.plait/config.yaml` → `PLAIT_*` env (`PLAIT_REVIEW_BASE`, lists comma-separated) → flags. Keys: `review.base` — the diff base; `review.rules` — repo files put into the review package, inside the repository only (default `AGENTS.md`, `CLAUDE.md`, `.plait/rules.md`).

Outside a TTY or with `--json` every command answers with JSON; exit codes are 0 ok, 2 invalid input, 3 forbidden by policy, 4 needs a human.

## Design goals

- One static binary, no CGO, no telemetry, no network calls except configured GitLab/GitHub/Jira APIs.
- Works behind proxies with a corporate CA and internal model endpoints.
- Organisation-specific setup is config, prompt overlays and hooks, never a code fork.
- Equal footing for GitLab self-hosted and GitHub.

## Docs

- [Product brief](docs/plait-brief.md)
- [Development plan](docs/development-plan.md)
- [Measurement 1 protocol](docs/pilot/measurement-1.md)

## License

[MIT](LICENSE)
