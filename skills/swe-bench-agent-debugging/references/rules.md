# SWE-bench Agent Debugging Rules

## Failure Diagnosis

- Prefer structured artifacts over text logs.
- `metrics.json` is the source of truth for run result.
- `events.jsonl` explains trajectory shape and guard triggers.
- `adapter.log` explains human-readable tool flow.

## Convergence Rules

- Repeated-region guard should only stop true repeated region reads.
- Grep should not be counted as repeated file-region reading.
- Exploration budget should not make grep as expensive as read_file.
- For SymPy-like printer cases, a budget around `12` is safer than `8`.
- Keep read-only-after-successful-edit as a hard stop.

## Tooling Rules

- `read_file` should include absolute file/range metadata.
- Summaries must preserve absolute line numbers.
- Do not summarize away exact insertion anchors.
- `edit_file` should support line insertion for adding methods/mappings.
- After successful `edit_file`, do not re-read just to verify.

## Prompt Rules

- Use repo-relative paths only.
- Disclose only tools that are available in the current toolset.
- For adding methods, instruct the model to use line insertion instead of replacing large existing methods.
- For printer-function fixes, prefer a small `_print_*` method or local mapping.

## Evaluation Rules

- A patch with failed verification is `case_failed`.
- A verified patch is `case_success`.
- Keep `events.jsonl`, `metrics.json`, `summary.md`, patch, repo diff, and verifier output in the run directory.
