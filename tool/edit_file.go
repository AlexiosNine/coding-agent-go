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

// findSimilarContent searches for the first line of old_string in the file
// and returns surrounding context to help the model correct its old_string.
func findSimilarContent(content, oldString string) string {
	// Take the first line of old_string as search key
	firstLine := strings.SplitN(strings.TrimSpace(oldString), "\n", 2)[0]
	firstLine = strings.TrimSpace(firstLine)
	if firstLine == "" {
		return "Hint: old_string appears to be empty or whitespace-only."
	}

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.Contains(line, firstLine) {
			// Found a partial match, show surrounding context
			start := i - 2
			if start < 0 {
				start = 0
			}
			end := i + 5
			if end > len(lines) {
				end = len(lines)
			}
			context := strings.Join(lines[start:end], "\n")
			return fmt.Sprintf("Hint: found partial match near line %d. Actual content:\n%s", i+1, context)
		}
	}

	return "Hint: no partial match found. Use read_file to check the exact content."
}
