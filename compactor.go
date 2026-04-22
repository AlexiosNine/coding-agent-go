package cc

import (
	"context"
	"fmt"
	"strings"
)

// Compactor compresses a slice of messages into a summary string.
type Compactor interface {
	Compact(ctx context.Context, msgs []Message) (string, error)
}

// RuleCompactor uses rule-based text truncation (existing behavior).
// It preserves first 10% and last 10% of messages, compressing the middle 80%.
type RuleCompactor struct{}

// Compact compresses messages using rule-based text truncation.
func (r *RuleCompactor) Compact(_ context.Context, msgs []Message) (string, error) {
	return compressMiddleMessages(msgs), nil
}

// LLMCompactor uses an LLM to generate a semantic summary.
// It sends the conversation to a small model (e.g., haiku) to produce a concise summary
// that preserves key decisions, code changes, and file paths.
type LLMCompactor struct {
	provider Provider
	model    string
}

// NewLLMCompactor creates a new LLM-based compactor.
//
// Parameters:
//   - provider: the LLM provider to use for summarization
//   - model: the model name (e.g., "claude-3-5-haiku-20241022" for cost efficiency)
func NewLLMCompactor(provider Provider, model string) *LLMCompactor {
	return &LLMCompactor{
		provider: provider,
		model:    model,
	}
}

const compactSystemPrompt = "You are a conversation summarizer. Output a concise summary preserving key technical details: file paths, code changes, tool call results, decisions made, and errors encountered. Use bullet points."

const compactMaxInputCharsPerMessage = 500

// Compact generates a semantic summary of the conversation using an LLM.
//
// The prompt instructs the model to preserve key decisions, code changes, file paths,
// and tool call results. Input is truncated to stay within a reasonable token budget.
//
// Returns:
//   - string: the compressed summary text
//   - error: provider errors or context cancellation
func (c *LLMCompactor) Compact(ctx context.Context, msgs []Message) (string, error) {
	prompt := buildCompactPrompt(msgs)

	resp, err := c.provider.Chat(ctx, ChatParams{
		Model:     c.model,
		System:    compactSystemPrompt,
		Messages:  []Message{NewUserMessage(prompt)},
		MaxTokens: 1000,
	})
	if err != nil {
		return "", fmt.Errorf("llm compactor: %w", err)
	}

	return resp.Text(), nil
}

// buildCompactPrompt constructs the prompt sent to the LLM for summarization.
// Each message is truncated to compactMaxInputCharsPerMessage characters.
func buildCompactPrompt(msgs []Message) string {
	var b strings.Builder
	b.WriteString("Summarize the following conversation concisely, preserving key decisions, code changes, and file paths:\n\n")

	for _, msg := range msgs {
		b.WriteString(fmt.Sprintf("[%s]: %s\n", msg.Role, truncateForCompaction(msg)))
	}

	return b.String()
}

// truncateForCompaction extracts a truncated text representation of a message
// suitable for the compaction prompt. It prioritizes text content and tool names
// over raw tool input/output.
func truncateForCompaction(msg Message) string {
	var parts []string

	for _, content := range msg.Content {
		switch v := content.(type) {
		case TextContent:
			text := v.Text
			if len(text) > compactMaxInputCharsPerMessage {
				text = text[:compactMaxInputCharsPerMessage] + "..."
			}
			parts = append(parts, text)
		case ToolUseContent:
			inputStr := string(v.Input)
			if len(inputStr) > 100 {
				inputStr = inputStr[:100] + "..."
			}
			parts = append(parts, fmt.Sprintf("Tool:%s(%s)", v.Name, inputStr))
		case ToolResultContent:
			output := v.Content
			if len(output) > 200 {
				output = output[:200] + "..."
			}
			parts = append(parts, fmt.Sprintf("Result: %s", output))
		}
	}

	result := strings.Join(parts, " | ")
	if len(result) > compactMaxInputCharsPerMessage {
		result = result[:compactMaxInputCharsPerMessage] + "..."
	}
	return result
}
