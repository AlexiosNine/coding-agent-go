package cc

import (
	"fmt"
	"strings"
)

// ToolOutputCompressor truncates large tool outputs before they enter memory.
type ToolOutputCompressor struct {
	maxSize int // max chars; 0 = disabled
}

// NewToolOutputCompressor creates a compressor with the given max size.
func NewToolOutputCompressor(maxSize int) *ToolOutputCompressor {
	return &ToolOutputCompressor{maxSize: maxSize}
}

// Compress applies smart truncation based on tool name.
func (c *ToolOutputCompressor) Compress(toolName, output string) string {
	if c.maxSize <= 0 || len(output) <= c.maxSize {
		return output
	}

	// Never compress edit/write results (they're already short)
	if toolName == "edit_file" || toolName == "write_file" {
		return output
	}

	switch toolName {
	case "read_file":
		return truncateReadFile(output, c.maxSize)
	case "shell":
		return truncateShell(output, c.maxSize)
	case "grep":
		return truncateGrep(output, c.maxSize)
	default:
		return truncateGeneric(output, c.maxSize)
	}
}

func truncateSuffix(remaining int) string {
	return fmt.Sprintf("\n[truncated, %d chars remaining. Use offset/limit for pagination]", remaining)
}

// truncateReadFile keeps first 60% + last 40% of the output.
// Preserves nudge hints (e.g., "[Note: You've already read...]") if present.
func truncateReadFile(output string, max int) string {
	// Extract and preserve nudge
	var nudge string
	if idx := strings.Index(output, "\n[Note: "); idx >= 0 {
		nudge = output[idx:]
		output = output[:idx]
	}

	suffix := truncateSuffix(len(output) - max)
	usable := max - len(suffix) - len(nudge)
	if usable <= 0 {
		return output[:max]
	}
	headSize := usable * 6 / 10
	tailSize := usable - headSize
	return output[:headSize] + "\n...\n" + output[len(output)-tailSize:] + suffix + nudge
}

// truncateShell keeps the tail (errors are usually at the end).
func truncateShell(output string, max int) string {
	suffix := truncateSuffix(len(output) - max)
	usable := max - len(suffix)
	if usable <= 0 {
		return output[len(output)-max:]
	}
	return output[len(output)-usable:] + suffix
}

// truncateGrep keeps the first N lines that fit.
func truncateGrep(output string, max int) string {
	lines := strings.SplitAfter(output, "\n")
	var b strings.Builder
	for _, line := range lines {
		if b.Len()+len(line) > max {
			break
		}
		b.WriteString(line)
	}
	remaining := len(output) - b.Len()
	if remaining > 0 {
		b.WriteString(truncateSuffix(remaining))
	}
	return b.String()
}

// truncateGeneric keeps the head.
func truncateGeneric(output string, max int) string {
	suffix := truncateSuffix(len(output) - max)
	usable := max - len(suffix)
	if usable <= 0 {
		return output[:max]
	}
	return output[:usable] + suffix
}
