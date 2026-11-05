# plait

A Go CLI used by coding agents, not people. It walks Claude Code, Codex, Gemini CLI, Qwen Code or pi through a team's dev flow (task → review → merge request) and keeps the task's artifacts in the repository, so work survives a session or agent switch.

Process logic, checks and gates live in a deterministic binary, not in a prompt. The developer stays in their own agent and calls a skill such as `/plait-review`; the agent asks `plait next` what to do and saves results through `plait`.

> **Status:** pre-alpha. v0 works end to end for Claude Code and Qwen Code: install, task context, review with run comparison, captured checks, audit log and `mr describe`; the pilot has not run yet. Design docs are in Russian.

## First version (v0)

`ctx` → work as usual → `review` → `mr describe`, with a single agent:

- **`ctx <KEY>`** pulls the task and its acceptance criteria (via the agent's Jira access or a local description).
- **`review`** reviews an exact diff snapshot against the criteria and repo rules in a fresh session, and on re-run shows what was fixed and what is still open.
- **`mr describe`** builds the MR description from the same artifacts.

A second agent from another vendor reviewing read-only comes next, as an opt-in.

plait assists the process; it does not enforce it. An agent can edit code without calling plait. Making the flow mandatory for merge takes a separate CI gate, which v0 does not have.

## Install

Needs Go to build; the binary is static and can be copied to a machine without internet ([closed-network path](docs/pilot/install.md), in Russian).

```sh
make build && install -m 755 bin/plait ~/.local/bin/
plait setup             # skills and a deny rule for approve/reject in every agent found in PATH; --dry-run, --uninstall
plait init              # once per repository: .plait/config.yaml, project skills, AGENTS.md section, .gitignore
plait doctor            # agents, skills, telemetry, model endpoint, proxy and CA, offline; --net also reaches them
```

Then call `/plait-review` in the agent; it follows `plait next` and `plait guide <stage>` from there. `plait ctx PROJ-7` tells the agent how to fetch the Jira task with its own access. Files plait did not write are never overwritten: such a conflict exits 4. A wrapper or fork of a supported agent is a config entry: `agents.<name>` with `base: claude`.

## Try it

Needs Go and git. Run inside a git repository with a commit on `HEAD`; the diff base is `origin/HEAD`, `main` or `master`, or `--base`.

```sh
make build                              # bin/plait
plait run new order-retry --key PROJ-7
plait ctx save PROJ-7 < task.md         # task and acceptance criteria; the key goes into the MR title
go test ./... > test.log; plait check save test --exit-code $? --file test.log   # result bound to the working tree
plait review export                     # snapshot under refs/plait, package in .plait/runs/<id>/review-<n>/package.md
# open a fresh agent session, give it package.md, save its JSON answer
plait review import answer.json         # checked against review-findings/v1 and bound to the snapshot
plait review report                     # findings by status and severity, coverage label with gaps
# fix, then review again: open findings go into the next package for a verdict
plait review export
plait review import answer2.json        # previous[]: fixed | still_open | not_checked per finding key
plait review report                     # fix_confirmed, open, region_changed, outdated
plait mr describe --summary-file -      # title and body into .plait/runs/<id>/mr.md; creates no MR
plait next --json                       # what to do next, at any step
```

A finding counts as fixed only on the reviewer's `fixed` verdict; a changed or deleted region without one is `region_changed` or `outdated`, to recheck. Checks are not run by plait: the agent runs them and saves the output, which counts as fresh only if the working tree matches the snapshot. Files matching `audit.deny_globs` never enter the package; `.plait/runs/<id>/audit.jsonl` logs every run command with paths and hashes, not contents.

`plait run list` and `plait run resume <id>` switch the current run. Run state lives in `.plait/` in the working copy: it is left out of snapshots, and `plait init` hides it in `.gitignore`. Snapshots never touch your index or branches, and `refs/plait/*` are not pushed.

Config layers, later wins: built-in defaults → `/etc/plait/config.yaml` → `~/.config/plait/config.yaml` (honours `XDG_CONFIG_HOME`) → `.plait/config.yaml` → `PLAIT_*` env (`PLAIT_REVIEW_BASE`, lists comma-separated) → flags. Keys: `review.base` — the diff base; `review.rules` — repo files put into the review package, inside the repository only (default `AGENTS.md`, `CLAUDE.md`, `.plait/rules.md`); `review.max_diff_bytes` — above it whole files drop out of the diff, biggest first; `review.checks` — expected check names, missing ones are a coverage gap; `audit.deny_globs` — paths kept out of the package; `mr.template` — repo path to a custom `text/template` for `mr describe`.

Outside a TTY or with `--json` every command answers with JSON; exit codes are 0 ok, 2 invalid input, 3 forbidden by policy, 4 needs a human.

## Design goals

- One static binary, no CGO, no telemetry, no network calls except configured GitLab/GitHub/Jira APIs.
- Works behind proxies with a corporate CA and internal model endpoints.
- Organisation-specific setup is config, prompt overlays and hooks, never a code fork.
- Equal footing for GitLab self-hosted and GitHub.

## Docs

- [Product brief](docs/plait-brief.md)
- [Development plan](docs/development-plan.md)
- [Closed-network install](docs/pilot/install.md)
- [Measurement 1 protocol](docs/pilot/measurement-1.md)

## License

[MIT](LICENSE)
