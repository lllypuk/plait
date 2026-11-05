# Guide: ctx — the task context

`task.md` of the run holds the task as its author wrote it. The reviewer checks the diff against its acceptance criteria, so they must come from the task, not from you.

## With a tracker key

1. `plait ctx <KEY>` says how to fetch the task: the configured MCP tool, or any Jira access you have.
2. Take the summary, the description, the acceptance criteria and the comments that change the scope.
3. Save them as Markdown: `plait ctx save <KEY> --source jira < task.md`.
4. If Jira is not reachable, ask the developer to describe the task and save it with `--source local`.

## Without a key

Save what the developer gives you: `plait ctx save < task.md`.

## Markdown layout

- `# <summary>` as the title, then the description.
- The criteria as a list under `## Acceptance criteria` (or `## Критерии приёмки`).
- A task without criteria is saved as it is: plait marks the gap and the review checks only the diff. Never invent criteria to fill it.

`ctx save` is idempotent: saving the same text again changes nothing. After it, `plait next --json` asks for the review package.
