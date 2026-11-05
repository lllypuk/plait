# plait

A Go CLI used by coding agents, not people. It walks Claude Code, Codex, Gemini CLI, Qwen Code or pi through a team's dev flow (task → review → merge request) and keeps the task's artifacts in the repository, so work survives a session or agent switch.

Process logic, checks and gates live in a deterministic binary, not in a prompt. The developer stays in their own agent and calls a skill such as `/plait-review`; the agent asks `plait next` what to do and saves results through `plait`.

> **Status:** pre-alpha, no code yet. Design docs are in Russian.

## First version (v0)

`ctx` → work as usual → `review` → `mr describe`, with a single agent:

- **`ctx <KEY>`** pulls the task and its acceptance criteria (via the agent's Jira access or a local description).
- **`review`** reviews an exact diff snapshot against the criteria and repo rules in a fresh session, and on re-run shows what was fixed and what is still open.
- **`mr describe`** builds the MR description from the same artifacts.

A second agent from another vendor reviewing read-only comes next, as an opt-in.

plait assists the process; it does not enforce it. An agent can edit code without calling plait. Making the flow mandatory for merge takes a separate CI gate, which v0 does not have.

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
