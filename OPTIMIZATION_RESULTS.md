# SWE-bench Optimization Results

## Overview

This document summarizes the Phase 1 and Phase 2 architecture optimizations applied to the goagent coding agent, aimed at improving performance on SWE-bench benchmark tasks.

## Optimization Phases

### Phase 1: Critical Infrastructure (Completed)

**Commit**: `ed88f40` - feat: Phase 1 architecture optimizations (3 critical improvements)

1. **Tool Timeout** (`goagent/session.go`)
   - Added 60s timeout for tool execution to prevent hangs
   - Uses `context.WithTimeout` in `executeSingleTool()`
   - Prevents infinite waits on unresponsive tools

2. **Continuation Check** (`goagent/session.go`)
   - Auto-resumes when LLM hits `max_tokens` to prevent patch truncation
   - Injects `[continue]` message and repeats query loop
   - Max 3 continuations to prevent infinite loops
   - Solves the "incomplete patch" problem in SWE-bench

3. **Concurrent Safety** (`goagent/session.go`)
   - Added `sync.RWMutex` to protect `activeSkills` slice
   - Thread-safe `getActiveSkills()` helper
   - Prevents data races in multi-threaded scenarios

**Test Results**: All unit tests pass

---

### Phase 2: Token Optimization (Completed)

**Commit**: `85933f8` - feat: Phase 2 architecture optimizations (3 improvements)

1. **Tool Output Compression** (`goagent/tool_output_compressor.go`)
   - Smart per-tool truncation strategy:
     - `read_file`: keep first 60% + last 40%
     - `shell`: keep tail (errors usually at end)
     - `grep`: keep first N matches
     - `edit_file`/`write_file`: no compression
   - Default max size: 8000 chars
   - Reduces token waste from large tool outputs

2. **Tool Retry with Exponential Backoff** (`goagent/errors.go`, `goagent/session.go`)
   - Detects transient errors: timeout, connection refused/reset, rate limit (429, 503)
   - Retries up to 3 times with exponential backoff (1s, 2s, 4s)
   - Improves resilience against API instability

3. **LLM-based Context Compaction** (`goagent/compactor.go`)
   - Compactor interface with two implementations:
     - `RuleCompactor`: rule-based (fast, deterministic)
     - `LLMCompactor`: semantic summarization (preserves meaning)
   - Circuit breaker pattern: falls back to rule-based on LLM failure
   - Preserves more semantic information than pure truncation

**Test Results**: All unit tests pass (7 compactor tests, 4 circuit breaker tests)

---

### Phase 2.5: ExplorationBudget Bug Fixes (Completed)

**Commit**: `90af59a` - fix: exploration budget nudge lost in compression

**Problem**: ExplorationBudget was configured (`WithExplorationBudget(15)`) but nudge messages were never seen by the model, causing excessive exploration turns (18 turns vs baseline 9-14).

**Root Causes Identified**:

1. **memory_compress.go**: `keepLast` used `n/10` instead of `recentWindow`
   - Nudge messages were compressed away before model saw them
   - Fixed: `keepLast := max(c.recentWindow, n/10)`

2. **session.go**: `consecutiveExplorationTurns` counter ran unconditionally
   - Dead code when `explorationBudget` was active
   - Fixed: Removed duplicate logic, moved into `else` branch

3. **session.go**: Nudge not persistent across compression cycles
   - Nudge added to memory but lost during next compression
   - Fixed: Inject nudge into `systemOverride` so it survives compression
   - Reset `systemOverride` when agent starts editing

**Test Results**: All unit tests pass

---

## Implementation Details

### Exploration Budget Mechanism

Located in `goagent/exploration_budget.go`:

```go
type ExplorationBudget struct {
    budget    int // initial budget (e.g., 15)
    remaining int
    tracker   *ReadTracker // detects repeated reads
}
```

**Token Costs**:
- Read-only tool: 1 token
- Repeated read: 2 tokens (detected by ReadTracker)
- Mutating tool (edit_file, write_file): resets budget

**Nudge Message** (when budget exhausted):
```
[System notice] Exploration budget exhausted (15/15 tokens used). 
You MUST use edit_file now to make changes, or respond with text if no changes are needed.
```

**Persistence Strategy**:
- Nudge added to memory as `UserMessage`
- Also injected into `systemOverride` field
- `systemOverride` prepended to system prompt in every LLM call
- Survives compression because system prompt is never compressed
- Cleared when agent starts editing (budget resets)

---

## SWE-bench Adapter Configuration

File: `goagent/swebench/adapter/main.go`

```go
agent := cc.New(
    cc.WithProvider(provider),
    cc.WithModel(model),
    cc.WithMaxTokens(102400),
    cc.WithTurnDelay(15 * time.Second),        // Rate limiting for xf-yun API
    cc.WithTokenAwareCompressMemory(20000, 3), // Compress at 20k tokens, keep last 3
    cc.WithToolOutputMaxSize(8000),            // Phase 2.1: Tool output compression
    cc.WithToolResultSummary(800),             // Summarize tool results
    cc.WithSessionFactCache(20),               // Cache import statements
    cc.WithExplorationBudget(15),              // Phase 2: Unified exploration tracking
    cc.WithMaxTurns(25),
    cc.WithMaxExplorationTurns(0),             // Disabled (using ExplorationBudget instead)
)
```

