# Guide: review — the author side

The change is reviewed by a fresh agent session that sees only the task, the repository rules, the diff and the saved checks. You prepare its package and handle its answer; you do not review the change yourself.

## Checks

plait does not run checks. Run the ones the team expects (the `review.checks` config lists them) and save each:

    go test ./... > test.log; plait check save test --exit-code $? --file test.log

A result counts only for the working tree it was saved on; after further edits save it again.

## Export

`plait review export` snapshots the working copy under `refs/plait/` (your index and branches stay untouched) and writes `.plait/runs/<id>/review-<n>/package.md`. Missing criteria, missing checks and dropped files show up as coverage gaps in the package.

## The fresh session

Ask the developer to open a new agent session, give it `package.md` and save its answer, one JSON object, to a file. Do not open it from this session and do not answer the package yourself: a review by the author's session is not a review. `plait guide reviewer` is the instruction for that session.

## Import and report

1. `plait review import <file>` (or `-` for stdin) validates the answer against `review-findings/v1` and binds it to the snapshot. On exit code 2 the answer is invalid: ask the developer to have the reviewer fix it as `fix` says; do not edit the answer yourself.
2. `plait review report` shows the findings by severity and status with the coverage label. Show it to the developer.

Then `plait next --json` leads to fixing (`plait guide fix`) or to the merge request (`plait guide mr`).
