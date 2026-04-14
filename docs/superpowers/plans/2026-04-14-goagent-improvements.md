# GoAgent Runtime Improvements — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add session isolation, concurrent tool execution, structured errors with retry, and streaming to the goagent runtime.

**Architecture:** Four incremental improvements, each independently committable. Session isolation is foundational — the agent loop moves from Agent to Session. Concurrent tools use errgroup. Retry wraps the provider call with exponential backoff. Streaming adds an optional StreamProvider interface with SSE parsing.

**Tech Stack:** Go 1.24, golang.org/x/sync/errgroup

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `session.go` | Create | Session type, Run() loop, NewSession(), Messages(), ClearMemory() |
| `retry.go` | Create | RetryConfig, retry() with exponential backoff |
| `stream.go` | Create | StreamEvent, StreamReader, StreamProvider interface |
| `agent.go` | Modify | Remove memory/loop, keep New() + convenience Run(), add RunStream() delegation |
| `errors.go` | Modify | Add ProviderError, IsRetryable(), IsRateLimited(), ErrStreamNotSupported |
| `options.go` | Modify | Replace WithMemory → WithMemoryFactory, add WithMaxConcurrency, WithRetry, WithMaxRetries |
| `provider.go` | Modify | Add StreamProvider optional interface |
| `provider/anthropic/anthropic.go` | Modify | Return ProviderError, implement Stream() |
| `provider/anthropic/convert.go` | Modify | Add parseSSE(), error classification |
| `provider/openai/openai.go` | Modify | Return ProviderError, implement Stream() |
| `provider/openai/convert.go` | Modify | Add parseSSE(), error classification |
| `cmd/agent/main.go` | Modify | Use Session for REPL, add -stream flag |
| `agent_test.go` | Modify | Add session, concurrent, retry, stream tests |
| `go.mod` | Modify | Add golang.org/x/sync |

---

## Task 1: Session Isolation — Tests

**Files:**
- Modify: `agent_test.go`

- [ ] **Step 1: Write failing test for session isolation**

```go
func TestSession_IndependentMemory(t *testing.T) {
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "Reply A"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 5, OutputTokens: 3}},
			{Content: []cc.Content{cc.TextContent{Text: "Reply B"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 5, OutputTokens: 3}},
		},
	}

	agent := cc.New(cc.WithProvider(provider), cc.WithModel("test"))

	s1 := agent.NewSession()
	s2 := agent.NewSession()

	_, err := s1.Run(context.Background(), "Hello from S1")
	if err != nil {
		t.Fatalf("s1.Run: %v", err)
	}
	_, err = s2.Run(context.Background(), "Hello from S2")
	if err != nil {
		t.Fatalf("s2.Run: %v", err)
	}

	if len(s1.Messages()) != 2 {
		t.Errorf("s1 expected 2 messages, got %d", len(s1.Messages()))
	}
	if len(s2.Messages()) != 2 {
		t.Errorf("s2 expected 2 messages, got %d", len(s2.Messages()))
	}
	if s1.Messages()[0].Text() == s2.Messages()[0].Text() {
		t.Error("sessions should have independent messages")
	}
}

func TestSession_ClearMemory(t *testing.T) {
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "Hi"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 5, OutputTokens: 3}},
		},
	}
	agent := cc.New(cc.WithProvider(provider), cc.WithModel("test"))
	s := agent.NewSession()

	_, _ = s.Run(context.Background(), "Hello")
	if len(s.Messages()) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(s.Messages()))
	}

	s.ClearMemory()
	if len(s.Messages()) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(s.Messages()))
	}
}

func TestAgent_Run_BackwardCompatible(t *testing.T) {
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "OK"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 5, OutputTokens: 2}},
		},
	}
	agent := cc.New(cc.WithProvider(provider), cc.WithModel("test"))

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "OK" {
		t.Errorf("expected 'OK', got %q", result.Output)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run "TestSession|TestAgent_Run_BackwardCompatible" -v`
Expected: FAIL — `agent.NewSession` undefined

---

## Task 2: Session Isolation — Implementation

**Files:**
- Create: `session.go`
- Modify: `agent.go`
- Modify: `options.go`

- [ ] **Step 3: Create session.go**

