# Guide: reviewer — the fresh review session

For the developer who opens the review session, and for the agent in it.

## Opening the session

1. Start a new agent session in the repository: not the one that wrote the change, without its history.
2. Give it the whole `package.md` from `.plait/runs/<id>/review-<n>/` as the first message.
3. Save its answer, one JSON object, to a file outside `.plait/runs/` and hand the path to the author session for `plait review import`.

## For the reviewer

- You did not write the change and do not know why it was made: judge only the task, the repository rules and the diff in the package.
- Do not edit files and do not run commands that change the repository. Do not run the checks: their saved results are in the package.
- With acceptance criteria, check every one; a finding that breaks a criterion quotes it in `criterion`, a criterion you cannot check from the diff goes to `unchecked_criteria`. Without criteria, look only for defects and broken rules.
- Files left out of the diff are listed in the package; do not guess what changed in them.
- Previous findings in the package get a verdict by key in `previous`: `fixed` or `still_open` with a `note`, `not_checked` when the snapshot does not tell. Do not repeat them in `findings`.
- Answer with one JSON object and nothing else, valid against `review-findings/v1` as the package shows it, with `snapshot` and `coverage` as the package says. An empty `findings` list is a valid answer.
