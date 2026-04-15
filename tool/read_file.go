package tool

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type readFileInput struct {
	Path      string `json:"path" desc:"The file path to read"`
	StartLine int    `json:"start_line,omitempty" desc:"Optional: start line number (1-indexed)"`
	EndLine   int    `json:"end_line,omitempty" desc:"Optional: end line number (inclusive)"`
}

// ReadFile returns a tool that reads file contents.
// Supports optional line range: start_line and end_line (1-indexed, inclusive).
func ReadFile() cc.Tool {
	return cc.NewFuncTool("read_file", "Read the contents of a file. Optionally specify start_line and end_line to read a specific range.", func(ctx context.Context, in readFileInput) (string, error) {
		if in.Path == "" {
			return "", fmt.Errorf("path is required")
		}

		// If no line range specified, read entire file
		if in.StartLine == 0 && in.EndLine == 0 {
			data, err := os.ReadFile(in.Path)
			if err != nil {
				return "", fmt.Errorf("read file %s: %w", in.Path, err)
			}
			return string(data), nil
		}

		// Read specific line range
		file, err := os.Open(in.Path)
		if err != nil {
			return "", fmt.Errorf("open file %s: %w", in.Path, err)
		}
		defer file.Close()

		if in.StartLine < 1 {
			in.StartLine = 1
		}
		if in.EndLine < in.StartLine {
			in.EndLine = in.StartLine + 50 // default to 50 lines if end not specified
		}

		var lines []string
		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if lineNum >= in.StartLine && lineNum <= in.EndLine {
				lines = append(lines, scanner.Text())
			}
			if lineNum > in.EndLine {
				break
			}
		}

		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read file %s: %w", in.Path, err)
		}

		if len(lines) == 0 {
			return "", fmt.Errorf("no lines found in range %d-%d", in.StartLine, in.EndLine)
		}

		return strings.Join(lines, "\n"), nil
	})
}
