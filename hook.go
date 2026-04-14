package cc

import (
	"context"
	"encoding/json"
)

// Hooks provides lifecycle callbacks for the agent loop.
// All fields are optional; nil hooks are skipped.
type Hooks struct {
	// BeforeToolCall is called before a tool is executed.
	// Return an error to skip the tool execution.
	BeforeToolCall func(ctx context.Context, name string, input json.RawMessage) error

	// AfterToolCall is called after a tool execution completes.
	AfterToolCall func(ctx context.Context, name string, output string, err error)

	// OnModelResponse is called after each LLM response is received.
	OnModelResponse func(ctx context.Context, resp *ChatResponse)
}