```go
package cc

import (
	"context"
	"fmt"
)

// Session holds the state for a single conversation.
// Create via Agent.NewSession().
type Session struct {
	agent  *Agent
	memory Memory
}

// Run executes the agent loop within this session's conversation context.
func (s *Session) Run(ctx context.Context, input string) (*RunResult, error) {
	if input == "" {
		return nil, ErrEmptyInput
	}
	if s.agent.provider == nil {
		return nil, ErrNoProvider
	}

	s.memory.Add(NewUserMessage(input))

	var totalUsage Usage

	for turn := range s.agent.maxTurns {
		resp, err := s.step(ctx)
		if err != nil {
			return nil, fmt.Errorf("turn %d: %w", turn+1, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)

		if s.agent.hooks.OnModelResponse != nil {
			s.agent.hooks.OnModelResponse(ctx, resp)
		}

		s.memory.Add(Message{Role: RoleAssistant, Content: resp.Content})

		toolUses := resp.ToolUses()
		if len(toolUses) == 0 {
			return &RunResult{
				Output:   resp.Text(),
				Messages: s.memory.Messages(),
				Turns:    turn + 1,
				Usage:    totalUsage,
			}, nil
		}

		results := s.executeTools(ctx, toolUses)
		s.memory.Add(NewToolResultMessage(results...))
	}

	return &RunResult{
		Output:   "Max turns exceeded",
		Messages: s.memory.Messages(),
		Turns:    s.agent.maxTurns,
		Usage:    totalUsage,
	}, ErrMaxTurns
}

// step makes a single LLM call with the current conversation state.
func (s *Session) step(ctx context.Context) (*ChatResponse, error) {
	return s.agent.provider.Chat(ctx, ChatParams{
		Model:     s.agent.model,
		System:    s.agent.system,
		Messages:  s.memory.Messages(),
		Tools:     s.agent.toolDefs(),
		MaxTokens: s.agent.maxTokens,
	})
}

// executeTools runs all tool calls from the LLM response.
func (s *Session) executeTools(ctx context.Context, toolUses []ToolUseContent) []ToolResultContent {
	results := make([]ToolResultContent, 0, len(toolUses))
	for _, tu := range toolUses {
		results = append(results, s.executeSingleTool(ctx, tu))
	}
	return results
}

// executeSingleTool runs a single tool call.
func (s *Session) executeSingleTool(ctx context.Context, tu ToolUseContent) ToolResultContent {
	tool, ok := s.agent.tools[tu.Name]
	if !ok {
		return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("tool %q not found", tu.Name), IsError: true}
	}

	if s.agent.hooks.BeforeToolCall != nil {
		if err := s.agent.hooks.BeforeToolCall(ctx, tu.Name, tu.Input); err != nil {
			return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("tool call blocked: %s", err.Error()), IsError: true}
		}
	}

	output, err := tool.Execute(ctx, tu.Input)

	if s.agent.hooks.AfterToolCall != nil {
		s.agent.hooks.AfterToolCall(ctx, tu.Name, output, err)
	}

	if err != nil {
		return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("error: %s", err.Error()), IsError: true}
	}
	return ToolResultContent{ToolUseID: tu.ID, Content: output}
}

// Messages returns the session's conversation history.
func (s *Session) Messages() []Message {
	return s.memory.Messages()
}

// ClearMemory resets the session's conversation history.
func (s *Session) ClearMemory() {
	s.memory.Clear()
}
```

- [ ] **Step 4: Rewrite agent.go — remove loop/memory, add NewSession(), keep convenience Run()**

Replace the entire content of `agent.go` with:

```go
package cc

import "context"

const (
	defaultMaxTurns  = 10
	defaultMaxTokens = 4096
)

// Agent is the core runtime that orchestrates LLM calls and tool execution.
// Agent itself is stateless — conversation state lives in Session.
type Agent struct {
	provider       Provider
	tools          map[string]Tool
	system         string
	model          string
	maxTurns       int
	maxTokens      int
	hooks          Hooks
	memoryFactory  func() Memory
}

// RunResult contains the outcome of an agent run.
type RunResult struct {
	Output   string
	Messages []Message
	Turns    int
	Usage    Usage
}

// New creates a new Agent with the given options.
func New(opts ...Option) *Agent {
	a := &Agent{
		tools:         make(map[string]Tool),
		maxTurns:      defaultMaxTurns,
		maxTokens:     defaultMaxTokens,
		memoryFactory: func() Memory { return NewBufferMemory() },
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// NewSession creates a new conversation session with independent memory.
func (a *Agent) NewSession() *Session {
	return &Session{
		agent:  a,
		memory: a.memoryFactory(),
	}
}

// Run is a convenience method for single-shot use.
// It creates a temporary session, runs the input, and returns the result.
func (a *Agent) Run(ctx context.Context, input string) (*RunResult, error) {
	return a.NewSession().Run(ctx, input)
}

// AddTool registers a tool with the agent.
func (a *Agent) AddTool(t Tool) {
	a.tools[t.Name()] = t
}

// SetSystem updates the system prompt.
func (a *Agent) SetSystem(system string) {
	a.system = system
}

// toolDefs returns the tool definitions for the LLM.
func (a *Agent) toolDefs() []ToolDef {
	if len(a.tools) == 0 {
		return nil
	}
	defs := make([]ToolDef, 0, len(a.tools))
	for _, t := range a.tools {
		defs = append(defs, ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}
```

- [ ] **Step 5: Update options.go — replace WithMemory with WithMemoryFactory**

Replace `WithMemory` at the bottom of `options.go` with:

```go
// WithMemoryFactory sets the factory function used to create per-session memory.
// Default creates an unbounded BufferMemory.
func WithMemoryFactory(f func() Memory) Option {
	return func(a *Agent) { a.memoryFactory = f }
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -v -count=1`
Expected: ALL PASS (including new session tests and existing tests)

- [ ] **Step 7: Update CLI to use Session for REPL**

