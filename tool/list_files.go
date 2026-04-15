package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type listFilesInput struct {
	Path      string `json:"path" desc:"Directory path to list (default: current directory)"`
	Recursive bool   `json:"recursive" desc:"List files recursively"`
	ShowHidden bool  `json:"show_hidden" desc:"Include hidden files (starting with .)"`
}

// ListFiles returns a tool that lists files in a directory.
func ListFiles() cc.Tool {
	return cc.NewFuncTool(
		"list_files",
		"List files and directories. Use this to explore directory contents.",
		func(ctx context.Context, input listFilesInput) (string, error) {
			path := input.Path
			if path == "" {
				path = "."
			}

			// Resolve to absolute path
			absPath, err := filepath.Abs(path)
			if err != nil {
				return "", fmt.Errorf("invalid path: %w", err)
			}

			// Check if path exists
			info, err := os.Stat(absPath)
			if err != nil {
				return "", fmt.Errorf("path not found: %w", err)
			}

			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", absPath)
			}

			var result strings.Builder
			result.WriteString(fmt.Sprintf("Contents of %s:\n\n", absPath))

			if input.Recursive {
				err = filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
					if err != nil {
						return err
					}

					// Skip hidden files if not requested
					if !input.ShowHidden && strings.HasPrefix(d.Name(), ".") && path != absPath {
						if d.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}

					relPath, _ := filepath.Rel(absPath, path)
					if relPath == "." {
						return nil
					}

					if d.IsDir() {
						result.WriteString(fmt.Sprintf("%s/\n", relPath))
					} else {
						info, _ := d.Info()
						result.WriteString(fmt.Sprintf("%s (%d bytes)\n", relPath, info.Size()))
					}
					return nil
				})
			} else {
				entries, err := os.ReadDir(absPath)
				if err != nil {
					return "", fmt.Errorf("read directory: %w", err)
				}

				for _, entry := range entries {
					// Skip hidden files if not requested
					if !input.ShowHidden && strings.HasPrefix(entry.Name(), ".") {
						continue
					}

					if entry.IsDir() {
						result.WriteString(fmt.Sprintf("%s/\n", entry.Name()))
					} else {
						info, _ := entry.Info()
						result.WriteString(fmt.Sprintf("%s (%d bytes)\n", entry.Name(), info.Size()))
					}
				}
			}

			if err != nil {
				return "", err
			}

			return result.String(), nil
		},
	)
}

func (t listFilesInput) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Directory path to list (default: current directory)"
			},
			"recursive": {
				"type": "boolean",
				"description": "List files recursively"
			},
			"show_hidden": {
				"type": "boolean",
				"description": "Include hidden files (starting with .)"
			}
		}
	}`)
}
