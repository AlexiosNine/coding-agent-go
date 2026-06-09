package tool

import (
	"context"
	"fmt"
	"os"
	"strings"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type editFileInput struct {
	Path             string `json:"path" desc:"The file path to edit"`
	OldString        string `json:"old_string,omitempty" desc:"The exact string to find and replace (must be unique in the file). Optional when insert_after_line or insert_before_line is used."`
	NewString        string `json:"new_string" desc:"The replacement string or inserted text"`
	InsertAfterLine  int    `json:"insert_after_line,omitempty" desc:"Optional: insert new_string after this 1-indexed line number instead of replacing old_string"`
	InsertBeforeLine int    `json:"insert_before_line,omitempty" desc:"Optional: insert new_string before this 1-indexed line number instead of replacing old_string"`
}

// EditFile returns a tool that performs targeted string replacement or line insertion.
// This is much more efficient than rewriting entire files.
func EditFile() cc.Tool {
	return cc.NewFuncTool(
		"edit_file",
		"Edit a file by replacing an exact old_string with new_string, or by inserting new_string with insert_after_line/insert_before_line when you know the target line. For adding new methods or mappings, prefer line insertion over replacing a large existing method.",
		func(ctx context.Context, input editFileInput) (string, error) {
			if input.Path == "" {
				return "", fmt.Errorf("path is required")
			}
			data, err := os.ReadFile(input.Path)
			if err != nil {
				return "", fmt.Errorf("read file %s: %w", input.Path, err)
			}

			content := string(data)
			if input.InsertAfterLine > 0 || input.InsertBeforeLine > 0 {
				newContent, err := insertAtLine(content, input.NewString, input.InsertAfterLine, input.InsertBeforeLine)
				if err != nil {
					return "", err
				}
				if err := os.WriteFile(input.Path, []byte(newContent), 0644); err != nil {
					return "", fmt.Errorf("write file %s: %w", input.Path, err)
				}
				return fmt.Sprintf("Inserted in %s (%d bytes → %d bytes). No need to re-read the file to verify.", input.Path, len(content), len(newContent)), nil
			}
			if input.OldString == "" {
				return "", fmt.Errorf("old_string is required unless insert_after_line or insert_before_line is used")
			}

			count := strings.Count(content, input.OldString)

			if count == 0 {
				// Try whitespace-normalized fallback before giving up
				if start, end, ok := findNormalizedMatch(content, input.OldString); ok {
					newContent := content[:start] + input.NewString + content[end:]
					if err := os.WriteFile(input.Path, []byte(newContent), 0644); err != nil {
						return "", fmt.Errorf("write file %s: %w", input.Path, err)
					}
					return fmt.Sprintf("Replaced in %s via whitespace-normalized match (%d bytes → %d bytes). No need to re-read the file to verify.", input.Path, len(content), len(newContent)), nil
				}
				// Help the model by showing nearby content
				hint := findSimilarContent(content, input.OldString)
				return "", fmt.Errorf("old_string not found in %s. %s", input.Path, hint)
			}
			if count > 1 {
				return "", fmt.Errorf("old_string found %d times in %s (must be unique)", count, input.Path)
			}

			newContent := strings.Replace(content, input.OldString, input.NewString, 1)

			if err := os.WriteFile(input.Path, []byte(newContent), 0644); err != nil {
				return "", fmt.Errorf("write file %s: %w", input.Path, err)
			}

			return fmt.Sprintf("Replaced in %s (%d bytes → %d bytes). No need to re-read the file to verify.", input.Path, len(content), len(newContent)), nil
		},
	)
}

func insertAtLine(content, insert string, afterLine, beforeLine int) (string, error) {
	if afterLine > 0 && beforeLine > 0 {
		return "", fmt.Errorf("use only one of insert_after_line or insert_before_line")
	}
	if insert == "" {
		return "", fmt.Errorf("new_string is required")
	}
	if !strings.HasSuffix(insert, "\n") {
		insert += "\n"
	}

	lines := strings.SplitAfter(content, "\n")
	lineCount := len(strings.Split(content, "\n"))
	idx := -1
	switch {
	case afterLine > 0:
		if afterLine > lineCount {
			return "", fmt.Errorf("insert_after_line %d exceeds file length %d", afterLine, lineCount)
		}
		idx = afterLine
	case beforeLine > 0:
		if beforeLine > lineCount {
			return "", fmt.Errorf("insert_before_line %d exceeds file length %d", beforeLine, lineCount)
		}
		idx = beforeLine - 1
	default:
		return "", fmt.Errorf("insert_after_line or insert_before_line is required")
	}
	if idx < 0 || idx > len(lines) {
		return "", fmt.Errorf("invalid insertion line")
	}

	var b strings.Builder
	for _, line := range lines[:idx] {
		b.WriteString(line)
	}
	b.WriteString(insert)
	for _, line := range lines[idx:] {
		b.WriteString(line)
	}
	return b.String(), nil
}

// normalizeWhitespace collapses runs of spaces/tabs to a single space
// and trims leading/trailing whitespace from each line.
func normalizeWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.Join(lines, "\n")
}

