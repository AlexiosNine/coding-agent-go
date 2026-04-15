# Token Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce token consumption 30-50% by caching tool definitions, paginating large tool outputs with a session-scoped buffer, and deduplicating repeated tool results before provider calls.

**Architecture:** This plan adds one Agent-level optimization (cached tool definitions) and two Session-level optimizations (OutputBuffer and MessageDeduplicator). Large outputs stop flowing directly into Memory; instead, Memory stores only the current page and pagination hints, while a session-local buffer retains full content for later page requests. Deduplication runs just before provider calls so stored history stays lossless while transmitted history is token-efficient.

**Tech Stack:** Go 1.25, existing functional options + session architecture, no new third-party dependencies.

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `output_buffer.go` | Create | Session-scoped full output cache with page slicing and bounded size |
| `dedup.go` | Create | Content-hash-based tool result deduplicator |
| `agent.go` | Modify | Add cachedToolDefs and invalidation logic |
| `session.go` | Modify | Create outputBuffer/dedup on session start; pass buffer in context; dedup before provider calls |
| `tool/read_file.go` | Modify | Add offset/limit pagination; support reading from OutputBuffer when paginating prior output |
| `tool/shell.go` | Modify | Add offset/limit pagination; store full output in OutputBuffer |
| `tool/grep.go` | Modify | Add offset/limit pagination over matches; store full match list in OutputBuffer |
| `tool/list_files.go` | Modify | Add offset/limit pagination over entries; store full entry list in OutputBuffer |
| `output_buffer_test.go` | Create | Store/get page/eviction tests |
| `dedup_test.go` | Create | Duplicate tool result detection/replacement tests |

---

### Task 1: Cache Tool Definitions

**Files:**
- Modify: `agent.go:15-31, 41-53, 69-93`
- Test: `agent_test.go`

- [ ] **Step 1: Write the failing test**

Append to `agent_test.go`:

```go
func TestAgent_ToolDefs_CachedAndInvalidated(t *testing.T) {
	tool1 := cc.NewFuncTool("echo", "Echo input", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return in.Text, nil
	})

	a := cc.New(cc.WithTools(tool1))

	first := a.DebugToolDefsForTest()
	second := a.DebugToolDefsForTest()
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 tool def, got %d and %d", len(first), len(second))
	}
	if &first[0] != &second[0] {
		t.Fatal("expected cached tool defs to reuse same backing slice")
	}

	tool2 := cc.NewFuncTool("upper", "Uppercase input", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return strings.ToUpper(in.Text), nil
	})
	a.AddTool(tool2)

	third := a.DebugToolDefsForTest()
	if len(third) != 2 {
		t.Fatalf("expected 2 tool defs after invalidation, got %d", len(third))
	}
}
```

Add `"strings"` to the imports in `agent_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestAgent_ToolDefs_CachedAndInvalidated -v`
Expected: FAIL with `a.DebugToolDefsForTest undefined`

- [ ] **Step 3: Implement cached tool definitions**

In `agent.go`, add the cached field to `Agent`:

```go	type Agent struct {
		provider            Provider
		tools               map[string]Tool
		cachedToolDefs      []ToolDef
		system              string
		model               string
		maxTurns            int
		maxTokens           int
		maxConcurrency      int
		maxExplorationTurns int
		retry               *RetryConfig
		hooks               Hooks
		memoryFactory       func() Memory
		approver            Approver
		sandbox             *Sandbox
		osSandbox           *OSSandbox
		closers             []io.Closer
	}
```

Replace `toolDefs()` and `AddTool()` in `agent.go` with:

```go
func (a *Agent) AddTool(t Tool) {
	a.tools[t.Name()] = t
	a.cachedToolDefs = nil
}

func (a *Agent) toolDefs() []ToolDef {
	if len(a.tools) == 0 {
		return nil
	}
	if a.cachedToolDefs == nil {
		defs := make([]ToolDef, 0, len(a.tools))
		for _, t := range a.tools {
			defs = append(defs, ToolDef{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
			})
		}
		a.cachedToolDefs = defs
	}
	return a.cachedToolDefs
}

// DebugToolDefsForTest exposes cached tool defs for tests only.
func (a *Agent) DebugToolDefsForTest() []ToolDef {
	return a.toolDefs()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestAgent_ToolDefs_CachedAndInvalidated -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add agent.go agent_test.go
git commit -m "perf: cache tool definitions between turns"
```

---

### Task 2: Add Session OutputBuffer

