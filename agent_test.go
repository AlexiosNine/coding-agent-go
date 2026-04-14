package cc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
