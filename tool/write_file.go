package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type writeFileInput struct {
	Path    string `json:"path" desc:"The file path to write to"`
	Content string `json:"content" desc:"The content to write"`
}

// WriteFile returns a tool that writes content to a file.
// It creates parent directories if they don't exist.
func WriteFile() cc.Tool {
	return cc.NewFuncTool("write_file", "Write content to a file, creating directories as needed", func(ctx context.Context, in writeFileInput) (string, error) {
		dir := filepath.Dir(in.Path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create directory %s: %w", dir, err)
		}
		if err := os.WriteFile(in.Path, []byte(in.Content), 0o644); err != nil {
			return "", fmt.Errorf("write file %s: %w", in.Path, err)
		}
		return fmt.Sprintf("Written %d bytes to %s", len(in.Content), in.Path), nil
	})
}
