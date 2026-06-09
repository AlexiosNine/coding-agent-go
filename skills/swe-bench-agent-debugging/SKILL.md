---
name: swe-bench-agent-debugging
description: Debug and optimize local SWE-bench coding-agent runs, especially stopped_by_guard, no-patch, tool-error, and verification-failure trajectories.
auto_match: true
keywords: ["SWE-bench", "swebench", "stopped_by_guard", "metrics.json", "events.jsonl", "trajectory", "verification failed", "no changes made"]
priority: 8
---

## Instructions

Use this skill when investigating a local SWE-bench run or tuning the coding-agent loop.

1. Start from the latest run directory under `swebench/runs/`.
2. Read `metrics.json`, `runner_status.jsonl`, and `summary.md`.
3. Use `events.jsonl` to inspect `guard_reason`, tool names, and `input_preview`.
4. Read `adapter.log` only after structured artifacts identify the failing phase.
5. Classify the failure as one of:
   - no patch
   - stopped by guard
   - tool error
   - provider error
   - verification failed
6. Apply the smallest fix to the failing layer:
   - prompt/template if the model lacks instruction
   - tool schema or output if the model cannot act
   - guard/budget if normal exploration is being misclassified
   - verifier/runner if the result is counted incorrectly
7. Re-run one case and compare metrics before broadening scope.

For detailed reusable rules, read `references/rules.md`.
