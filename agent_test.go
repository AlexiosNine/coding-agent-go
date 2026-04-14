package cc_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

// mockProvider is a test provider that returns pre-configured responses.
type mockProvider struct {
	responses []*cc.ChatResponse
	callIndex int
}

func (m *mockProvider) Chat(_ context.Context, _ cc.ChatParams) (*cc.ChatResponse, error) {
	if m.callIndex >= len(m.responses) {
		return nil, errors.New("no more mock responses")
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func TestAgent_Run_SimpleText(t *testing.T) {
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content:    []cc.Content{cc.TextContent{Text: "Hello!"}},
				StopReason: "end_turn",
				Usage:      cc.Usage{InputTokens: 10, OutputTokens: 5},
			},
		},
	}

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("test-model"),
	)

	result, err := agent.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Hello!" {
		t.Errorf("expected output 'Hello!', got %q", result.Output)
	}
	if result.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", result.Turns)
	}
}

func TestAgent_Run_ToolUse(t *testing.T) {
	// Mock provider returns tool_use, then text after tool result
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content: []cc.Content{
					cc.ToolUseContent{
						ID:    "call_1",
						Name:  "add",
						Input: json.RawMessage(`{"a":2,"b":3}`),
					},
				},
				StopReason: "tool_use",
				Usage:      cc.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{
				Content:    []cc.Content{cc.TextContent{Text: "The sum is 5"}},
				StopReason: "end_turn",
				Usage:      cc.Usage{InputTokens: 15, OutputTokens: 8},
			},
		},
	}

	addTool := cc.NewFuncTool("add", "Add two numbers", func(_ context.Context, in struct {
		A int `json:"a"`
		B int `json:"b"`
	}) (string, error) {
		return "5", nil
	})

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("test-model"),
		cc.WithTools(addTool),
	)

	result, err := agent.Run(context.Background(), "What is 2+3?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "The sum is 5" {
		t.Errorf("expected output 'The sum is 5', got %q", result.Output)
	}
	if result.Turns != 2 {
		t.Errorf("expected 2 turns, got %d", result.Turns)
	}
	if result.Usage.InputTokens != 25 {
		t.Errorf("expected 25 input tokens, got %d", result.Usage.InputTokens)
	}
}

func TestAgent_Run_MaxTurns(t *testing.T) {
	// Provider always returns tool_use, causing infinite loop
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content: []cc.Content{
					cc.ToolUseContent{ID: "call_1", Name: "noop", Input: json.RawMessage(`{}`)},
				},
				StopReason: "tool_use",
			},
			{
				Content: []cc.Content{
					cc.ToolUseContent{ID: "call_2", Name: "noop", Input: json.RawMessage(`{}`)},
				},
				StopReason: "tool_use",
			},
			{
				Content: []cc.Content{
					cc.ToolUseContent{ID: "call_3", Name: "noop", Input: json.RawMessage(`{}`)},
				},
				StopReason: "tool_use",
			},
		},
	}

	noopTool := cc.NewFuncTool("noop", "Do nothing", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("test-model"),
		cc.WithTools(noopTool),
		cc.WithMaxTurns(3),
	)

	result, err := agent.Run(context.Background(), "Loop forever")
	if !errors.Is(err, cc.ErrMaxTurns) {
		t.Errorf("expected ErrMaxTurns, got %v", err)
	}
	if result.Turns != 3 {
		t.Errorf("expected 3 turns, got %d", result.Turns)
	}
}

func TestAgent_Run_EmptyInput(t *testing.T) {
	agent := cc.New(cc.WithProvider(&mockProvider{}))
	_, err := agent.Run(context.Background(), "")
	if !errors.Is(err, cc.ErrEmptyInput) {
		t.Errorf("expected ErrEmptyInput, got %v", err)
	}
}

func TestAgent_Run_NoProvider(t *testing.T) {
	agent := cc.New()
	_, err := agent.Run(context.Background(), "test")
	if !errors.Is(err, cc.ErrNoProvider) {
		t.Errorf("expected ErrNoProvider, got %v", err)
	}
}

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

func TestSession_ConcurrentToolExecution(t *testing.T) {
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
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Errorf("expected 3 tool executions, got %d", len(order))
	}
}

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

	if cc.IsRetryable(errors.New("random error")) {
		t.Error("expected non-ProviderError to not be retryable")
	}
}

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

