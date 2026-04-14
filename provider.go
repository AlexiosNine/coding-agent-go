package cc

import (
	"context"
	"encoding/json"
)

// Provider is the interface for LLM backends.
// Each provider (Anthropic, OpenAI, etc.) implements this interface
// to translate between the internal message format and the provider's API.
type Provider interface {
	// Chat sends messages to the LLM and returns a complete response.
	Chat(ctx context.Context, params ChatParams) (*ChatResponse, error)
}

// ChatParams contains the parameters for a Chat request.
type ChatParams struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int
}

// ToolDef describes a tool that the LLM can call.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ChatResponse is the response from a Chat request.
type ChatResponse struct {
	Content    []Content
	StopReason string // "end_turn", "tool_use", "max_tokens"
	Usage      Usage
}

// Text returns the concatenated text content of the response.
func (r *ChatResponse) Text() string {
	var text string
	for _, c := range r.Content {
		if tc, ok := c.(TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

// ToolUses returns all tool use blocks in the response.
func (r *ChatResponse) ToolUses() []ToolUseContent {
	var uses []ToolUseContent
	for _, c := range r.Content {
		if tu, ok := c.(ToolUseContent); ok {
			uses = append(uses, tu)
		}
	}
	return uses
}

// HasToolUse returns true if the response contains any tool use blocks.
func (r *ChatResponse) HasToolUse() bool {
	for _, c := range r.Content {
		if _, ok := c.(ToolUseContent); ok {
			return true
		}
	}
	return false
}
