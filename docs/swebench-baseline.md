# SWE-bench Local Baseline

Date: 2026-06-09

This document defines the current local SWE-bench baseline for goagent. The goal is not full benchmark scoring. The goal is a small, repeatable regression suite for trajectory quality, workspace isolation, prompt/template changes, verifier behavior, and agent capability deltas.

## Baseline Suite

The canonical local suite is:

```text
swebench/suites/golden_cases.txt
```

Current cases:

| Instance | Repo | Purpose |
| --- | --- | --- |
| `sympy__sympy-11400` | `sympy/sympy` | Symbolic printer repair. Tests deep code search and avoiding repeated exploration. |
| `django__django-11179` | `django/django` | Framework behavior repair. Tests precise branch targeting in existing control flow. |
| `pytest-dev__pytest-11143` | `pytest-dev/pytest` | AST rewrite repair. Tests small semantic condition changes with minimal patching. |

Keep this set small. Add a case only when it represents a distinct failure mode or skill that is worth guarding.

## Run Commands

Run the full baseline:

```bash
./swebench/run_suite_collect.sh --max-turns 18 --budget 12 --toolset lean --turn-delay 0
```

Run one case:

```bash
./swebench/run_case_collect.sh django__django-11179 \
  --max-turns 18 \
  --budget 12 \
  --toolset lean \
  --turn-delay 0
```

Expected environment:

```bash
export OPENAI_API_KEY="<secret_key>"
export OPENAI_BASE_URL="<base_url>"
export LLM_MODEL="xopkimik25"
```

Local fallback extraction, if needed for this repo's existing e2e config:

```bash
KEY=$(perl -ne 'print "$1\n" if /apiKey = "([^"]+)"/' e2e_test.go | head -1)
BASE=$(perl -ne 'print "$1\n" if /baseURL = "([^"]+)"/' e2e_test.go | head -1)

OPENAI_API_KEY="$KEY" OPENAI_BASE_URL="$BASE" LLM_MODEL=xopkimik25 \
  ./swebench/run_suite_collect.sh --max-turns 18 --budget 12 --toolset lean --turn-delay 0
```

Do not copy secret values into docs, logs, or committed files.

## Current Baseline Result

These runs were produced with model `xopkimik25`, toolset `lean`, max turns `18`, budget `12`, and turn delay `0`.

| Instance | Baseline run | Result | Verify | Turns | Tool calls | First edit | Guard triggers | Patch lines | Duration |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `sympy__sympy-11400` | `swebench/runs/20260609_011540_sympy__sympy-11400` | `case_success` | `passed` | 10 | 12 | 9 | 1 | 17 | 35.6s |
| `django__django-11179` | `swebench/runs/20260609_125903_django__django-11179` | `case_success` | `passed` | 5 | 4 | 4 | 0 | 12 | 38.7s |
| `pytest-dev__pytest-11143` | `swebench/runs/20260609_130143_pytest-dev__pytest-11143` | `case_success` | `passed` | 6 | 5 | 5 | 0 | 13 | 76.8s |

Baseline pass condition:

```text
run_result == "case_success" && verify_status == "passed"
```

Generated patch alone is not success. `verify_status=failed` or `verify_status=skipped` must be treated as not passing the baseline.

## Source Of Truth

Per-run artifacts live under:

```text
swebench/runs/<timestamp>_<instance_id>/
```

Use these files as the source of truth:

| File | Use |
| --- | --- |
| `metrics.json` | Machine-readable outcome and aggregate counters. |
| `events.jsonl` | Tool/model/guard trajectory events. |
| `summary.md` | Human-readable run summary. |
| `prediction_patch.diff` | Patch emitted by the adapter/runner flow. |
| `adapter.log` | Debug log for prompt/tool/verifier behavior. |
| `runner.stdout.log` / `runner.stderr.log` | Runner separation and prediction parsing diagnostics. |

`adapter.log` is for debugging. Do not compute benchmark results from it when `metrics.json` exists.

