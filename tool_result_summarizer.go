package cc

import (
	"fmt"
	"regexp"
	"strings"
)

// ToolResultSummarizer generates concise structured summaries of tool outputs.
// Summaries replace raw output in the conversation, while originals stay in OutputBuffer.
type ToolResultSummarizer struct {
	maxLen int // max summary chars; 0 = disabled
}

// NewToolResultSummarizer creates a summarizer with the given max length.
func NewToolResultSummarizer(maxLen int) *ToolResultSummarizer {
	return &ToolResultSummarizer{maxLen: maxLen}
}

// Summarize generates a structured summary based on tool type.
// Returns original output if it's already short enough or tool type shouldn't be summarized.
func (s *ToolResultSummarizer) Summarize(toolName, output string) string {
	if s.maxLen <= 0 || len(output) <= s.maxLen {
		return output
	}

	// Never summarize edit/write results
	if toolName == "edit_file" || toolName == "write_file" {
		return output
	}

	switch toolName {
	case "grep":
		return s.summarizeGrep(output)
	case "read_file":
		return s.summarizeReadFile(output)
	case "shell":
		return s.summarizeShell(output)
	case "list_files":
		return s.summarizeListFiles(output)
	default:
		return truncateGeneric(output, s.maxLen)
	}
}

var defClassRe = regexp.MustCompile(`(?m)^\s*(?:def |class )\w+`)

// summarizeGrep keeps the header line + all match lines (file:line format).
func (s *ToolResultSummarizer) summarizeGrep(output string) string {
	lines := strings.Split(output, "\n")
	var b strings.Builder
	for _, line := range lines {
		if b.Len()+len(line) > s.maxLen {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	result := strings.TrimRight(b.String(), "\n")
	if len(result) < len(output) {
		remaining := len(output) - len(result)
		result += fmt.Sprintf("\n[%d chars truncated]", remaining)
	}
	return result
}

// summarizeReadFile extracts file path, line range, and def/class signatures.
func (s *ToolResultSummarizer) summarizeReadFile(output string) string {
	lines := strings.Split(output, "\n")

	// Extract def/class signatures with line numbers
	var signatures []string
	for i, line := range lines {
		if defClassRe.MatchString(line) {
			sig := strings.TrimSpace(line)
			// Truncate long signatures
			if len(sig) > 60 {
				sig = sig[:60] + "..."
			}
			signatures = append(signatures, fmt.Sprintf("  line %d: %s", i+1, sig))
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[%d lines read]\n", len(lines))

	if len(signatures) > 0 {
		b.WriteString("Symbols found:\n")
		for _, sig := range signatures {
			if b.Len()+len(sig) > s.maxLen-50 {
				b.WriteString("  ...\n")
				break
			}
			b.WriteString(sig)
			b.WriteByte('\n')
		}
	}

	// Keep first few and last few lines of actual content
	contentBudget := s.maxLen - b.Len() - 30
	if contentBudget > 100 {
		headLines := contentBudget * 6 / 10 / 40 // ~40 chars per line
		tailLines := contentBudget * 4 / 10 / 40
		if headLines < 3 {
			headLines = 3
		}
		if tailLines < 2 {
			tailLines = 2
		}
		if headLines+tailLines < len(lines) {
			b.WriteString("Content:\n")
			for _, line := range lines[:headLines] {
				b.WriteString(line)
				b.WriteByte('\n')
			}
			b.WriteString("...\n")
			for _, line := range lines[len(lines)-tailLines:] {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		} else {
			// Short enough, keep all
			b.WriteString("Content:\n")
			b.WriteString(strings.Join(lines, "\n"))
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// summarizeShell keeps exit info + last N lines.
func (s *ToolResultSummarizer) summarizeShell(output string) string {
	lines := strings.Split(output, "\n")
	// Keep last lines that fit
	var kept []string
	total := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if total+len(lines[i]) > s.maxLen-50 {
			break
		}
		kept = append([]string{lines[i]}, kept...)
		total += len(lines[i]) + 1
	}

	var b strings.Builder
	if len(kept) < len(lines) {
		fmt.Fprintf(&b, "[%d lines total, showing last %d]\n", len(lines), len(kept))
	}
	b.WriteString(strings.Join(kept, "\n"))
	return b.String()
}

// summarizeListFiles keeps directory name + file count + file list.
func (s *ToolResultSummarizer) summarizeListFiles(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "[%d entries]\n", len(lines))
	for _, line := range lines {
		if b.Len()+len(line) > s.maxLen {
			fmt.Fprintf(&b, "... and %d more", len(lines)-len(strings.Split(b.String(), "\n"))+1)
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