In `cmd/agent/main.go`, change `runREPL` to create a session:

Replace lines 90-123 with:

```go
func runREPL(agent *cc.Agent) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	session := agent.NewSession()
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("cc-connect agent (type 'exit' to quit)")

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}
		if input == "/clear" {
			session.ClearMemory()
			fmt.Println("Memory cleared.")
			continue
		}

		result, err := session.Run(ctx, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			continue
		}
		fmt.Printf("\n%s\n", result.Output)
		fmt.Printf("[turns: %d | tokens: %d in / %d out]\n", result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
	}
}
```

- [ ] **Step 8: Build and vet**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go build ./... && go vet ./...`
Expected: No errors

- [ ] **Step 9: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add session.go agent.go options.go agent_test.go cmd/agent/main.go
git commit -m "feat: add Session for conversation isolation

- Session holds per-conversation memory, Agent is now stateless
- Agent.Run() preserved as convenience (creates temp session)
- CLI REPL uses session for multi-turn conversations
- Replace WithMemory with WithMemoryFactory"
```

---

## Task 3: Concurrent Tool Execution — Tests

**Files:**
- Modify: `agent_test.go`

- [ ] **Step 10: Write failing test for concurrent tool execution**

```go
func TestSession_ConcurrentToolExecution(t *testing.T) {
	// Provider returns 3 tool calls at once, then a text response
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content: []cc.Content{
					cc.ToolUseContent{ID: "c1", Name: "slow", Input: json.RawMessage(`{"id":"1"}`)},
					cc.ToolUseContent{ID: "c2", Name: "slow", Input: json.RawMessage(`{"id":"2"}`)},
					cc.ToolUseContent{ID: "c3", Name: "slow", Input: json.RawMessage(`{"id":"3"}`)},
				},
				StopReason: "tool_use",
				Usage:      cc.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{
				Content:    []cc.Content{cc.TextContent{Text: "All done"}},
				StopReason: "end_turn",
				Usage:      cc.Usage{InputTokens: 20, OutputTokens: 5},
			},
		},
	}

	var mu sync.Mutex
	var order []string

	slowTool := cc.NewFuncTool("slow", "Slow tool", func(_ context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		mu.Lock()
		order = append(order, in.ID)
		mu.Unlock()
		return "done-" + in.ID, nil
	})

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("test"),
		cc.WithTools(slowTool),
	)

	result, err := agent.NewSession().Run(context.Background(), "Run all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "All done" {
		t.Errorf("expected 'All done', got %q", result.Output)
	}
	// All 3 tools must have executed
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Errorf("expected 3 tool executions, got %d", len(order))
	}
}
```

Add `"sync"` to the import block of `agent_test.go`.

- [ ] **Step 11: Run test to verify it fails**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestSession_ConcurrentToolExecution -v`
Expected: FAIL — tools execute but test should pass since serial also works. This test validates correctness; concurrency is verified by the implementation change.

---

## Task 4: Concurrent Tool Execution — Implementation

**Files:**
- Modify: `session.go`
- Modify: `options.go`
- Modify: `go.mod`

- [ ] **Step 12: Add errgroup dependency**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go get golang.org/x/sync`

- [ ] **Step 13: Add WithMaxConcurrency option**

Append to `options.go` after `WithMemoryFactory`:

```go
// WithMaxConcurrency limits the number of tools that can execute in parallel.
// Default is 0 (unlimited).
func WithMaxConcurrency(n int) Option {
	return func(a *Agent) { a.maxConcurrency = n }
}
```

Add `maxConcurrency int` field to the Agent struct in `agent.go`:

```go
type Agent struct {
	provider       Provider
	tools          map[string]Tool
	system         string
	model          string
	maxTurns       int
	maxTokens      int
	maxConcurrency int
	hooks          Hooks
	memoryFactory  func() Memory
}
```

- [ ] **Step 14: Rewrite executeTools in session.go to use errgroup**

Replace the `executeTools` method in `session.go` with:

```go
// executeTools runs all tool calls concurrently.
func (s *Session) executeTools(ctx context.Context, toolUses []ToolUseContent) []ToolResultContent {
	results := make([]ToolResultContent, len(toolUses))

	g, ctx := errgroup.WithContext(ctx)
	if s.agent.maxConcurrency > 0 {
		g.SetLimit(s.agent.maxConcurrency)
	}

	for i, tu := range toolUses {
		g.Go(func() error {
			results[i] = s.executeSingleTool(ctx, tu)
			return nil
		})
	}
	_ = g.Wait()
	return results
}
```

Add `"golang.org/x/sync/errgroup"` to the imports in `session.go`.

- [ ] **Step 15: Run all tests**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -v -count=1`
Expected: ALL PASS

- [ ] **Step 16: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add session.go agent.go options.go agent_test.go go.mod go.sum
git commit -m "feat: concurrent tool execution with errgroup

- Tools execute in parallel via errgroup
- Results maintain original order
- WithMaxConcurrency(n) option to limit parallelism"
```

---

## Task 5: Structured Errors — Types and Helpers

