package cc

import (
	"fmt"
	"strings"
)

const (
	defaultRecentWindow      = 10
	defaultMaxMessages       = 50
	defaultContextWindowSize = 0      // 0 means no token-based compression
	defaultCompressThreshold = 0.70   // compress at 70% of context window
	estimatedCharsPerToken   = 4      // rough estimate: 1 token ≈ 4 chars
)

// CompressMemory automatically compresses conversation history when it exceeds
// a threshold. Supports two trigger modes:
//
//  1. Message count: compress when len(messages) > maxMessages
//  2. Token estimate: compress when estimated tokens > contextWindowSize * compressThreshold
//
// 3-tier compression strategy (borrowed from Claude Code):
//   - Tier 1: Drop old tool results (keep only last turn's results)
//   - Tier 2: Summarize old conversation turns into a single message
//   - Tier 3: Keep only system prompt + recent window
type CompressMemory struct {
	messages          []Message
	recentWindow      int     // number of recent messages to always keep
	maxMessages       int     // trigger compression by message count
	contextWindowSize int     // model's context window in tokens (0 = disabled)
	compressThreshold float64 // fraction of context window that triggers compression (e.g. 0.70)
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
		recentWindow:      recentWindow,
		maxMessages:       maxMessages,
		compressThreshold: defaultCompressThreshold,
	}
}

// NewTokenAwareCompressMemory creates a memory that compresses based on estimated token usage.
// contextWindowSize is the model's context window in tokens (e.g. 200000 for 200k).
// Compression triggers when estimated tokens reach 70% of the window.
func NewTokenAwareCompressMemory(contextWindowSize int, recentWindow int) *CompressMemory {
	if recentWindow <= 0 {
		recentWindow = defaultRecentWindow
	}
	return &CompressMemory{
		recentWindow:      recentWindow,
		maxMessages:       1000000, // effectively disable message-count trigger, use token threshold instead
		contextWindowSize: contextWindowSize,
		compressThreshold: defaultCompressThreshold,
	}
}

// SetCompressThreshold sets the fraction of context window that triggers compression.
// Default is 0.70 (70%).
func (c *CompressMemory) SetCompressThreshold(threshold float64) {
	if threshold > 0 && threshold < 1 {
		c.compressThreshold = threshold
	}
}

func (c *CompressMemory) Add(msg Message) {
	c.messages = append(c.messages, msg)
	if c.shouldCompress() {
		c.compress()
	}
}

// shouldCompress checks if compression should be triggered.
func (c *CompressMemory) shouldCompress() bool {
	// Check message count threshold
	if len(c.messages) > c.maxMessages {
		return true
	}

	// Check token-based threshold
	if c.contextWindowSize > 0 {
		estimated := c.EstimateTokens()
		threshold := int(float64(c.contextWindowSize) * c.compressThreshold)
		return estimated > threshold
	}

	return false
}

// EstimateTokens returns a rough estimate of total tokens in all messages.
// Uses a simple heuristic: 1 token ≈ 4 characters.
func (c *CompressMemory) EstimateTokens() int {
	total := 0
	for _, msg := range c.messages {
		for _, content := range msg.Content {
			switch v := content.(type) {
			case TextContent:
				total += len(v.Text) / estimatedCharsPerToken
			case ToolUseContent:
				total += len(v.Input)/estimatedCharsPerToken + 20 // name + overhead
			case ToolResultContent:
				total += len(v.Content) / estimatedCharsPerToken
			}
		}
		total += 4 // per-message overhead (role, formatting)
	}
	return total
}

// TokenUsagePercent returns the estimated percentage of context window used.
// Returns 0 if contextWindowSize is not set.
func (c *CompressMemory) TokenUsagePercent() float64 {
	if c.contextWindowSize <= 0 {
		return 0
	}
	return float64(c.EstimateTokens()) / float64(c.contextWindowSize) * 100
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
