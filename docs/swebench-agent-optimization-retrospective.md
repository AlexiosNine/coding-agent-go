# SWE-bench Agent Optimization Retrospective

## Background

This note summarizes the optimization path for `sympy__sympy-11400` after repeated local failures. The case started as a trajectory-convergence problem: the agent repeatedly used read-only tools, was stopped by guards, and produced no patch.

Successful run:

- Run dir: `swebench/runs/20260609_011540_sympy__sympy-11400`
- Result: `case_success`
- Verification: `passed`
- First edit turn: `9`
- Patch size: `606` bytes, `17` lines

## Failure Modes Found

1. **Insufficient observability**
   - Early logs did not preserve enough structured data to explain why a run stopped.
   - Fix: add `events.jsonl`, `metrics.json`, `summary.md`, `runner_status.jsonl`, and tool `input_preview`.

2. **Read output lacked edit confidence**
   - `read_file` returned code without file/range metadata.
   - Summaries used relative line numbers, encouraging repeated narrowing reads.
   - Fix: `read_file` now emits `File`, absolute `Lines`, and `Content`; summaries preserve absolute symbol lines.

3. **Prompt text was scattered**
   - SWE-bench rules lived as hard-coded Go strings.
   - Fix: introduce `templates/` and render prompt blocks progressively: case, core tool flow, tool-specific rules, SWE-bench rules.

4. **Sandbox failures were hard to diagnose**
   - Tool calls could be blocked before adapter hooks logged them.
   - Fix: include `input_preview` on tool and guard events; add explicit repo-relative path rules.

5. **Summarization was too aggressive**
   - `SWE_TOOL_SUMMARY_CHARS=800` removed exact edit context.
   - Fix: raise default summary budget to `2400`.

6. **Mutation interface was too hard**
   - `edit_file` only supported exact replacement, but adding a printer method is naturally a line insertion.
   - Fix: add `insert_after_line` and `insert_before_line` to `edit_file`.

7. **Guard logic over-penalized normal exploration**
   - `ReadTracker` counted repeated grep against file-region repetition.
   - Aggregate read totals duplicated exploration-budget responsibility.
   - Fix: repeated-region guard now focuses on actual repeated reads; grep does not trigger region repetition.

8. **Budget was too tight for this case**
   - `--budget 8` stopped before the model reached a stable edit.
   - `--budget 12` allowed the model to read enough context, use line insertion, and pass verification.
   - Fix: SWE-bench default budget is now `12`; max read-only turns default is `10`.

## Successful Path

The successful trajectory:

1. Locate C printer and `sinc` references.
2. Read relevant C printer region and target `sinc` definition.
3. Read a nearby insertion anchor around `_print_sign`.
4. Use `edit_file` with `insert_after_line`.
5. Stop after edit; verification passes.

Patch shape:

```python
def _print_sinc(self, func):
    from sympy import Piecewise, sin, Ne
    x = func.args[0]
    return self._print(Piecewise((sin(x)/x, Ne(x, 0)), (1, True)))
```

## Extracted Rules

- Make failed trajectories observable before changing prompts.
- Preserve absolute file/range metadata in read outputs.
- Do not summarize away exact edit anchors.
- Separate repeated-region detection from exploration-budget accounting.
- Do not make grep as expensive as read_file in exploration budgets.
- Prefer line insertion for adding methods or mappings.
- Keep sandbox path rules explicit: repo-relative paths only.
- Treat `metrics.json` as source of truth; text logs are secondary.
- Tune budget per case family; `sympy__sympy-11400` needs about 10-12 exploration actions.

## Recommended Defaults

- `SWE_EXPLORATION_BUDGET=12`
- `SWE_MAX_READ_ONLY_TURNS=10`
- `SWE_TOOL_SUMMARY_CHARS=2400`
- `SWE_TOOLSET=lean`

## Follow-up Work

- Aggregate metrics across the 2-3 selected cases.
- Add a report script that compares guard reasons, first edit turn, patch size, and verification status.
- Consider a two-stage guard for SWE-bench: one blocking tool result warning before final hard stop.
- Add repo-family rules for Django/Pytest if their failure patterns differ from SymPy.