**Files:**
- Modify: `errors.go`

- [ ] **Step 17: Add ProviderError type and helpers to errors.go**

Append after the existing `ToolError` type:

```go
// ErrStreamNotSupported is returned when streaming is requested but the provider doesn't support it.
var ErrStreamNotSupported = errors.New("provider: streaming not supported")

// ProviderError contains structured API error information.
type ProviderError struct {
	Provider   string
	StatusCode int
	Type       string // "rate_limit", "auth", "server", "overloaded"
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s: %s (status %d)", e.Provider, e.Message, e.StatusCode)
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// IsRetryable returns true if the error is a retryable ProviderError.
func IsRetryable(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable
	}
	return false
}

// IsRateLimited returns true if the error is a rate limit ProviderError.
func IsRateLimited(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Type == "rate_limit"
	}
	return false
}

// classifyHTTPStatus returns the error type and retryable flag for an HTTP status code.
func classifyHTTPStatus(status int) (errType string, retryable bool) {
	switch {
	case status == 429:
		return "rate_limit", true
	case status == 401 || status == 403:
		return "auth", false
	case status == 529:
		return "overloaded", true
	case status >= 500:
		return "server", true
	default:
		return "client", false
	}
}
```

Add `"fmt"` and `"time"` to the imports in `errors.go`.

- [ ] **Step 18: Run build to verify**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go build ./...`
Expected: No errors

---

## Task 6: Structured Errors — Tests

**Files:**
- Modify: `agent_test.go`

- [ ] **Step 19: Write tests for ProviderError helpers**

```go
func TestProviderError_IsRetryable(t *testing.T) {
	retryable := &cc.ProviderError{
		Provider: "test", StatusCode: 429, Type: "rate_limit",
		Message: "too many requests", Retryable: true,
	}
	if !cc.IsRetryable(retryable) {
		t.Error("expected 429 to be retryable")
	}
	if !cc.IsRateLimited(retryable) {
		t.Error("expected 429 to be rate limited")
	}

	notRetryable := &cc.ProviderError{
		Provider: "test", StatusCode: 401, Type: "auth",
		Message: "unauthorized", Retryable: false,
	}
	if cc.IsRetryable(notRetryable) {
		t.Error("expected 401 to not be retryable")
	}
	if cc.IsRateLimited(notRetryable) {
		t.Error("expected 401 to not be rate limited")
	}

	// Non-ProviderError
	if cc.IsRetryable(errors.New("random error")) {
		t.Error("expected non-ProviderError to not be retryable")
	}
}
```

- [ ] **Step 20: Run test**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run TestProviderError -v`
Expected: PASS

---

## Task 7: Retry Logic — Implementation and Tests

**Files:**
- Create: `retry.go`
- Modify: `options.go`
- Modify: `session.go`
- Modify: `agent_test.go`

- [ ] **Step 21: Write failing retry test**

```go
func TestSession_RetryOnRateLimit(t *testing.T) {
	callCount := 0
	provider := &errorThenSuccessProvider{
		errors: []error{
			&cc.ProviderError{Provider: "test", StatusCode: 429, Type: "rate_limit", Message: "rate limited", Retryable: true},
			&cc.ProviderError{Provider: "test", StatusCode: 503, Type: "server", Message: "unavailable", Retryable: true},
		},
		success: &cc.ChatResponse{
			Content: []cc.Content{cc.TextContent{Text: "Finally!"}}, StopReason: "end_turn",
			Usage: cc.Usage{InputTokens: 10, OutputTokens: 5},
		},
		callCount: &callCount,
	}

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("test"),
		cc.WithMaxRetries(3),
	)

	result, err := agent.Run(context.Background(), "retry me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Finally!" {
		t.Errorf("expected 'Finally!', got %q", result.Output)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (2 retries + 1 success), got %d", callCount)
	}
}

func TestSession_NoRetryOnAuthError(t *testing.T) {
	callCount := 0
	provider := &errorThenSuccessProvider{
		errors: []error{
			&cc.ProviderError{Provider: "test", StatusCode: 401, Type: "auth", Message: "unauthorized", Retryable: false},
		},
		success:   nil,
		callCount: &callCount,
	}

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("test"),
		cc.WithMaxRetries(3),
	)

	_, err := agent.Run(context.Background(), "fail me")
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (no retry for auth), got %d", callCount)
	}
}
```

Also add the helper mock to `agent_test.go`:

```go
// errorThenSuccessProvider returns errors for the first N calls, then succeeds.
type errorThenSuccessProvider struct {
	errors    []error
	success   *cc.ChatResponse
	callCount *int
}

func (p *errorThenSuccessProvider) Chat(_ context.Context, _ cc.ChatParams) (*cc.ChatResponse, error) {
	*p.callCount++
	idx := *p.callCount - 1
	if idx < len(p.errors) {
		return nil, p.errors[idx]
	}
	return p.success, nil
}
```

- [ ] **Step 22: Run tests to verify they fail**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run "TestSession_Retry|TestSession_NoRetry" -v`
Expected: FAIL — `WithMaxRetries` undefined

- [ ] **Step 23: Create retry.go**

```go
package cc

