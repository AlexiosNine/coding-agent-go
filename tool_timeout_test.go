package cc_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

// slowTool simulates a tool that takes longer than the timeout
type slowTool struct {
	delay time.Duration
}

func (t *slowTool) Name() string        { return "slow_tool" }
func (t *slowTool) Description() string { return "A tool that sleeps" }
func (t *slowTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (t *slowTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	select {
	case <-time.After(t.delay):
		return "completed", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestToolTimeout_Exceeded(t *testing.T) {
	// Create a tool that takes 5 seconds
	slowTool := &slowTool{delay: 5 * time.Second}

	// Mock provider that calls the slow tool
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content: []cc.Content{
					cc.ToolUseContent{
						ID:    "test-1",
						Name:  "slow_tool",
						Input: json.RawMessage(`{}`),
					},
				},
				StopReason: "tool_use",
			},
			{
				Content:    []cc.Content{cc.TextContent{Text: "Tool timed out"}},
				StopReason: "end_turn",
			},
		},
	}

	// Create agent with 1 second timeout
	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(slowTool),
		cc.WithToolTimeout(1*time.Second),
	)

	start := time.Now()
	result, err := agent.Run(context.Background(), "test")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should timeout after ~1 second, not 5 seconds
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v (expected ~1s)", elapsed)
	}

	// Check that the result contains timeout message
	if result.Output != "Tool timed out" {
		t.Errorf("unexpected output: %s", result.Output)
	}
}

func TestToolTimeout_NotExceeded(t *testing.T) {
	// Create a tool that completes quickly
	fastTool := &slowTool{delay: 100 * time.Millisecond}

	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content: []cc.Content{
					cc.ToolUseContent{
						ID:    "test-1",
						Name:  "slow_tool",
						Input: json.RawMessage(`{}`),
					},
				},
				StopReason: "tool_use",
			},
			{
				Content:    []cc.Content{cc.TextContent{Text: "Success"}},
				StopReason: "end_turn",
			},
		},
	}

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(fastTool),
		cc.WithToolTimeout(1*time.Second),
	)

	result, err := agent.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Output != "Success" {
		t.Errorf("unexpected output: %s", result.Output)
	}
}

func TestToolTimeout_Disabled(t *testing.T) {
	// Create a tool that takes 2 seconds
	slowTool := &slowTool{delay: 2 * time.Second}

	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content: []cc.Content{
					cc.ToolUseContent{
						ID:    "test-1",
						Name:  "slow_tool",
						Input: json.RawMessage(`{}`),
					},
				},
				StopReason: "tool_use",
			},
			{
				Content:    []cc.Content{cc.TextContent{Text: "Completed"}},
				StopReason: "end_turn",
			},
		},
	}

	// No timeout configured (default 0)
	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(slowTool),
	)

	start := time.Now()
	result, err := agent.Run(context.Background(), "test")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should complete after ~2 seconds
	if elapsed < 2*time.Second {
		t.Errorf("tool completed too quickly: %v (expected ~2s)", elapsed)
	}

	if result.Output != "Completed" {
		t.Errorf("unexpected output: %s", result.Output)
	}
}
