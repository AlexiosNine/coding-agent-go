# GoAgent Runtime Improvements — Design Spec

**Date**: 2026-04-14
**Approach**: Incremental improvements (4 independent changes, each independently mergeable)
**Estimated scope**: ~610 new lines, 4 new files, 8 modified files

---

## Context

The goagent runtime has a working MVP with a ReAct agent loop, dual LLM providers (Anthropic + OpenAI), built-in tools, and a CLI. Four improvements are needed to make it production-ready:

1. **Conversation isolation** — `Run()` mutates shared memory, making multi-conversation use unsafe
2. **Concurrent tool execution** — tools execute serially, wasting time when LLM returns multiple tool calls
3. **Structured errors + retry** — provider errors are opaque strings with no retry logic
4. **Streaming** — no way to stream LLM output token-by-token

---

## 1. Conversation Isolation (Session)

### Problem
`Agent.Run()` directly appends to `a.memory`. Multiple calls accumulate history. Separate conversations require manual `ClearMemory()`. No way to run two conversations on the same Agent concurrently.

### Design

**New file: `session.go` (~120 lines)**

```go
// Session holds the state for a single conversation.
type Session struct {
    agent  *Agent
    memory Memory
}

// NewSession creates a new conversation session.
func (a *Agent) NewSession() *Session {
    return &Session{
        agent:  a,
        memory: NewBufferMemory(), // or clone agent's memory factory
    }
}

// Run executes the agent loop within this session's conversation context.
func (s *Session) Run(ctx context.Context, input string) (*RunResult, error)

// Messages returns the session's conversation history.
func (s *Session) Messages() []Message

// ClearMemory resets the session's conversation history.
func (s *Session) ClearMemory()
```

**Changes to `agent.go`:**
- Move the current `Run()` loop logic into a private `run(ctx, input, memory)` method
- `Agent.Run()` becomes a convenience method that creates a temporary session internally (backward compatible)
- Remove `a.memory` field from Agent; Agent becomes stateless

**Changes to `options.go`:**
- Remove `WithMemory(m Memory)` — replaced by `WithMemoryFactory(func() Memory)`
- `WithMemoryFactory` sets the factory used by `NewSession()` to create per-session memory
- Default factory: `func() Memory { return NewBufferMemory() }`

**Changes to `cmd/agent/main.go`:**
- REPL mode uses `agent.NewSession()` to maintain conversation across turns
- Single-shot mode uses `agent.Run()` (unchanged)

### Backward Compatibility
- `Agent.Run()` continues to work as before for single-shot use
- `Agent.ClearMemory()` and `Agent.Messages()` are removed (use Session instead)
- This is a breaking change for users who rely on `Agent.ClearMemory()` / `Agent.Messages()`

---

## 2. Concurrent Tool Execution

### Problem
`executeTools` iterates tool calls serially. If the LLM returns 3 tool calls, they run one after another.

### Design

**Changes to `agent.go` (~40 lines changed):**

```go
func (s *Session) executeTools(ctx context.Context, toolUses []ToolUseContent) ([]ToolResultContent, error) {
    results := make([]ToolResultContent, len(toolUses))
    g, ctx := errgroup.WithContext(ctx)

    for i, tu := range toolUses {
        g.Go(func() error {
            results[i] = s.executeSingleTool(ctx, tu)
            return nil // errors captured in ToolResultContent.IsError
        })
    }
    _ = g.Wait()
    return results, nil
}

func (s *Session) executeSingleTool(ctx context.Context, tu ToolUseContent) ToolResultContent {
    // Current single-tool logic (lookup, before hook, execute, after hook)
    // Returns ToolResultContent with IsError=true on failure
}
```

**New dependency**: `golang.org/x/sync/errgroup` in `go.mod`

**New option in `options.go`:**
```go
func WithMaxConcurrency(n int) Option  // limits parallel tool executions, default unlimited
```

When `maxConcurrency` is set, use `errgroup` with `SetLimit(n)`.

### Key decisions
- Tool errors do NOT cancel other tools — each tool result is independent
- Results maintain original order via indexed slice
- Hooks (`BeforeToolCall`, `AfterToolCall`) are called per-tool, potentially concurrently — document this

---

## 3. Structured Errors + Retry

### Problem
Provider errors are `fmt.Errorf` strings. No way to distinguish rate limits from auth failures. No automatic retry.

### Design

**New file: `retry.go` (~80 lines)**

```go
// RetryConfig controls automatic retry behavior.
type RetryConfig struct {
    MaxRetries int           // default 3
    InitDelay  time.Duration // default 1s
    MaxDelay   time.Duration // default 30s
    Retryable  func(error) bool // custom retry predicate
}

// retry executes fn with exponential backoff.
func retry(ctx context.Context, cfg RetryConfig, fn func() error) error
```

**Changes to `errors.go` (~70 lines added):**

```go
// ProviderError contains structured API error information.
type ProviderError struct {
    Provider   string        // "anthropic", "openai"
    StatusCode int           // HTTP status code
    Type       string        // "rate_limit", "auth", "server", "overloaded"
    Message    string        // human-readable error message
    Retryable  bool          // whether this error can be retried
    RetryAfter time.Duration // suggested wait time (from Retry-After header)
    Err        error         // underlying error
}

func (e *ProviderError) Error() string
func (e *ProviderError) Unwrap() error
func IsRetryable(err error) bool
func IsRateLimited(err error) bool
```