### 2026-06-01 Small-Case Configuration

The current SWE-bench work is intentionally scoped to a 3-case loop instead of a broad run:

- `django__django-11179`
- `sympy__sympy-11400`
- `pytest-dev__pytest-11143`

The adapter now prepares a local per-instance workspace and runs the agent from inside that checkout:

- Default workspace root: `goagent/swebench/workspace`
- Per-case path: `goagent/swebench/workspace/<instance_id>`
- Override: `SWE_WORKSPACE_ROOT=/absolute/path`
- Logs: `<workspace_root>/logs/<instance_id>.log`
- Patches: `<workspace_root>/patches/<instance_id>.patch`

Each workspace is reset to the benchmark base commit before a run:

```text
git checkout <base_commit>
git reset --hard <base_commit>
git clean -fd
```

The default toolset is now deliberately small:

```text
SWE_TOOLSET=lean: grep, read_file, edit_file
SWE_TOOLSET=full: read_file, write_file, edit_file, list_files, grep
```

`shell` is excluded from both toolsets for this benchmark path. The adapter also applies `WithStrictSandbox(workDir)`, so file tools are scoped to the prepared case workspace.

Current runtime knobs:

| Env var | Default | Purpose |
|---------|---------|---------|
| `SWE_MAX_TOKENS` | `4096` | Max model output tokens per response |
| `SWE_CONTEXT_WINDOW` | `12000` | Token-aware memory compression threshold |
| `SWE_RECENT_WINDOW` | `4` | Recent messages preserved during compression |
| `SWE_TOOL_OUTPUT_CHARS` | `5000` | Max retained tool output size |
| `SWE_TOOL_SUMMARY_CHARS` | `1000` | Max structured tool summary size |
| `SWE_EXPLORATION_BUDGET` | `10` | Read/exploration budget before edit nudge |
| `SWE_MAX_TURNS` | `18` | Max agent turns per case |
| `SWE_TOOLSET` | `lean` | Toolset selection |
| `TURN_DELAY` | `15s` | Delay between model turns |

Runner usage for the fixed 3-case loop:

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent/swebench/runner

KEY=$(perl -ne 'print "$1\n" if /apiKey = "([^"]+)"/' ../../e2e_test.go | head -1)
BASE=$(perl -ne 'print "$1\n" if /baseURL = "([^"]+)"/' ../../e2e_test.go | head -1)

OPENAI_API_KEY="$KEY" OPENAI_BASE_URL="$BASE" LLM_MODEL=xopkimik25 \
TURN_DELAY=0 SWE_TOOLSET=lean SWE_MAX_TURNS=18 SWE_EXPLORATION_BUDGET=10 \
SWE_MAX_TOKENS=4096 SWE_TOOL_OUTPUT_CHARS=5000 SWE_TOOL_SUMMARY_CHARS=1000 \
./runner --dataset ../../../swebench_lite.json \
  --instances django__django-11179,sympy__sympy-11400,pytest-dev__pytest-11143 \
  --output /tmp/goagent-smallcases-lean.jsonl
```

The command extracts secrets into local shell variables and passes them only to the child process. Do not paste the extracted key into docs, logs, or committed config.

---

## Test Environment Challenges

### API Key Configuration

The SWE-bench adapter requires `OPENAI_API_KEY` environment variable, but the current environment uses `ANTHROPIC_API_KEY` for the xf-yun API proxy.

**Workaround**:
```bash
export OPENAI_API_KEY="${ANTHROPIC_API_KEY}"
export OPENAI_BASE_URL="${ANTHROPIC_BASE_URL}"
export LLM_MODEL="xopkimik25"
```

**Current status**: local fallback config is available in the e2e test files, so native tool-calling e2e can be run when outbound network/API access is allowed.

**Current local fallback**: `e2e_test.go` and `subagent_e2e_test.go` contain fallback OpenAI-compatible config for local e2e tests. The safe extraction pattern is:

```bash
KEY=$(perl -ne 'print "$1\n" if /apiKey = "([^"]+)"/' e2e_test.go | head -1)
BASE=$(perl -ne 'print "$1\n" if /baseURL = "([^"]+)"/' e2e_test.go | head -1)
```

Use `KEY` as `OPENAI_API_KEY` and `BASE` as `OPENAI_BASE_URL`. The fallback `base_url` is `https://maas-api.cn-huabei-1.xf-yun.com/v2`; the secret value should not be copied into this document.

### API Stability

The xf-yun.com API frequently returns "system is busy" errors, requiring:
- 15s turn delay between LLM calls
- 20k token threshold before compression (vs default 10k)
- Retry mechanism for transient errors

---

## Verification Status

### Unit Tests: ✅ PASS

All unit tests pass for:
- ExplorationBudget (exhaustion nudge, mutation detection)
- CompressMemory (LLM compactor, fallback, recent window preservation)
- CircuitBreaker (state transitions, cooldown)
- ToolTimeout (context cancellation)