import (
	"context"
	"math"
	"time"
)

// RetryConfig controls automatic retry behavior for provider calls.
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts. Default 3.
	MaxRetries int
	// InitDelay is the initial delay before the first retry. Default 1s.
	InitDelay time.Duration
	// MaxDelay is the maximum delay between retries. Default 30s.
	MaxDelay time.Duration
}

// DefaultRetryConfig returns a RetryConfig with sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		InitDelay:  1 * time.Second,
		MaxDelay:   30 * time.Second,
	}
}

// retry executes fn with exponential backoff for retryable errors.
// It returns immediately for non-retryable errors.
func retry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	for attempt := range cfg.MaxRetries + 1 {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsRetryable(lastErr) {
			return lastErr
		}
		if attempt == cfg.MaxRetries {
			break
		}

		delay := cfg.delay(attempt)

		// Respect RetryAfter from ProviderError if larger
		var pe *ProviderError
		if errors.As(lastErr, &pe) && pe.RetryAfter > delay {
			delay = pe.RetryAfter
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

// delay calculates the backoff delay for a given attempt.
func (cfg RetryConfig) delay(attempt int) time.Duration {
	d := time.Duration(float64(cfg.InitDelay) * math.Pow(2, float64(attempt)))
	if d > cfg.MaxDelay {
		d = cfg.MaxDelay
	}
	return d
}
```

Add `"errors"` to the imports.

- [ ] **Step 24: Add retry options and field to Agent**

Add `retry *RetryConfig` field to Agent struct in `agent.go`:

```go
type Agent struct {
	provider       Provider
	tools          map[string]Tool
	system         string
	model          string
	maxTurns       int
	maxTokens      int
	maxConcurrency int
	retry          *RetryConfig
	hooks          Hooks
	memoryFactory  func() Memory
}
```

Append to `options.go`:

```go
// WithRetry sets the retry configuration for provider calls.
func WithRetry(cfg RetryConfig) Option {
	return func(a *Agent) { a.retry = &cfg }
}

// WithMaxRetries enables retry with the given max attempts and default delays.
func WithMaxRetries(n int) Option {
	return func(a *Agent) {
		cfg := DefaultRetryConfig()
		cfg.MaxRetries = n
		a.retry = &cfg
	}
}
```

- [ ] **Step 25: Integrate retry into Session.step()**

Replace the `step` method in `session.go`:

```go
// step makes a single LLM call, with retry if configured.
func (s *Session) step(ctx context.Context) (*ChatResponse, error) {
	params := ChatParams{
		Model:     s.agent.model,
		System:    s.agent.system,
		Messages:  s.memory.Messages(),
		Tools:     s.agent.toolDefs(),
		MaxTokens: s.agent.maxTokens,
	}

	if s.agent.retry == nil {
		return s.agent.provider.Chat(ctx, params)
	}

	var resp *ChatResponse
	err := retry(ctx, *s.agent.retry, func() error {
		var callErr error
		resp, callErr = s.agent.provider.Chat(ctx, params)
		return callErr
	})
	return resp, err
}
```

- [ ] **Step 26: Run all tests**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -v -count=1`
Expected: ALL PASS

Note: The retry tests use real `time.After` delays. Since the mock returns errors immediately and the test uses `WithMaxRetries(3)` with default 1s InitDelay, override the delay for tests. Actually, the retry function uses exponential backoff starting at 1s which is too slow for tests. We need to make the test use a short delay.

Update the retry tests to use `WithRetry` with short delays:

```go
func TestSession_RetryOnRateLimit(t *testing.T) {
	callCount := 0
	provider := &errorThenSuccessProvider{
		errors: []error{
			&cc.ProviderError{Provider: "test", StatusCode: 429, Type: "rate_limit", Message: "rate limited", Retryable: true},
			&cc.ProviderError{Provider: "test", StatusCode: 503, Type: "server", Message: "unavailable", Retryable: true},
		},
		success: &cc.ChatResponse{
			Content: []cc.Content{cc.TextContent{Text: "Finally!"}}, StopReason: "end_turn",
			Usage: cc.Usage{InputTokens: 10, OutputTokens: 5},
		},
		callCount: &callCount,
	}

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("test"),
		cc.WithRetry(cc.RetryConfig{MaxRetries: 3, InitDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}),
	)

	result, err := agent.Run(context.Background(), "retry me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Finally!" {
		t.Errorf("expected 'Finally!', got %q", result.Output)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestSession_NoRetryOnAuthError(t *testing.T) {
	callCount := 0
	provider := &errorThenSuccessProvider{
		errors: []error{
			&cc.ProviderError{Provider: "test", StatusCode: 401, Type: "auth", Message: "unauthorized", Retryable: false},
		},
		success:   nil,
		callCount: &callCount,
	}

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("test"),
		cc.WithRetry(cc.RetryConfig{MaxRetries: 3, InitDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}),
	)

	_, err := agent.Run(context.Background(), "fail me")
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}
```

Add `"time"` to the test imports.

- [ ] **Step 27: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add errors.go retry.go agent.go options.go session.go agent_test.go
git commit -m "feat: structured ProviderError with automatic retry

- ProviderError type with StatusCode, Type, Retryable fields
- IsRetryable() and IsRateLimited() helpers
- retry() with exponential backoff, respects RetryAfter
- WithRetry() and WithMaxRetries() options
- classifyHTTPStatus() for provider implementations"
```

---

## Task 8: Update Providers to Return ProviderError

**Files:**
- Modify: `provider/anthropic/anthropic.go`
- Modify: `provider/openai/openai.go`

- [ ] **Step 28: Update Anthropic provider error handling**

In `provider/anthropic/anthropic.go`, replace the error status check (lines 84-85):

```go
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d: %s", cc.ErrProviderRequest, resp.StatusCode, string(respBody))
	}
```

With:

```go
	if resp.StatusCode != http.StatusOK {
		errType, retryable := cc.ClassifyHTTPStatus(resp.StatusCode)
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, &cc.ProviderError{
			Provider:   "anthropic",
			StatusCode: resp.StatusCode,
			Type:       errType,
			Message:    string(respBody),
			Retryable:  retryable,
			RetryAfter: retryAfter,
			Err:        cc.ErrProviderRequest,
		}
	}
```

Add `parseRetryAfter` helper to `provider/anthropic/convert.go`:

```go
// parseRetryAfter parses the Retry-After header value.
func parseRetryAfter(val string) time.Duration {
	if val == "" {
		return 0
	}
	if secs, err := strconv.Atoi(val); err == nil {
		return time.Duration(secs) * time.Second
	}
	return 0
}
```

Add `"strconv"` and `"time"` to imports in `convert.go`.

Also rename `classifyHTTPStatus` to `ClassifyHTTPStatus` (exported) in `errors.go` so providers can use it.

- [ ] **Step 29: Update OpenAI provider error handling**

In `provider/openai/openai.go`, replace the error status check with the same pattern:

```go
	if resp.StatusCode != http.StatusOK {
		errType, retryable := cc.ClassifyHTTPStatus(resp.StatusCode)
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, &cc.ProviderError{
			Provider:   "openai",
			StatusCode: resp.StatusCode,
			Type:       errType,
			Message:    string(respBody),
			Retryable:  retryable,
			RetryAfter: retryAfter,
			Err:        cc.ErrProviderRequest,
		}
	}
```

Add the same `parseRetryAfter` helper to `provider/openai/convert.go`.

- [ ] **Step 30: Build and test**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go build ./... && go test ./... -v -count=1`
Expected: ALL PASS

- [ ] **Step 31: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add errors.go provider/anthropic/ provider/openai/
git commit -m "feat: providers return structured ProviderError

- Anthropic and OpenAI providers return ProviderError with status classification
- Parse Retry-After header for rate limit responses
- Export ClassifyHTTPStatus for provider use"
```

---

## Task 9: Streaming — Types and StreamReader

**Files:**
- Create: `stream.go`
- Modify: `provider.go`
- Modify: `errors.go`

- [ ] **Step 32: Create stream.go with StreamEvent and StreamReader**

```go
package cc

import (
	"io"
	"sync"
)

// StreamEvent represents a single event in a streaming response.
type StreamEvent struct {
	Type    string          // "text_delta", "tool_use", "message_stop", "error"
	Text    string          // for text_delta events
	ToolUse *ToolUseContent // for tool_use events (complete, after accumulation)
	Usage   Usage           // for message_stop events
	Error   error           // for error events
}

// StreamReader reads streaming events using an iterator pattern.
type StreamReader struct {
	ch     chan StreamEvent
	cancel func()
	once   sync.Once
}

// NewStreamReader creates a StreamReader. The producer writes events to the
// returned channel and closes it when done. Cancel is called on Close().
func NewStreamReader(bufSize int, cancel func()) (*StreamReader, chan<- StreamEvent) {
	ch := make(chan StreamEvent, bufSize)
	return &StreamReader{ch: ch, cancel: cancel}, ch
}

// Next returns the next event. Returns io.EOF when the stream is complete.
func (r *StreamReader) Next() (StreamEvent, error) {
	ev, ok := <-r.ch
	if !ok {
		return StreamEvent{}, io.EOF
	}
	if ev.Error != nil {
		return ev, ev.Error
	}
	return ev, nil
}

// Close cancels the stream and drains remaining events.
func (r *StreamReader) Close() error {
	r.once.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
		// Drain channel to unblock producer
		for range r.ch {
		}
	})
	return nil
}