**Changes to providers (`provider/anthropic/anthropic.go`, `provider/openai/openai.go`):**
- Parse HTTP status codes into `ProviderError`
- Map 429 → `Retryable: true, Type: "rate_limit"`
- Map 500/503 → `Retryable: true, Type: "server"`
- Map 401/403 → `Retryable: false, Type: "auth"`
- Parse `Retry-After` header when present

**Changes to `agent.go`:**
- `step()` wraps provider call with `retry()` when `RetryConfig` is set
- Default: 3 retries, 1s initial delay, exponential backoff

**New option in `options.go`:**
```go
func WithRetry(cfg RetryConfig) Option
func WithMaxRetries(n int) Option  // shorthand for common case
```

---

## 4. Streaming

### Problem
Provider only has `Chat()` (synchronous). No way to stream tokens to the user in real-time.

### Design

**New file: `stream.go` (~200 lines)**

```go
// StreamEvent represents a single event in a streaming response.
type StreamEvent struct {
    Type     string          // "text_delta", "tool_use", "message_stop", "error"
    Text     string          // for text_delta events
    ToolUse  *ToolUseContent // for tool_use events (complete, after accumulation)
    Error    error           // for error events
}

// StreamReader reads streaming events using an iterator pattern.
type StreamReader struct {
    events chan StreamEvent
    err    error
    done   chan struct{}
}

func (r *StreamReader) Next() (StreamEvent, error)  // returns io.EOF when done
func (r *StreamReader) Close() error                  // cancels the stream
func (r *StreamReader) Collect() (*ChatResponse, error) // reads all events into a ChatResponse
```

**Changes to `provider.go`:**

```go
type Provider interface {
    Chat(ctx context.Context, params ChatParams) (*ChatResponse, error)
    // Stream is optional. Providers that don't support streaming return ErrStreamNotSupported.
    Stream(ctx context.Context, params ChatParams) (*StreamReader, error)
}
```

Since adding `Stream()` to the interface is breaking, use an optional interface instead:

```go
// StreamProvider is optionally implemented by providers that support streaming.
type StreamProvider interface {
    Provider
    Stream(ctx context.Context, params ChatParams) (*StreamReader, error)
}
```

**Changes to providers:**
- Anthropic: implement SSE parsing for `/messages` with `stream: true`
- OpenAI: implement SSE parsing for `/chat/completions` with `stream: true`
- Both accumulate tool_use deltas into complete ToolUseContent before emitting

**New in `session.go`:**

```go
// RunStream executes the agent loop with streaming output.
// Returns a channel of events. Tool execution happens between stream segments.
func (s *Session) RunStream(ctx context.Context, input string) (<-chan StreamEvent, error)
```

**Changes to `cmd/agent/main.go`:**
- REPL mode uses `RunStream()` to print tokens as they arrive
- Single-shot mode continues using `Run()`

### Key decisions
- `StreamProvider` is an optional interface — providers that only implement `Chat()` still work
- `RunStream()` handles the full agent loop: stream LLM → detect tool_use → execute tools → stream again
- Tool execution is NOT streamed (tools return complete results)
- `StreamReader.Collect()` allows callers to fall back to non-streaming behavior

---

## Implementation Order

1. **Session isolation** — foundational, other changes build on it
2. **Concurrent tool execution** — small change, quick win
3. **Structured errors + retry** — independent of streaming
4. **Streaming** — largest change, depends on session being in place

Each improvement gets its own commit. All tests must pass after each step.

---

## Files Changed Summary

| File | Action | Changes |
|------|--------|---------|
| `session.go` | **New** | Session type, Run(), NewSession() |
| `stream.go` | **New** | StreamEvent, StreamReader, RunStream() |
| `retry.go` | **New** | RetryConfig, retry() with exponential backoff |
| `agent.go` | Modify | Extract run logic to private method, remove memory state, concurrent tools |
| `errors.go` | Modify | Add ProviderError, IsRetryable(), IsRateLimited() |
| `provider.go` | Modify | Add StreamProvider interface, StreamReader |
| `options.go` | Modify | Add WithRetry, WithMaxRetries, WithMaxConcurrency, WithMemoryFactory |
| `provider/anthropic/anthropic.go` | Modify | Return ProviderError, implement Stream() |
| `provider/anthropic/convert.go` | Modify | Add SSE parsing helpers |
| `provider/openai/openai.go` | Modify | Return ProviderError, implement Stream() |
| `provider/openai/convert.go` | Modify | Add SSE parsing helpers |
| `cmd/agent/main.go` | Modify | Use Session for REPL, streaming output |
| `agent_test.go` | Modify | Add session tests, concurrent tool tests, retry tests |
| `go.mod` | Modify | Add `golang.org/x/sync` dependency |

---

## Verification Plan

1. **Session tests**: Create two sessions on same agent, verify independent message histories
2. **Concurrent tool tests**: Mock provider returns 3 tool calls, verify all execute and results are ordered
3. **Retry tests**: Mock provider returns 429 twice then 200, verify retry succeeds
4. **ProviderError tests**: Verify error type detection (IsRetryable, IsRateLimited)
5. **Stream tests**: Mock provider with StreamReader, verify event sequence
6. **Integration**: `go build ./...` + `go vet ./...` + `go test ./...` all pass
7. **CLI manual test**: Run REPL with streaming, verify token-by-token output