### Integration Tests: ✅ TOOL CALLING VERIFIED

The current local OpenAI-compatible xf-yun configuration has been verified with native tool calling:

```bash
OPENAI_API_KEY="$KEY" OPENAI_BASE_URL="$BASE" LLM_MODEL=xopkimik25 \
GOCACHE=/private/tmp/goagent-gocache \
go test -tags e2e -run TestE2E_ToolCalling -count=1 -v .
```

Result: the e2e tool-calling test passed in 2 turns.

Note: network/API tests may require sandbox escalation in Codex because DNS and outbound network are restricted by default.

**Expected Behavior** (after fixes, with compatible API):
- ExplorationBudget should trigger nudge after 15 read-only tool calls
- Nudge should persist in systemOverride and survive compression
- Agent should start editing sooner (target: 9-14 turns vs baseline 18)

**Test Command** (when API key available):
```bash
cd goagent/swebench/adapter
export OPENAI_API_KEY="<your-key>"
export OPENAI_BASE_URL="<your-base-url>"
./run_test.sh sympy__sympy-11400 xopkimik25
```

---

## Performance Metrics (Expected)

### Baseline (Before Optimizations)
- **sympy__sympy-11400**: 9-14 turns (from summary)
- **django__django-11179**: 4 turns (from summary)

### After Phase 1+2 (First Test)
- **sympy__sympy-11400**: 18 turns ❌ (worse than baseline)
- **Reason**: ExplorationBudget nudge lost in compression

### After Phase 2.5 (Expected)
- **sympy__sympy-11400**: 9-12 turns ✅ (target)
- **Mechanism**: Nudge persists in systemOverride, forces editing after 15 reads

---

## Key Learnings

### 1. Nudge Persistence is Critical

**Problem**: Nudges added to conversation memory get compressed away before the model sees them.

**Solution**: Inject nudges into `systemOverride` field, which is prepended to system prompt on every LLM call and never compressed.

### 2. recentWindow Must Be Respected

**Problem**: `compress()` method ignored `recentWindow` parameter, using `n/10` instead.

**Impact**: With 40 messages and `recentWindow=3`, only last 4 messages kept (10%), not last 3 as configured.

**Fix**: `keepLast := max(c.recentWindow, n/10)` ensures recentWindow is always respected.

### 3. Dead Code Accumulation

**Problem**: When adding new features (ExplorationBudget), old code paths (consecutiveExplorationTurns) were left in place, running unconditionally.

**Impact**: Confusing logic, potential bugs, wasted CPU cycles.

**Fix**: Move legacy code into `else` branch, only run when new feature is disabled.

---

## Next Steps

1. **Run Fixed 3-Case Loop**: Execute the selected Django/SymPy/Pytest cases with `SWE_TOOLSET=lean`.
2. **Collect Metrics**: Parse logs for turn count, tool calls, exploration-budget nudges, patch size, and prediction success.
3. **Compare Lean vs Full**: Re-run the exact same cases with `SWE_TOOLSET=full` and compare success/token tradeoffs.
4. **Generalize Verification**: Replace the current SymPy-specific verifier script with per-repo/per-instance verification.
5. **Add Repo Cache**: Use a local git mirror/cache so workspace preparation does not depend on repeated remote clone.
6. **Improve Read/Edit Precision**: Add line-numbered compact reads or grep-to-read range hints to reduce edit misses.

---

## Files Modified

### Phase 1
- `goagent/session.go` (tool timeout, continuation check, concurrent safety)
- `goagent/agent.go` (toolTimeout field)
- `goagent/options.go` (WithToolTimeout option)

### Phase 2
- `goagent/tool_output_compressor.go` (NEW)
- `goagent/compactor.go` (NEW)
- `goagent/circuit_breaker.go` (NEW)
- `goagent/errors.go` (transient error detection)
- `goagent/exploration_budget.go` (NEW)
- `goagent/memory_compress.go` (compactor integration)
- `goagent/session.go` (tool retry, exploration budget)
- `goagent/agent.go` (new fields)
- `goagent/options.go` (new options)

### Phase 2.5
- `goagent/memory_compress.go` (respect recentWindow)
- `goagent/session.go` (nudge persistence, dead code removal)

### Tests
- `goagent/tool_timeout_test.go` (NEW)
- `goagent/compactor_test.go` (NEW)
- `goagent/circuit_breaker_test.go` (NEW)
- `goagent/exploration_budget_test.go` (NEW)

---

## Troubleshooting Guide

See: `/Users/alexioschen/.claude/skills/swe-bench-optimization-troubleshooting.md`

Covers 6 common issues:
1. Nudge lost in compressor/summarizer pipeline
2. Repeated read detection too aggressive
3. API rate limiting
4. SessionFactCache concurrent map writes
5. grep parameter type errors
6. edit_file error hints too vague

---

**Last Updated**: 2026-06-01
**Status**: Phase 1+2+2.5 complete; current focus is fixed 3-case SWE-bench loop with local workspace isolation
