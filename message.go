package cc

import "encoding/json"

// Role represents the role of a message sender.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message represents a single message in a conversation.
type Message struct {
	Role    Role
	Content []Content
}

// NewUserMessage creates a user message with text content.
func NewUserMessage(text string) Message {
	return Message{
		Role:    RoleUser,
		Content: []Content{TextContent{Text: text}},
	}
}

// NewAssistantMessage creates an assistant message with text content.
func NewAssistantMessage(text string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: []Content{TextContent{Text: text}},
	}
}

// NewToolResultMessage creates a user message with tool result content.
func NewToolResultMessage(results ...ToolResultContent) Message {
	content := make([]Content, len(results))
	for i, r := range results {
		content[i] = r
	}
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// Text returns the concatenated text content of the message.
func (m Message) Text() string {
	var text string
	for _, c := range m.Content {
		if tc, ok := c.(TextContent); ok {
			text += tc.Text
		}
	}
	return text
}

// ToolUses returns all tool use blocks in the message.
func (m Message) ToolUses() []ToolUseContent {
	var uses []ToolUseContent
	for _, c := range m.Content {
		if tu, ok := c.(ToolUseContent); ok {
			uses = append(uses, tu)
		}
	}
	return uses
}

// Content is the interface for message content blocks.
// A message can contain multiple content blocks of different types.
type Content interface {
	contentType() string
}

// TextContent represents plain text content.
type TextContent struct {
	Text string
}

func (TextContent) contentType() string { return "text" }

// ToolUseContent represents a tool call requested by the assistant.
type ToolUseContent struct {
	ID    string
	Name  string
	Input json.RawMessage
}

func (ToolUseContent) contentType() string { return "tool_use" }

// ToolResultContent represents the result of a tool execution.
type ToolResultContent struct {
	ToolUseID string
	Content   string
	IsError   bool
}

func (ToolResultContent) contentType() string { return "tool_result" }

// Usage tracks token usage for a single LLM call.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Add returns a new Usage with the sum of both usages.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
	}
}
