package tool

import (
	"context"
	"fmt"
	"os"
	"strings"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type editFileInput struct {
	Path      string `json:"path" desc:"The file path to edit"`
	OldString string `json:"old_string" desc:"The exact string to find and replace (must be unique in the file)"`
	NewString string `json:"new_string" desc:"The replacement string"`
}

// EditFile returns a tool that performs targeted string replacement in a file.
// This is much more efficient than rewriting entire files.
func EditFile() cc.Tool {
	return cc.NewFuncTool(
		"edit_file",
		"Replace a specific string in a file. Provide the exact old_string to find and new_string to replace it with. The old_string must be unique in the file.",
		func(ctx context.Context, input editFileInput) (string, error) {
			if input.Path == "" {
				return "", fmt.Errorf("path is required")
			}
			if input.OldString == "" {
				return "", fmt.Errorf("old_string is required")
			}

			data, err := os.ReadFile(input.Path)
			if err != nil {
				return "", fmt.Errorf("read file %s: %w", input.Path, err)
			}

			content := string(data)
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

	ctxStart := bestLine - 2
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := bestLine + windowSize + 3
	if ctxEnd > len(lines) {
		ctxEnd = len(lines)
	}
	context := strings.Join(lines[ctxStart:ctxEnd], "\n")
	return fmt.Sprintf("Hint: found partial match near line %d (%d/%d lines matched). Actual content:\n%s",
		bestLine+1, bestScore, windowSize, context)
}
