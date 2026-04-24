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

**Issue**: `ANTHROPIC_API_KEY` is empty in the current session, preventing end-to-end testing.

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

### Integration Tests: ⏸️ BLOCKED

**Reason**: xf-yun API does not support OpenAI function calling

**Verification**:
- Simple request (no tools): ✅ Status 200
- Request with tools: ❌ Status 500 "RequestParamsError:Invalid Params"

The xf-yun xop3qwen32b model does not support the OpenAI `tools` parameter for function calling, which is required by the SWE-bench adapter. The adapter relies heavily on tool calling (shell, read_file, edit_file, grep, etc.) to interact with the codebase.

**Workarounds**:
1. Use an API that supports function calling (OpenAI, Anthropic, compatible providers)
2. Rewrite adapter to use prompt-based tool calling (describe tools in system prompt, parse from text)

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

1. **Obtain API Key**: Configure `OPENAI_API_KEY` for end-to-end testing
2. **Run Full Test Suite**: Test all 6 SWE-bench cases (sympy, django, matplotlib, seaborn, pytest, xarray)
3. **Collect Metrics**: Turn count, token consumption, success rate
4. **Compare with Baseline**: Validate optimization ROI
5. **Document Findings**: Update this file with actual test results

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

**Last Updated**: 2026-04-22
**Status**: Phase 1+2+2.5 complete, integration testing blocked by missing API key
