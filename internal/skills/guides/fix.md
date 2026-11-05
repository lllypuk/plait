# Guide: fix — after the review

The report lists open findings by severity: `blocker`, `major`, `minor`, `nit`. While a blocker or major finding is open, `plait next` asks for another review round before the merge request.

1. Go through the findings with the developer. Fix the ones the developer accepts; the developer decides about the rest.
2. Change only what the findings require; a broader change makes the next review harder.
3. Run the checks again and save them: `plait check save <name> --exit-code <code> --file <output>`.
4. `plait review export` writes a new package. The open findings go into it, and the reviewer gives each one a verdict: `fixed`, `still_open` or `not_checked`.
5. Hand the package to a fresh session as before (`plait guide review`), then `plait review import` and `plait review report`.

A finding counts as fixed only on the reviewer's `fixed` verdict. `region_changed` or `outdated` mean its code changed without a verdict: the next round checks it again.

Do not mark findings as fixed yourself and do not edit files under `.plait/runs/`.