// Collect reads all events and assembles them into a ChatResponse.
func (r *StreamReader) Collect() (*ChatResponse, error) {
	var contents []Content
	var usage Usage
	var toolUses []ToolUseContent

	for {
		ev, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch ev.Type {
		case "text_delta":
			// Accumulate text into a single TextContent
			if len(contents) == 0 {
				contents = append(contents, TextContent{})
			}
			if tc, ok := contents[len(contents)-1].(TextContent); ok {
				contents[len(contents)-1] = TextContent{Text: tc.Text + ev.Text}
			} else {
				contents = append(contents, TextContent{Text: ev.Text})
			}
		case "tool_use":
			if ev.ToolUse != nil {
				toolUses = append(toolUses, *ev.ToolUse)
				contents = append(contents, *ev.ToolUse)
			}
		case "message_stop":
			usage = ev.Usage
		}
	}

	stopReason := "end_turn"
	if len(toolUses) > 0 {
		stopReason = "tool_use"
	}

	return &ChatResponse{
		Content:    contents,
		StopReason: stopReason,
		Usage:      usage,
	}, nil
}
```

- [ ] **Step 33: Add StreamProvider interface to provider.go**

Append to `provider.go`:

```go
// StreamProvider is optionally implemented by providers that support streaming.
type StreamProvider interface {
	Provider
	Stream(ctx context.Context, params ChatParams) (*StreamReader, error)
}
```

- [ ] **Step 34: Build to verify**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go build ./...`
Expected: No errors

