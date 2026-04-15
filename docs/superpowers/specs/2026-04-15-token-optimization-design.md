# Token Optimization — Design Spec

**Date**: 2026-04-15
**Scope**: 3 independent optimizations, each independently mergeable
**Goal**: Reduce token consumption 30-50% across all LLM providers

---

## Context

Current goagent sends full tool results and all tool definitions on every LLM call. Long conversations accumulate redundant content: repeated grep results, re-read files, regenerated tool schemas. This wastes tokens and increases cost/latency proportionally.

Three optimizations address this:
1. **Unified pagination** — tools return pages instead of full output
2. **Tool definition caching** — compute JSON schemas once, reuse every turn
3. **Message deduplication** — detect and collapse repeated tool results

---

## 1. Unified Pagination + OutputBuffer

### Problem
Tools like `read_file`, `shell`, `grep`, `list_files` can return thousands of lines. All content goes into Memory (conversation history), inflating every subsequent LLM call.

### Design

**OutputBuffer** — Session-level cache for complete tool outputs.

```go
// output_buffer.go
type OutputBuffer struct {
    mu      sync.RWMutex
    entries map[string]*BufferEntry  // toolUseID -> entry
    maxSize int64                    // 50MB default
    size    int64
}

type BufferEntry struct {
    Lines     []string
    CreatedAt time.Time
}

func (b *OutputBuffer) Store(id string, content string)
func (b *OutputBuffer) GetPage(id string, offset, limit int) (page string, total int, hasMore bool)
```

**Session integration**:
```go
type Session struct {
    agent          *Agent
    memory         Memory
    outputBuffer   *OutputBuffer  // new
}
```

**Paginated tool results** — Memory stores only the current page + pagination hint. OutputBuffer stores the full output for subsequent page requests.

Tool-specific pagination behavior:

| Tool | Default Limit | Pagination Style |
|------|--------------|-----------------|
| `read_file` | 200 lines | Sequential pages |
| `shell` | 200 lines | Sequential pages |
| `grep` | 50 matches | Sequential pages |
| `list_files` | 100 entries | Sequential pages |

**Tool result format in Memory**:
```
[Page 1/25] Lines 1-200 of 5000:
<content>
---
Total: 5000 lines. Use read_file with offset=200 to see next page.
```

**Tool input changes**:
```go
// All paginated tools gain these optional fields:
type paginatedInput struct {
    Offset int `json:"offset" desc:"Skip first N lines/items (default 0)"`
    Limit  int `json:"limit" desc:"Max lines/items to return (default varies by tool)"`
}
```

When a tool is called with offset > 0, it reads from OutputBuffer instead of re-executing.

**Context propagation**: OutputBuffer is passed to tools via context.Context:
```go
type outputBufferKey struct{}
func WithOutputBuffer(ctx context.Context, buf *OutputBuffer) context.Context
func GetOutputBuffer(ctx context.Context) *OutputBuffer
```
Session sets this in `executeSingleTool()` before calling `tool.Execute(ctx, input)`.

**Lifecycle**: OutputBuffer is created with Session, cleared when Session ends. LRU eviction when maxSize exceeded.

### Files
- Create: `output_buffer.go` (~80 lines)
- Modify: `session.go` — add outputBuffer field, pass to tool execution
- Modify: `tool/read_file.go` — add offset/limit, store in buffer
- Modify: `tool/shell.go` — add offset/limit, store in buffer
- Modify: `tool/grep.go` — add offset/limit, store in buffer
- Modify: `tool/list_files.go` — add offset/limit, store in buffer

---

## 2. Tool Definition Caching

### Problem
`agent.toolDefs()` regenerates JSON schemas for all tools on every LLM call. With 7 tools, this creates ~7 allocations per turn.

### Design

**Lazy-invalidated cache** on Agent:

```go
type Agent struct {
    tools          map[string]Tool
    cachedToolDefs []ToolDef  // new: precomputed
}

func (a *Agent) toolDefs() []ToolDef {
    if a.cachedToolDefs == nil {
        a.cachedToolDefs = a.computeToolDefs()
    }
    return a.cachedToolDefs
}

func (a *Agent) AddTool(t Tool) {
    a.tools[t.Name()] = t
    a.cachedToolDefs = nil  // invalidate
}
```

Cache is invalidated only when tools change (AddTool, WithMCPServer).

### Files
- Modify: `agent.go` — add cachedToolDefs field, update toolDefs(), AddTool()

---

## 3. Message Deduplication

### Problem
Same tool calls produce identical results across turns (e.g., grep same pattern, read same file). These duplicates inflate context.

### Design

**Content-hash based deduplication** applied before sending to LLM:

```go
// dedup.go
type MessageDeduplicator struct {
    seen map[uint64]int  // content hash -> first message index
}

func (d *MessageDeduplicator) Process(messages []Message) []Message
```

**Rules**:
- Only dedup tool result messages (ToolResultContent)
- Hash the tool result content
- If same hash seen before, replace with: `[Same result as earlier tool call, omitted to save tokens]`
- Keep the original index reference so LLM knows which call it refers to
- Never dedup user or assistant messages

**Application point**: In `session.step()`, before passing messages to provider:

```go
func (s *Session) step(ctx context.Context) (*ChatResponse, error) {
    messages := s.memory.Messages()
    if s.dedup != nil {
        messages = s.dedup.Process(messages)
    }
    // ... send to provider
}
```

**Session integration**:
```go
type Session struct {
    agent        *Agent
    memory       Memory
    outputBuffer *OutputBuffer
    dedup        *MessageDeduplicator  // new
}
```

Deduplicator is created per-session, reset on ClearMemory().

### Files
- Create: `dedup.go` (~60 lines)
- Modify: `session.go` — add dedup field, apply in step()

---

## Implementation Order

1. **Tool definition caching** — smallest change, immediate benefit, no risk
2. **OutputBuffer + pagination** — largest change, highest token savings
3. **Message deduplication** — moderate change, complements pagination

Each gets its own commit. All tests must pass after each step.

---

## Files Summary

| File | Action | Description |
|------|--------|-------------|
| `output_buffer.go` | Create | OutputBuffer with LRU eviction |
| `dedup.go` | Create | MessageDeduplicator with content hashing |
| `agent.go` | Modify | Add cachedToolDefs, invalidation logic |
| `session.go` | Modify | Add outputBuffer + dedup, wire into step() |
| `tool/read_file.go` | Modify | Add offset/limit pagination |
| `tool/shell.go` | Modify | Add offset/limit pagination |
| `tool/grep.go` | Modify | Add offset/limit pagination |
| `tool/list_files.go` | Modify | Add offset/limit pagination |
| `output_buffer_test.go` | Create | Buffer store/get/eviction tests |
| `dedup_test.go` | Create | Dedup detection/replacement tests |

---

## Verification Plan

1. **Tool definition cache**: Verify toolDefs() returns same slice on repeated calls, invalidates on AddTool()
2. **OutputBuffer**: Store 5000 lines, GetPage(0, 200) returns first page, GetPage(200, 200) returns second page
3. **Pagination**: read_file large file → returns 200 lines + "Total: N. Next: offset=200"
4. **Shell pagination**: shell "seq 1000" → returns first 200 + pagination hint
5. **Dedup**: Two identical grep calls → second result replaced with reference
6. **Integration**: Run SWE-bench case, verify fewer tokens used
7. **Build**: `go build ./...` + `go vet ./...` + `go test ./...` all pass