// findNormalizedMatch searches for oldString in content using whitespace-normalized
// comparison. Returns byte offsets [start, end) into the original content.
// Returns found=false if zero or more than one normalized match exists.
func findNormalizedMatch(content, oldString string) (start, end int, found bool) {
	normContent := normalizeWhitespace(content)
	normOld := normalizeWhitespace(oldString)
	if normOld == "" {
		return 0, 0, false
	}

	if strings.Count(normContent, normOld) != 1 {
		return 0, 0, false
	}

	normIdx := strings.Index(normContent, normOld)
	normEnd := normIdx + len(normOld)

	// Build mapping from normalized byte offset to original byte offset.
	// Walk both strings in parallel.
	origBytes := []byte(content)
	normBytes := []byte(normContent)
	origPos := 0
	normToOrig := make([]int, len(normBytes)+1)
	for ni := 0; ni < len(normBytes); ni++ {
		normToOrig[ni] = origPos
		// Advance origPos past any extra whitespace that was collapsed
		if origPos < len(origBytes) {
			origPos++
			// If normalized char is a space, skip all original whitespace chars
			if normBytes[ni] == ' ' {
				for origPos < len(origBytes) && (origBytes[origPos] == ' ' || origBytes[origPos] == '\t') {
					origPos++
				}
			}
		}
	}
	normToOrig[len(normBytes)] = origPos

	return normToOrig[normIdx], normToOrig[normEnd], true
}

// findSimilarContent searches for the best matching region in the file
// and returns surrounding context to help the model correct its old_string.
func findSimilarContent(content, oldString string) string {
	lines := strings.Split(content, "\n")
	oldLines := strings.Split(strings.TrimSpace(oldString), "\n")

	if len(oldLines) == 0 || (len(oldLines) == 1 && strings.TrimSpace(oldLines[0]) == "") {
		return "Hint: old_string appears to be empty or whitespace-only."
	}

	// Multi-line sliding window scoring
	windowSize := len(oldLines)
	bestScore := 0
	bestLine := -1

	if windowSize <= len(lines) {
		for i := 0; i <= len(lines)-windowSize; i++ {
			score := 0
			for j, ol := range oldLines {
				trimmed := strings.TrimSpace(ol)
				if trimmed != "" && strings.Contains(lines[i+j], trimmed) {
					score++
				}
			}
			if score > bestScore {
				bestScore = score
				bestLine = i
			}
		}
	}

	// Fallback: single-line search using first non-empty line
	if bestScore == 0 {
		firstLine := ""
		for _, ol := range oldLines {
			t := strings.TrimSpace(ol)
			if t != "" {
				firstLine = t
				break
			}
		}
		if firstLine == "" {
			return "Hint: no partial match found. Use read_file to check the exact content."
		}
		for i, line := range lines {
			if strings.Contains(line, firstLine) {
				bestLine = i
				bestScore = 1
				break
			}
		}
	}

	if bestLine == -1 {
		return "Hint: no partial match found. Use read_file to check the exact content."
	}

	// 扩大上下文范围到 ±5 行
	ctxStart := bestLine - 5
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := bestLine + windowSize + 5
	if ctxEnd > len(lines) {
		ctxEnd = len(lines)
	}

	// 计算 diff (with bounds safety)
	actualEnd := bestLine + windowSize
	if actualEnd > len(lines) {
		actualEnd = len(lines)
	}
	actualLines := lines[bestLine:actualEnd]
	diff := computeLineDiff(oldLines, actualLines)

	// 构造增强的错误信息
	var hint strings.Builder
	hint.WriteString(fmt.Sprintf("Hint: found partial match near line %d (%d/%d lines matched).\n\n", bestLine+1, bestScore, windowSize))

	if diff != "" {
		hint.WriteString("Differences:\n")
		hint.WriteString(diff)
		hint.WriteString("\n")
	}

	hint.WriteString(fmt.Sprintf("Actual content (lines %d-%d):\n", ctxStart+1, ctxEnd))
	for i := ctxStart; i < ctxEnd; i++ {
		marker := "  "
		if i >= bestLine && i < bestLine+windowSize {
			marker = "→ " // 标记匹配区域
		}
		hint.WriteString(fmt.Sprintf("%s%4d: %s\n", marker, i+1, lines[i]))
	}

	hint.WriteString("\nTip: Use read_file to get the exact content, then copy-paste as old_string. If you are adding a new method or mapping and already know the line number, use insert_after_line or insert_before_line instead of replacing a large block.")

	return hint.String()
}

// computeLineDiff compares expected and actual line slices, returns a diff string
// highlighting mismatched lines. Uses trimmed comparison and caps output at 10 diffs.
func computeLineDiff(expected, actual []string) string {
	var diff strings.Builder
	maxDiffs := 10
	count := 0

	limit := len(expected)
	if len(actual) < limit {
		limit = len(actual)
	}

	for i := 0; i < limit && count < maxDiffs; i++ {
		if strings.TrimSpace(expected[i]) != strings.TrimSpace(actual[i]) {
			diff.WriteString(fmt.Sprintf("  Line %d:\n", i+1))
			diff.WriteString(fmt.Sprintf("    Expected: %s\n", expected[i]))
			diff.WriteString(fmt.Sprintf("    Actual:   %s\n", actual[i]))
			count++
		}
	}

	if len(expected) != len(actual) {
		diff.WriteString(fmt.Sprintf("  Line count mismatch: expected %d lines, actual %d lines\n", len(expected), len(actual)))
	}

	if count >= maxDiffs {
		diff.WriteString("  ... (more differences truncated)\n")
	}

	return diff.String()
}
