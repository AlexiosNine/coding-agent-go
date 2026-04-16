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

// summarizeReadFile extracts file path, line range, and def/class signatures,
// while preserving as much actual code content as possible.
func (s *ToolResultSummarizer) summarizeReadFile(output string) string {
	lines := strings.Split(output, "\n")

	// Extract def/class signatures with line numbers
	var signatures []string
	for i, line := range lines {
		if defClassRe.MatchString(line) {
			sig := strings.TrimSpace(line)
			if len(sig) > 60 {
				sig = sig[:60] + "..."
			}
			signatures = append(signatures, fmt.Sprintf("  line %d: %s", i+1, sig))
		}
	}

	// Build header with symbol index
	var header strings.Builder
	fmt.Fprintf(&header, "[%d lines read]\n", len(lines))
	if len(signatures) > 0 {
		header.WriteString("Symbols found:\n")
		for _, sig := range signatures {
			if header.Len() > s.maxLen/4 {
				header.WriteString("  ...\n")
				break
			}
			header.WriteString(sig)
			header.WriteByte('\n')
		}
	}

	// Fill remaining budget with actual content (head + tail)
	contentBudget := s.maxLen - header.Len()
	if contentBudget < 100 {
		contentBudget = 100
	}

	headerStr := header.String()
	content := strings.Join(lines, "\n")
	if len(content) <= contentBudget {
		return headerStr + "Content:\n" + content
	}

	headSize := contentBudget * 6 / 10
	tailSize := contentBudget - headSize
	return headerStr + "Content:\n" + content[:headSize] + "\n...\n" + content[len(content)-tailSize:]
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
