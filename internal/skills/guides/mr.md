# Guide: mr — the merge request description

`plait mr describe` writes `.plait/runs/<id>/mr.md`: a title with the task key, your summary, the task, the review coverage, the open blocker and major findings and the checks. It creates no merge request; the developer does.

1. `plait next --json` offers `plait mr describe` once a review is reported. With blocker or major findings still open, tell the developer: the description lists them, and fixing means a new review round.
2. Write a summary of two to five lines: what changed and why. Do not repeat the task text and do not claim what the review did not check.
3. `plait mr describe --summary-file <file>`, or `--summary-file -` with the summary on stdin.
4. Show the title and body to the developer.

If the working copy changed after the reviewed snapshot, `mr describe` stops with `snapshot_stale`. Review the current code (`plait review export`), or, only when the developer says so, describe the reviewed snapshot with `--allow-stale`; the description then marks it as stale.

The `mr.template` config sets a custom template for the description.