func TestAsAgentTool_LLMDriven(t *testing.T) {
	// Sub-agent: always responds with "42"
	subProvider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "42"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 5, OutputTokens: 2}},
		},
	}
	subAgent := cc.New(cc.WithProvider(subProvider), cc.WithModel("sub"))

	// Parent agent: first call returns tool_use for "calc", second call returns final text
	parentProvider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content: []cc.Content{
					cc.ToolUseContent{ID: "call_1", Name: "calc", Input: json.RawMessage(`{"task":"compute 6*7"}`)},
				},
				StopReason: "tool_use",
				Usage:      cc.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{
				Content:    []cc.Content{cc.TextContent{Text: "The answer is 42"}},
				StopReason: "end_turn",
				Usage:      cc.Usage{InputTokens: 15, OutputTokens: 5},
			},
		},
	}

	agent := cc.New(
		cc.WithProvider(parentProvider),
		cc.WithModel("parent"),
		cc.WithTools(cc.AsAgentTool("calc", "A calculator sub-agent", subAgent)),
	)

	result, err := agent.Run(context.Background(), "What is 6*7?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "The answer is 42" {
		t.Errorf("expected 'The answer is 42', got %q", result.Output)
	}
	if result.Turns != 2 {
		t.Errorf("expected 2 turns, got %d", result.Turns)
	}
}

func TestAsAgentTool_CodeDriven(t *testing.T) {
	// Direct code-level invocation of sub-agent
	subProvider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "Research complete: Go is great"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 10, OutputTokens: 8}},
		},
	}
	subAgent := cc.New(cc.WithProvider(subProvider), cc.WithModel("sub"), cc.WithSystem("You are a researcher"))

	result, err := subAgent.Run(context.Background(), "Tell me about Go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "Research complete: Go is great" {
		t.Errorf("expected 'Research complete: Go is great', got %q", result.Output)
	}
}

func TestAsAgentTool_EmptyTask(t *testing.T) {
	subAgent := cc.New(cc.WithProvider(&mockProvider{
		responses: []*cc.ChatResponse{{Content: []cc.Content{cc.TextContent{Text: "ok"}}, StopReason: "end_turn"}},
	}))

	tool := cc.AsAgentTool("test", "test agent", subAgent)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"task":""}`))
	if err == nil {
		t.Error("expected error for empty task")
	}
}

func TestAsAgentTool_StructuredResult(t *testing.T) {
	subProvider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "result data"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}
	subAgent := cc.New(cc.WithProvider(subProvider), cc.WithModel("sub"))
	tool := cc.AsAgentTool("worker", "a worker", subAgent)

	output, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"do work"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Output should be structured JSON
	var result cc.AgentToolResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("expected JSON output, got: %s", output)
	}
	if result.Output != "result data" {
		t.Errorf("expected output 'result data', got %q", result.Output)
	}
	if result.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", result.Turns)
	}
}

func TestAsAgentTool_ContextPassthrough(t *testing.T) {
	// Sub-agent receives context from parent LLM via the "context" field
	subProvider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.TextContent{Text: "Got the context"}}, StopReason: "end_turn", Usage: cc.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}
	subAgent := cc.New(cc.WithProvider(subProvider), cc.WithModel("sub"))
	tool := cc.AsAgentTool("worker", "a worker", subAgent)

	output, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"summarize","context":"The user asked about Go concurrency"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result cc.AgentToolResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("expected JSON output, got: %s", output)
	}
	if result.Output != "Got the context" {
		t.Errorf("expected 'Got the context', got %q", result.Output)
	}
}

func TestSharedState(t *testing.T) {
	state := cc.NewSharedState()
	state.Set("key1", "value1")
	state.Set("count", 42)

	if state.GetString("key1") != "value1" {
		t.Errorf("expected 'value1', got %q", state.GetString("key1"))
	}
	if state.Get("count") != 42 {
		t.Errorf("expected 42, got %v", state.Get("count"))
	}
	if state.GetString("missing") != "" {
		t.Errorf("expected empty string for missing key")
	}

	// Test context propagation
	ctx := cc.WithSharedState(context.Background(), state)
	retrieved := cc.GetSharedState(ctx)
	if retrieved == nil {
		t.Fatal("expected shared state from context")
	}
	if retrieved.GetString("key1") != "value1" {
		t.Errorf("expected 'value1' from context state")
	}
}