**Files:**
- Create: `output_buffer.go`
- Modify: `session.go:13-17, 55-60, 85-86, 147-151`
- Test: `output_buffer_test.go`

- [ ] **Step 6: Write the failing test**

Create `output_buffer_test.go`:

```go
package cc_test

import (
	"strings"
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestOutputBuffer_StoreAndPaginate(t *testing.T) {
	buf := cc.NewOutputBuffer(1 << 20)
	content := strings.Join([]string{"a", "b", "c", "d", "e"}, "\n")
	buf.Store("tool-1", content)

	page, total, hasMore := buf.GetPage("tool-1", 0, 2)
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if !hasMore {
		t.Fatal("expected hasMore=true for first page")
	}
	if page != "a\nb" {
		t.Fatalf("expected first page 'a\\nb', got %q", page)
	}

	page, total, hasMore = buf.GetPage("tool-1", 2, 2)
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if !hasMore {
		t.Fatal("expected hasMore=true for middle page")
	}
	if page != "c\nd" {
		t.Fatalf("expected middle page 'c\\nd', got %q", page)
	}
}

func TestOutputBuffer_EvictsWhenOverSize(t *testing.T) {
	buf := cc.NewOutputBuffer(20)
	buf.Store("tool-1", "1234567890")
	buf.Store("tool-2", "abcdefghij")
	buf.Store("tool-3", "zzzzzzzzzz")

	if _, _, ok := buf.TryGetPage("tool-1", 0, 10); ok {
		t.Fatal("expected oldest entry to be evicted")
	}
}
```

- [ ] **Step 7: Run test to verify it fails**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestOutputBuffer -v`
Expected: FAIL with `undefined: cc.NewOutputBuffer`

- [ ] **Step 8: Implement OutputBuffer**

Create `output_buffer.go`:

```go
package cc

import (
	"strings"
	"sync"
	"time"
)

type BufferEntry struct {
	Lines     []string
	CreatedAt time.Time
	Size      int64
}

type OutputBuffer struct {
	mu      sync.RWMutex
	entries map[string]*BufferEntry
	order   []string
	maxSize int64
	size    int64
}

func NewOutputBuffer(maxSize int64) *OutputBuffer {
	if maxSize <= 0 {
		maxSize = 50 << 20
	}
	return &OutputBuffer{entries: make(map[string]*BufferEntry), maxSize: maxSize}
}

func (b *OutputBuffer) Store(id string, content string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Split(content, "\n")
	size := int64(len(content))
	if old, ok := b.entries[id]; ok {
		b.size -= old.Size
	}
	b.entries[id] = &BufferEntry{Lines: lines, CreatedAt: time.Now(), Size: size}
	b.order = append(b.order, id)
	b.size += size
	for b.size > b.maxSize && len(b.order) > 0 {
		oldest := b.order[0]
		b.order = b.order[1:]
		if entry, ok := b.entries[oldest]; ok {
			b.size -= entry.Size
			delete(b.entries, oldest)
		}
	}
}