## HTML Dashboard

Run the local dashboard:

```bash
go run ./swebench/dashboard
```

Default URL:

```text
http://127.0.0.1:8765
```

Useful options:

```bash
go run ./swebench/dashboard \
  --addr 127.0.0.1:8765 \
  --workspace /path/to/coding/workspace \
  --provider openai \
  --model xopkimik25 \
  --max-turns 20 \
  --runs swebench/runs \
  --suite-runs swebench/suite_runs
```

Pages:

| Path | Purpose |
| --- | --- |
| `/` | Command-line style coding-agent chat. The agent can inspect and edit files inside the configured workspace. |
| `/eval` | Read-only SWE-bench evaluation status page. |

The chat page uses a strict filesystem sandbox rooted at `--workspace`. The evaluation page scans run directories, reads `metrics.json` and `events.jsonl`, and links to per-run artifacts such as patch, summary, adapter log, and runner logs.

## Verifier Rules

`swebench/verify_patch.sh` currently supports only the three golden cases.

| Instance | Verifier shape |
| --- | --- |
| `sympy__sympy-11400` | Imports patched SymPy and checks `ccode(sinc(x))` equals the expected `Piecewise` C output. |
| `django__django-11179` | Applies patch and uses AST checks to confirm `Collector.delete()` clears `instance` pk in the single-instance fast-delete early-return branch before returning. |
| `pytest-dev__pytest-11143` | Imports patched pytest assertion rewriter and checks numeric leading expressions are not treated as docstrings. |

The Django verifier is intentionally static. Old Django plus modern local Python can fail on environment dependencies unrelated to this case. The static verifier checks the exact behavioral repair location and avoids counting local dependency breakage as agent failure.

## Prompt Baseline

The baseline uses progressive prompt templates:

| Layer | Files |
| --- | --- |
| SWE-bench case | `templates/swebench/case.md` |
| Tool guidance | `templates/tools/grep.md`, `templates/tools/read_file.md`, `templates/tools/edit_file.md` |
| Repo guidance | `templates/swebench/repos/django.md`, `templates/swebench/repos/pytest.md` |
| Rules | `templates/swebench/rules.md` |

Repo guidance is allowed when it encodes stable repository navigation and failure-mode distinctions. It should not hard-code one-off patch text unless the goal is an explicit oracle test.

## Interpreting Regressions

Use this triage order:

1. `verify_status=failed`: inspect `prediction_patch.diff` first, then verifier output in `adapter.log`.
2. `stopped_by_guard` with no patch: inspect `events.jsonl` for repeated read regions and first planned edit.
3. Patch in wrong location: add or refine repo-level guidance only if the distinction is reusable for that repo family.
4. `tool_error`: inspect `edit_file` errors and whether the model ignored actual-content recovery instructions.
5. `provider_error`: do not score as agent capability failure unless it reproduces across retries.

## Change Policy

Before changing guard, tool, prompt, verifier, or adapter behavior:

1. Run the affected unit tests.
2. Run at least the case whose failure mode is affected.
3. Compare against this baseline table.
4. Record any new passing run directory if it becomes the new baseline.

Minimum test commands:

```bash
bash -n swebench/verify_patch.sh
GOCACHE=/private/tmp/goagent-gocache go test -count=1 . ./tool
(cd swebench/adapter && GOCACHE=/private/tmp/goagent-gocache go test -count=1 .)
```

## Known Limits

- Three cases are a regression baseline, not a statistically meaningful SWE-bench score.
- Current baseline is tied to model `xopkimik25`; changing model changes the baseline.
- `sympy__sympy-11400` passed with `guard_triggers=1`, so guard-triggered post-edit behavior needs careful interpretation. A passing verified patch still counts as success, but this case remains the main signal for over-exploration.
- Verifiers are case-specific and intentionally minimal. They should be expanded only when the added checks reduce false positives without reintroducing environment fragility.
