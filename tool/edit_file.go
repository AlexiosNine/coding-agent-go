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
				return "", fmt.Errorf("old_string not found in %s", input.Path)
			}
			if count > 1 {
				return "", fmt.Errorf("old_string found %d times in %s (must be unique)", count, input.Path)
			}

			newContent := strings.Replace(content, input.OldString, input.NewString, 1)

			if err := os.WriteFile(input.Path, []byte(newContent), 0644); err != nil {
				return "", fmt.Errorf("write file %s: %w", input.Path, err)
			}

			return fmt.Sprintf("Replaced in %s (%d bytes → %d bytes)", input.Path, len(content), len(newContent)), nil
		},
	)
}
