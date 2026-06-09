# SWE-bench Agent Rules

## Trajectory Rules

- Use structured artifacts first: `metrics.json`, `events.jsonl`, `runner_status.jsonl`, then text logs.
- Classify failure before tuning:
  - no patch
  - stopped by guard
  - tool error
  - provider error
  - verification failed
- When a guard fires, inspect `guard_reason`, `tool`, and `input_preview`.

## Prompt Rules

- Keep base prompt short.
- Add tool-specific guidance only for exposed tools.
- Use repo-relative path examples.
- For printer/function fixes, say whether the expected mutation is a mapping or a new `_print_*` method.

## Tool Rules

- `grep` is for locating symbols and references; it should not by itself trigger repeated-region guards.
- `read_file` output must include absolute file/range metadata.
- Preserve enough read context for exact edits.
- For additions, prefer `edit_file` with `insert_after_line` or `insert_before_line`.
- Do not replace large existing methods just to add a small method.

## Guard And Budget Rules

- Repeated-region guard should track actual repeated file regions, not grep counts.
- Exploration budget should constrain expensive context reads more than grep.
- SWE-bench default budget should be high enough to allow one edit; for `sympy__sympy-11400`, use `12`.
- Read-only-after-successful-edit should remain a hard stop.

## Evaluation Rules

- Verification failure is not capability success.
- `metrics.json.run_result` is the source of truth.
- A generated patch with failed verification is `case_failed`.
- A verified patch is `case_success`, even if the final stop reason is a post-edit guard.