func (b *OutputBuffer) GetPage(id string, offset, limit int) (string, int, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry := b.entries[id]
	if entry == nil {
		return "", 0, false
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 200
	}
	total := len(entry.Lines)
	if offset >= total {
		return "", total, false
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return strings.Join(entry.Lines[offset:end], "\n"), total, end < total
}

func (b *OutputBuffer) TryGetPage(id string, offset, limit int) (string, int, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry := b.entries[id]
	if entry == nil {
		return "", 0, false
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 200
	}
	total := len(entry.Lines)
	if offset >= total {
		return "", total, false
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return strings.Join(entry.Lines[offset:end], "\n"), total, true
}
```

- [ ] **Step 9: Wire OutputBuffer into Session**

In `session.go`, change `Session` to:

```go
type Session struct {
	agent          *Agent
	memory         Memory
	outputBuffer   *OutputBuffer
	dedup          *MessageDeduplicator
	systemOverride string
}
```

In `agent.go`, change `NewSession()` to:

```go
func (a *Agent) NewSession() *Session {
	return &Session{
		agent:        a,
		memory:       a.memoryFactory(),
		outputBuffer: NewOutputBuffer(50 << 20),
		dedup:        NewMessageDeduplicator(),
	}
}
```

In `session.go`, just before `tool.Execute(ctx, tu.Input)` in `executeSingleTool()`, add:

```go
ctx = WithOutputBuffer(ctx, s.outputBuffer)
```

Also add the context helpers at the bottom of `output_buffer.go`:

```go
type outputBufferKey struct{}

func WithOutputBuffer(ctx context.Context, buf *OutputBuffer) context.Context {
	return context.WithValue(ctx, outputBufferKey{}, buf)
}

func GetOutputBuffer(ctx context.Context) *OutputBuffer {
	if v := ctx.Value(outputBufferKey{}); v != nil {
		return v.(*OutputBuffer)
	}
	return nil
}
```

Add `"context"` to `output_buffer.go` imports.

- [ ] **Step 10: Run tests to verify they pass**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestOutputBuffer -v`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add output_buffer.go output_buffer_test.go agent.go session.go
git commit -m "perf: add session-scoped OutputBuffer for paginated tool results"
```

---

### Task 3: Add Pagination to read_file and shell

**Files:**
- Modify: `tool/read_file.go`
- Modify: `tool/shell.go`
- Test: `agent_test.go`

- [ ] **Step 12: Write failing pagination tests**

Append to `agent_test.go`:

```go
func TestReadFile_PaginationUsesOffsetAndLimit(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big.txt")
	content := strings.Join([]string{"l1", "l2", "l3", "l4", "l5"}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := tool.ReadFile()

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"`+path+`","offset":1,"limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "l2\nl3") {
		t.Fatalf("expected paginated output with l2/l3, got %q", out)
	}
	if !strings.Contains(out, "Total:") {
		t.Fatalf("expected pagination metadata, got %q", out)
	}
}
```

Add imports to `agent_test.go`: `"os"`, `"path/filepath"`, and `"github.com/alexioschen/cc-connect/goagent/tool"`.

- [ ] **Step 13: Run test to verify it fails**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestReadFile_PaginationUsesOffsetAndLimit -v`
Expected: FAIL because `offset` and `limit` are ignored / unsupported

- [ ] **Step 14: Implement paginated read_file**

In `tool/read_file.go`, change `readFileInput` to:

```go
type readFileInput struct {
	Path      string `json:"path" desc:"The file path to read"`
	StartLine int    `json:"start_line,omitempty" desc:"Optional: start line number (1-indexed)"`
	EndLine   int    `json:"end_line,omitempty" desc:"Optional: end line number (inclusive)"`
	Offset    int    `json:"offset,omitempty" desc:"Optional: skip first N lines (pagination)"`
	Limit     int    `json:"limit,omitempty" desc:"Optional: max lines to return (default 200)"`
}
```

Then update the implementation inside `ReadFile()` to:

```go
		buf := cc.GetOutputBuffer(ctx)
		if in.Offset > 0 && buf != nil {
			page, total, ok := buf.TryGetPage("read_file:"+in.Path, in.Offset, in.Limit)
			if ok {
				return fmt.Sprintf("%s\n---\nTotal: %d lines. Next offset: %d", page, total, in.Offset+in.Limit), nil
			}
		}

		data, err := os.ReadFile(in.Path)
		if err != nil {
			return "", fmt.Errorf("read file %s: %w", in.Path, err)
		}
		lines := strings.Split(string(data), "\n")
		if in.StartLine == 0 && in.EndLine == 0 {
			offset := in.Offset
			limit := in.Limit
			if limit <= 0 {
				limit = 200
			}
			if buf != nil {
				buf.Store("read_file:"+in.Path, string(data))
			}
			end := offset + limit
			if end > len(lines) {
				end = len(lines)
			}
			page := strings.Join(lines[offset:end], "\n")
			if end < len(lines) {
				return fmt.Sprintf("%s\n---\nTotal: %d lines. Next offset: %d", page, len(lines), end), nil
			}
			return page, nil
		}
```

Leave the existing `start_line` / `end_line` logic intact below this path.

- [ ] **Step 15: Implement paginated shell output**

In `tool/shell.go`, change `shellInput` to:

```go
type shellInput struct {
	Command string `json:"command" desc:"The shell command to execute"`
	Timeout int    `json:"timeout" desc:"Timeout in seconds (default 30)"`
	Offset  int    `json:"offset,omitempty" desc:"Optional: skip first N lines of prior output"`
	Limit   int    `json:"limit,omitempty" desc:"Optional: max lines to return (default 200)"`
}
```

Inside `Shell()`, before executing the command, add:

```go
		buf := cc.GetOutputBuffer(ctx)
		key := "shell:" + in.Command
		if in.Offset > 0 && buf != nil {
			page, total, ok := buf.TryGetPage(key, in.Offset, in.Limit)
			if ok {
				return fmt.Sprintf("%s\n---\nTotal: %d lines. Next offset: %d", page, total, in.Offset+in.Limit), nil
			}
		}
```

After `result := strings.TrimSpace(string(out))`, add:

```go
		if buf != nil {
			buf.Store(key, result)
		}
		lines := strings.Split(result, "\n")
		offset := in.Offset
		limit := in.Limit
		if limit <= 0 {
			limit = 200
		}
		if len(lines) > limit && offset == 0 {
			page := strings.Join(lines[:limit], "\n")
			return fmt.Sprintf("%s\n---\nTotal: %d lines. Next offset: %d", page, len(lines), limit), nil
		}
```

Keep the current error handling after this block.

- [ ] **Step 16: Run tests to verify they pass**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestReadFile_PaginationUsesOffsetAndLimit -v`
Expected: PASS

- [ ] **Step 17: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add tool/read_file.go tool/shell.go agent_test.go
git commit -m "perf: add paginated read_file and shell outputs"
```

---

### Task 4: Add Pagination to grep and list_files

**Files:**
- Modify: `tool/grep.go`
- Modify: `tool/list_files.go`
- Test: `agent_test.go`

- [ ] **Step 18: Write failing grep pagination test**

Append to `agent_test.go`:

```go
func TestGrep_PaginationIncludesMetadata(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "search.txt")
	content := strings.Join([]string{"error one", "ok", "error two", "ok", "error three"}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	g := tool.Grep()
	out, err := g.Execute(context.Background(), json.RawMessage(`{"pattern":"error","path":"`+path+`","limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Total:") {
		t.Fatalf("expected pagination metadata, got %q", out)
	}
}
```

- [ ] **Step 19: Run test to verify it fails**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestGrep_PaginationIncludesMetadata -v`
Expected: FAIL because grep ignores `limit`

- [ ] **Step 20: Add offset/limit to grep**

In `tool/grep.go`, change `grepInput` to:

```go
type grepInput struct {
	Pattern    string `json:"pattern" desc:"Search pattern (regex supported)"`
	Path       string `json:"path" desc:"File or directory to search in"`
	Recursive  bool   `json:"recursive" desc:"Search recursively in directories"`
	MaxResults int    `json:"max_results" desc:"Maximum number of matches to scan (default 1000)"`
	Offset     int    `json:"offset,omitempty" desc:"Optional: skip first N matches"`
	Limit      int    `json:"limit,omitempty" desc:"Optional: max matches to return (default 50)"`
}
```

Inside `Grep()`, replace the current maxResults handling with:

```go
		maxResults := input.MaxResults
		if maxResults <= 0 {
			maxResults = 1000
		}
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		offset := input.Offset
```

After collecting all matches into a `[]string matches`, paginate them:

```go
		if len(matches) == 0 {
			return fmt.Sprintf("No matches found for %q in %s", input.Pattern, absPath), nil
		}
		end := offset + limit
		if end > len(matches) {
			end = len(matches)
		}
		page := strings.Join(matches[offset:end], "\n")
		header := fmt.Sprintf("Found %d match(es) for %q:\n\n", len(matches), input.Pattern)
		if end < len(matches) {
			return header + page + fmt.Sprintf("\n---\nTotal: %d matches. Next offset: %d", len(matches), end), nil
		}
		return header + page, nil
```

Update `searchFile()` to append matches into a slice rather than writing to a strings.Builder.

- [ ] **Step 21: Add offset/limit to list_files**

In `tool/list_files.go`, change `listFilesInput` to:

```go
type listFilesInput struct {
	Path       string `json:"path" desc:"Directory path to list (default: current directory)"`
	Recursive  bool   `json:"recursive" desc:"List files recursively"`
	ShowHidden bool   `json:"show_hidden" desc:"Include hidden files (starting with .)"`
	Offset     int    `json:"offset,omitempty" desc:"Optional: skip first N entries"`
	Limit      int    `json:"limit,omitempty" desc:"Optional: max entries to return (default 100)"`
}
```

Collect entries into `[]string items`, then paginate:

```go
		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		offset := input.Offset
		end := offset + limit
		if end > len(items) {
			end = len(items)
		}
		page := strings.Join(items[offset:end], "\n")
		result.WriteString(page)
		if end < len(items) {
			result.WriteString(fmt.Sprintf("\n---\nTotal: %d entries. Next offset: %d", len(items), end))
		}
```

- [ ] **Step 22: Run tests to verify they pass**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run "TestGrep_PaginationIncludesMetadata" -v`
Expected: PASS

- [ ] **Step 23: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add tool/grep.go tool/list_files.go agent_test.go
git commit -m "perf: add paginated grep and list_files outputs"
```

---

### Task 5: Add Message Deduplication

**Files:**
- Create: `dedup.go`
- Modify: `session.go`
- Test: `dedup_test.go`

- [ ] **Step 24: Write failing dedup tests**

Create `dedup_test.go`:

```go
package cc_test

import (
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestMessageDeduplicator_ReplacesDuplicateToolResults(t *testing.T) {
	d := cc.NewMessageDeduplicator()
	messages := []cc.Message{
		cc.NewUserMessage("find errors"),
		cc.NewToolResultMessage(cc.ToolResultContent{ToolUseID: "a1", Content: "same output"}),
		cc.NewToolResultMessage(cc.ToolResultContent{ToolUseID: "a2", Content: "same output"}),
	}
	out := d.Process(messages)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	if out[2].Text() == "same output" {
		t.Fatal("expected duplicate tool result to be replaced")
	}
}

func TestMessageDeduplicator_DoesNotTouchUserMessages(t *testing.T) {
	d := cc.NewMessageDeduplicator()
	messages := []cc.Message{
		cc.NewUserMessage("same"),
		cc.NewUserMessage("same"),
	}
	out := d.Process(messages)
	if out[0].Text() != "same" || out[1].Text() != "same" {
		t.Fatal("expected user messages to be unchanged")
	}
}
```

- [ ] **Step 25: Run test to verify it fails**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestMessageDeduplicator -v`
Expected: FAIL with `undefined: cc.NewMessageDeduplicator`

- [ ] **Step 26: Implement MessageDeduplicator**

Create `dedup.go`:

```go
package cc

import (
	"fmt"
	"hash/fnv"
)

type MessageDeduplicator struct {
	seen map[uint64]int
}

func NewMessageDeduplicator() *MessageDeduplicator {
	return &MessageDeduplicator{seen: make(map[uint64]int)}
}

func (d *MessageDeduplicator) Process(messages []Message) []Message {
	out := make([]Message, len(messages))
	copy(out, messages)
	for i, msg := range out {
		for j, content := range msg.Content {
			tr, ok := content.(ToolResultContent)
			if !ok {
				continue
			}
			h := hashString(tr.Content)
			if firstIdx, exists := d.seen[h]; exists {
				tr.Content = fmt.Sprintf("[Same result as earlier tool call near message #%d, omitted to save tokens]", firstIdx)
				out[i].Content[j] = tr
				continue
			}
			d.seen[h] = i
		}
	}
	return out
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
```

- [ ] **Step 27: Wire dedup into session.step()**

In `session.go`, change `step()` to start with:

```go
func (s *Session) step(ctx context.Context) (*ChatResponse, error) {
	system := s.agent.system
	if s.systemOverride != "" {
		system = s.systemOverride
	}
	messages := s.memory.Messages()
	if s.dedup != nil {
		messages = s.dedup.Process(messages)
	}
	params := ChatParams{
		Model:     s.agent.model,
		System:    system,
		Messages:  messages,
		Tools:     s.agent.toolDefs(),
		MaxTokens: s.agent.maxTokens,
	}
```

Also ensure `s.dedup` is initialized in `NewSession()` from Task 2.

- [ ] **Step 28: Run tests to verify they pass**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestMessageDeduplicator -v`
Expected: PASS

- [ ] **Step 29: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add dedup.go dedup_test.go session.go
git commit -m "perf: deduplicate repeated tool results before provider calls"
```

---

### Task 6: Full Verification

**Files:**
- Verify only

- [ ] **Step 30: Run all tests**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -count=1`
Expected: PASS all tests

- [ ] **Step 31: Run build and vet**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go build ./... && go vet ./...`
Expected: PASS with no errors

- [ ] **Step 32: Smoke test SWE-bench adapter**

Run:

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent/swebench/adapter
OPENAI_API_KEY="..." OPENAI_BASE_URL="..." LLM_MODEL="xopglm5" ./adapter /tmp/test_instance.json
```

Expected:
- Tool outputs show pagination metadata for large files / grep results
- No crashes
- Patch JSONL emitted on stdout or graceful exploration abort

- [ ] **Step 33: Commit verification-only adjustments if needed**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git status
# If verification exposed any tiny fixes, stage only those files
git add <files>
git commit -m "chore: finalize token optimization rollout"
```
