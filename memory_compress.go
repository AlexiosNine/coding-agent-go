package cc

import (
	"context"
	"fmt"
	"strings"
	"time"
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
	compactor         Compactor // nil = use rule-based (default)
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

// SetCompactor sets the compression strategy.
// When nil (default), rule-based compression is used.
// Pass an LLMCompactor to enable LLM-based semantic summarization.
func (c *CompressMemory) SetCompactor(compactor Compactor) {
	c.compactor = compactor
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
	if n <= 3 {
		return // need at least 3 messages to compress
	}

	// Dynamic split: keep first 10% and last 10%, compress middle 80%
	keepFirst := max(1, n/10)
	keepLast := max(1, n/10)

	// Ensure we have something to compress
	if keepFirst+keepLast >= n {
		keepFirst = 1
		keepLast = max(1, n/2)
	}

	first := c.messages[:keepFirst]
	middleStart := keepFirst
	middleEnd := n - keepLast
	middle := c.messages[middleStart:middleEnd]
	recent := c.messages[middleEnd:]

	// Compress middle using compactor (LLM or rule-based)
	compressed := c.compactMiddle(middle)

	// Rebuild: [first 10%] [compressed middle] [last 10%]
	result := make([]Message, 0, len(first)+1+len(recent))
	result = append(result, first...)
	result = append(result, NewUserMessage(compressed))
	result = append(result, recent...)

	c.messages = result
}

// compactMiddle compresses the middle portion of messages using the configured compactor.
// Falls back to rule-based compression if no compactor is set or if LLM compaction fails.
func (c *CompressMemory) compactMiddle(middle []Message) string {
	if c.compactor == nil {
		return compressMiddleMessages(middle)
	}

	// Use a background context with timeout for compaction
	// to avoid blocking the main conversation loop indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := c.compactor.Compact(ctx, middle)
	if err != nil {
		// Fallback to rule-based on error
		return compressMiddleMessages(middle)
	}

	return "[LLM-compressed conversation history]\n" + result
}

// compressMiddleMessages simplifies tool results and summarizes conversation.
func compressMiddleMessages(msgs []Message) string {
	var parts []string
	parts = append(parts, "[Compressed conversation history]")

	turnCount := 0
	toolCallCount := 0

	for _, msg := range msgs {
		text := msg.Text()
		toolUses := msg.ToolUses()

		if len(toolUses) > 0 {
			toolCallCount += len(toolUses)
			for _, tu := range toolUses {
				// Keep tool name and truncated input
				inputStr := string(tu.Input)
				if len(inputStr) > 100 {
					inputStr = inputStr[:100] + "..."
				}
				parts = append(parts, fmt.Sprintf("- Tool: %s(%s)", tu.Name, inputStr))
			}
			continue
		}

		// Check for tool results - simplify but don't drop
		hasToolResult := false
		for _, content := range msg.Content {
			if tr, ok := content.(ToolResultContent); ok {
				hasToolResult = true
				output := tr.Content
				if len(output) > 200 {
					output = output[:200] + "..."
				}
				idPrefix := tr.ToolUseID
				if len(idPrefix) > 8 {
					idPrefix = idPrefix[:8]
				}
				parts = append(parts, fmt.Sprintf("- Result[%s]: %s", idPrefix, output))
			}
		}
		if hasToolResult {
			continue
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