---

## Task 10: Streaming — Tests

**Files:**
- Modify: `agent_test.go`

- [ ] **Step 35: Write StreamReader tests**

```go
func TestStreamReader_Collect(t *testing.T) {
	reader, ch := cc.NewStreamReader(10, nil)

	go func() {
		ch <- cc.StreamEvent{Type: "text_delta", Text: "Hello "}
		ch <- cc.StreamEvent{Type: "text_delta", Text: "world"}
		ch <- cc.StreamEvent{Type: "message_stop", Usage: cc.Usage{InputTokens: 10, OutputTokens: 5}}
		close(ch)
	}()

	resp, err := reader.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text() != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", resp.Text())
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("expected 'end_turn', got %q", resp.StopReason)
	}
}

func TestStreamReader_CollectWithToolUse(t *testing.T) {
	reader, ch := cc.NewStreamReader(10, nil)

	go func() {
		ch <- cc.StreamEvent{Type: "text_delta", Text: "Let me check"}
		ch <- cc.StreamEvent{Type: "tool_use", ToolUse: &cc.ToolUseContent{
			ID: "call_1", Name: "shell", Input: json.RawMessage(`{"command":"date"}`),
		}}
		ch <- cc.StreamEvent{Type: "message_stop", Usage: cc.Usage{InputTokens: 10, OutputTokens: 8}}
		close(ch)
	}()

	resp, err := reader.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("expected 'tool_use', got %q", resp.StopReason)
	}
	if len(resp.ToolUses()) != 1 {
		t.Errorf("expected 1 tool use, got %d", len(resp.ToolUses()))
	}
}

func TestStreamReader_Close(t *testing.T) {
	cancelled := false
	reader, ch := cc.NewStreamReader(10, func() { cancelled = true })

	go func() {
		ch <- cc.StreamEvent{Type: "text_delta", Text: "partial"}
		close(ch)
	}()

	err := reader.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cancelled {
		t.Error("expected cancel to be called")
	}
}
```

- [ ] **Step 36: Run stream tests**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -run "TestStreamReader" -v`
Expected: ALL PASS

---

## Task 11: Streaming — Session.RunStream()

**Files:**
- Modify: `session.go`

- [ ] **Step 37: Add RunStream to Session**

Append to `session.go`:

```go
// RunStream executes the agent loop with streaming output.
// If the provider implements StreamProvider, tokens are streamed as they arrive.
// Otherwise, falls back to Chat() and emits the complete response as events.
func (s *Session) RunStream(ctx context.Context, input string) (<-chan StreamEvent, error) {
	if input == "" {
		return nil, ErrEmptyInput
	}
	if s.agent.provider == nil {
		return nil, ErrNoProvider
	}

	out := make(chan StreamEvent, 64)

	go func() {
		defer close(out)
		s.memory.Add(NewUserMessage(input))

		for turn := range s.agent.maxTurns {
			resp, err := s.streamStep(ctx, out)
			if err != nil {
				out <- StreamEvent{Type: "error", Error: fmt.Errorf("turn %d: %w", turn+1, err)}
				return
			}

			if s.agent.hooks.OnModelResponse != nil {
				s.agent.hooks.OnModelResponse(ctx, resp)
			}

			s.memory.Add(Message{Role: RoleAssistant, Content: resp.Content})

			toolUses := resp.ToolUses()
			if len(toolUses) == 0 {
				out <- StreamEvent{Type: "message_stop", Usage: resp.Usage}
				return
			}

			results := s.executeTools(ctx, toolUses)
			s.memory.Add(NewToolResultMessage(results...))
		}

		out <- StreamEvent{Type: "error", Error: ErrMaxTurns}
	}()

	return out, nil
}

