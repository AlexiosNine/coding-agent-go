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
	Path       string `json:"path" desc:"Directory path to list (default: current directory)"`
	Recursive  bool   `json:"recursive" desc:"List files recursively"`
	ShowHidden bool   `json:"show_hidden" desc:"Include hidden files (starting with .)"`
	Offset     int    `json:"offset,omitempty" desc:"Optional: entry offset for pagination (0-indexed)"`
	Limit      int    `json:"limit,omitempty" desc:"Optional: maximum entries per page (default 100)"`
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

			limit := input.Limit
			if limit <= 0 {
				limit = 100
			}

			// Collect all entries into a slice
			var entries []string

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
						entries = append(entries, fmt.Sprintf("%s/", relPath))
					} else {
						info, _ := d.Info()
						entries = append(entries, fmt.Sprintf("%s (%d bytes)", relPath, info.Size()))
					}
					return nil
				})
			} else {
				dirEntries, readErr := os.ReadDir(absPath)
				if readErr != nil {
					return "", fmt.Errorf("read directory: %w", readErr)
				}

				for _, entry := range dirEntries {
					// Skip hidden files if not requested
					if !input.ShowHidden && strings.HasPrefix(entry.Name(), ".") {
						continue
					}

					if entry.IsDir() {
						entries = append(entries, fmt.Sprintf("%s/", entry.Name()))
					} else {
						info, _ := entry.Info()
						entries = append(entries, fmt.Sprintf("%s (%d bytes)", entry.Name(), info.Size()))
					}
				}
			}

			if err != nil {
				return "", err
			}

			total := len(entries)
			offset := input.Offset
			if offset < 0 {
				offset = 0
			}

			header := fmt.Sprintf("Contents of %s:\n\n", absPath)

			if total == 0 {
				return header + "(empty)", nil
			}

			if offset >= total {
				return fmt.Sprintf("%sNo entries at offset %d (total: %d)", header, offset, total), nil
			}

			end := offset + limit
			if end > total {
				end = total
			}

			page := strings.Join(entries[offset:end], "\n")
			hasMore := end < total

			result := header + page

			if hasMore {
				result += fmt.Sprintf("\n---\nTotal: %d entries. Next offset: %d", total, end)
			}

			return result, nil
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
			},
			"offset": {
				"type": "integer",
				"description": "Optional: entry offset for pagination (0-indexed)"
			},
			"limit": {
				"type": "integer",
				"description": "Optional: maximum entries per page (default 100)"
			}
		}
	}`)
}
