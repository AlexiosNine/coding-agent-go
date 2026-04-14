package tool

import (
	"context"
	"fmt"
	"os"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type readFileInput struct {
	Path string `json:"path" desc:"The file path to read"`
}

// ReadFile returns a tool that reads file contents.
func ReadFile() cc.Tool {
	return cc.NewFuncTool("read_file", "Read the contents of a file", func(ctx context.Context, in readFileInput) (string, error) {
		data, err := os.ReadFile(in.Path)
		if err != nil {
			return "", fmt.Errorf("read file %s: %w", in.Path, err)
		}
		return string(data), nil
	})
}