// streamStep attempts to stream from the provider, falling back to Chat().
func (s *Session) streamStep(ctx context.Context, out chan<- StreamEvent) (*ChatResponse, error) {
	params := ChatParams{
		Model:     s.agent.model,
		System:    s.agent.system,
		Messages:  s.memory.Messages(),
		Tools:     s.agent.toolDefs(),
		MaxTokens: s.agent.maxTokens,
	}

	// Try streaming if provider supports it
	if sp, ok := s.agent.provider.(StreamProvider); ok {
		reader, err := sp.Stream(ctx, params)
		if err != nil {
			return nil, err
		}

		// Forward events to output channel while collecting the response
		var contents []Content
		var usage Usage
		for {
			ev, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}

			// Forward to caller
			out <- ev

			switch ev.Type {
			case "text_delta":
				if len(contents) == 0 {
					contents = append(contents, TextContent{})
				}
				if tc, ok := contents[len(contents)-1].(TextContent); ok {
					contents[len(contents)-1] = TextContent{Text: tc.Text + ev.Text}
				}
			case "tool_use":
				if ev.ToolUse != nil {
					contents = append(contents, *ev.ToolUse)
				}
			case "message_stop":
				usage = ev.Usage
			}
		}

		stopReason := "end_turn"
		for _, c := range contents {
			if _, ok := c.(ToolUseContent); ok {
				stopReason = "tool_use"
				break
			}
		}

		return &ChatResponse{Content: contents, StopReason: stopReason, Usage: usage}, nil
	}

	// Fallback: use Chat() and emit as single event
	resp, err := s.agent.provider.Chat(ctx, params)
	if err != nil {
		return nil, err
	}

	text := resp.Text()
	if text != "" {
		out <- StreamEvent{Type: "text_delta", Text: text}
	}
	for _, tu := range resp.ToolUses() {
		tu := tu
		out <- StreamEvent{Type: "tool_use", ToolUse: &tu}
	}

	return resp, nil
}
```

Add `"io"` to the imports in `session.go`.

- [ ] **Step 38: Write RunStream test**

```go
func TestSession_RunStream(t *testing.T) {
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "Streamed!"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}

	agent := cc.New(cc.WithProvider(provider), cc.WithModel("test"))
	session := agent.NewSession()

	ch, err := session.RunStream(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var events []cc.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Fallback mode: should emit text_delta + message_stop
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "text_delta" || events[0].Text != "Streamed!" {
		t.Errorf("expected text_delta 'Streamed!', got %+v", events[0])
	}
	if events[1].Type != "message_stop" {
		t.Errorf("expected message_stop, got %+v", events[1])
	}
}
```

- [ ] **Step 39: Run all tests**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go test ./... -v -count=1`
Expected: ALL PASS

- [ ] **Step 40: Update CLI with -stream flag**

In `cmd/agent/main.go`, add the stream flag and update REPL:

Add flag: `streamMode := flag.Bool("stream", false, "Enable streaming output")`

Update `runREPL` signature to accept stream bool, and when streaming, use `session.RunStream()`:

```go
func runREPL(agent *cc.Agent, stream bool) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	session := agent.NewSession()
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("cc-connect agent (type 'exit' to quit)")

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}
		if input == "/clear" {
			session.ClearMemory()
			fmt.Println("Memory cleared.")
			continue
		}

		if stream {
			ch, err := session.RunStream(ctx, input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", err)
				continue
			}
			fmt.Println()
			for ev := range ch {
				switch ev.Type {
				case "text_delta":
					fmt.Print(ev.Text)
				case "error":
					fmt.Fprintf(os.Stderr, "\nError: %s\n", ev.Error)
				case "message_stop":
					fmt.Printf("\n[tokens: %d in / %d out]\n", ev.Usage.InputTokens, ev.Usage.OutputTokens)
				}
			}
		} else {
			result, err := session.Run(ctx, input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", err)
				continue
			}
			fmt.Printf("\n%s\n", result.Output)
			fmt.Printf("[turns: %d | tokens: %d in / %d out]\n", result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
		}
	}
}
```

Update the call in `main()`: `runREPL(agent, *streamMode)`

- [ ] **Step 41: Build and run all tests**

Run: `cd /Users/alexioschen/Documents/cc-connect/goagent && go build ./... && go vet ./... && go test ./... -v -count=1`
Expected: ALL PASS, no vet warnings

- [ ] **Step 42: Commit**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
git add stream.go session.go provider.go agent_test.go cmd/agent/main.go
git commit -m "feat: streaming support with StreamProvider interface

- StreamEvent and StreamReader types with iterator pattern
- StreamProvider optional interface for streaming-capable providers
- Session.RunStream() with fallback to Chat() for non-streaming providers
- StreamReader.Collect() to assemble events into ChatResponse
- CLI -stream flag for token-by-token output"
```

---

## Task 12: Final Verification

- [ ] **Step 43: Run full test suite and build**

```bash
cd /Users/alexioschen/Documents/cc-connect/goagent
go build ./...
go vet ./...
go test ./... -v -count=1
```

Expected: ALL PASS, no warnings, clean build.

- [ ] **Step 44: Verify git log**

```bash
git log --oneline
```

Expected 4 new commits on top of the initial commit:
1. `feat: add Session for conversation isolation`
2. `feat: concurrent tool execution with errgroup`
3. `feat: structured ProviderError with automatic retry`
4. `feat: streaming support with StreamProvider interface`
