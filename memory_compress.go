package cc

import (
	"fmt"
	"strings"
)

const (
	defaultRecentWindow = 10
	defaultMaxMessages  = 50
)

// CompressMemory automatically compresses conversation history when it exceeds
// a threshold. It preserves the first message (system context), recent messages,
// and summarizes the middle into a single message.
//
// 3-tier compression strategy (borrowed from Claude Code):
//   - Tier 1: Drop old tool results (keep only last turn's results)
//   - Tier 2: Summarize old conversation turns into a single message
//   - Tier 3: Keep only system prompt + recent window
type CompressMemory struct {
	messages     []Message
	recentWindow int // number of recent messages to always keep
	maxMessages  int // trigger compression when exceeded
}

// NewCompressMemory creates a memory that auto-compresses when messages exceed maxMessages.
// recentWindow controls how many recent messages are preserved during compression.
func NewCompressMemory(recentWindow, maxMessages int) *CompressMemory {
	if recentWindow <= 0 {
		recentWindow = defaultRecentWindow
	}
	if maxMessages <= 0 {
		maxMessages = defaultMaxMessages
	}
	if recentWindow >= maxMessages {
		recentWindow = maxMessages / 2
	}
	return &CompressMemory{
		recentWindow: recentWindow,
		maxMessages:  maxMessages,
	}
}

func (c *CompressMemory) Add(msg Message) {
	c.messages = append(c.messages, msg)
	if len(c.messages) > c.maxMessages {
		c.compress()
	}
}

func (c *CompressMemory) Messages() []Message {
	out := make([]Message, len(c.messages))
	copy(out, c.messages)
	return out
}

func (c *CompressMemory) Clear() {
	c.messages = nil
}

// Len returns the current number of messages.
func (c *CompressMemory) Len() int {
	return len(c.messages)
}

func (c *CompressMemory) compress() {
	n := len(c.messages)
	if n <= c.recentWindow+1 {
		return // nothing to compress
	}

	// Split: [first] [middle...] [recent...]
	first := c.messages[0]                        // preserve first message (user's initial context)
	recentStart := n - c.recentWindow
	middle := c.messages[1:recentStart]
	recent := c.messages[recentStart:]

	// Tier 1: Strip tool results from middle, keep only text
	// Tier 2: Summarize middle conversation into a single message
	summary := summarizeMessages(middle)

	// Rebuild: [first] [summary] [recent...]
	compressed := make([]Message, 0, 2+len(recent))
	compressed = append(compressed, first)
	compressed = append(compressed, NewUserMessage(summary))
	compressed = append(compressed, recent...)

	c.messages = compressed
}

// summarizeMessages creates a text summary of a slice of messages.
// Extracts text content, drops tool results, preserves key information.
func summarizeMessages(msgs []Message) string {
	var parts []string
	parts = append(parts, "[Previous conversation summary]")

	turnCount := 0
	toolCallCount := 0

	for _, msg := range msgs {
		text := msg.Text()
		toolUses := msg.ToolUses()

		if len(toolUses) > 0 {
			toolCallCount += len(toolUses)
			for _, tu := range toolUses {
				parts = append(parts, fmt.Sprintf("- Called tool %q", tu.Name))
			}
			continue
		}

		// Check for tool results
		hasToolResult := false
		for _, content := range msg.Content {
			if _, ok := content.(ToolResultContent); ok {
				hasToolResult = true
				break
			}
		}
		if hasToolResult {
			continue // drop tool results from summary
		}

		if text != "" {
			turnCount++
			role := string(msg.Role)
			// Truncate long messages
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			parts = append(parts, fmt.Sprintf("- %s: %s", role, text))
		}
	}

	parts = append(parts, fmt.Sprintf("[%d turns, %d tool calls compressed]", turnCount, toolCallCount))
	return strings.Join(parts, "\n")
}
