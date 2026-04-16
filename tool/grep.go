package tool

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type grepInput struct {
	Pattern    string `json:"pattern" desc:"Search pattern (regex supported)"`
	Path       string `json:"path" desc:"File or directory to search in"`
	Recursive  *bool  `json:"recursive,omitempty" desc:"Search recursively in directories (default: true)"`
	MaxDepth   int    `json:"max_depth,omitempty" desc:"Maximum directory depth for recursive search (default 10, 0=unlimited)"`
	MaxResults int    `json:"max_results" desc:"Maximum number of results to return (default 1000)"`
	Offset     int    `json:"offset,omitempty" desc:"Optional: result offset for pagination (0-indexed)"`
	Limit      int    `json:"limit,omitempty" desc:"Optional: maximum results per page (default 50)"`
}

// Grep returns a tool that searches file contents for a pattern.
func Grep() cc.Tool {
	return cc.NewFuncTool(
		"grep",
		"Search file contents for a pattern (regex supported). Returns matching lines with file path and line number.",
		func(ctx context.Context, input grepInput) (string, error) {
			if input.Pattern == "" {
				return "", fmt.Errorf("pattern is required")
			}

			path := input.Path
			if path == "" {
				path = "."
			}

			maxResults := input.MaxResults
			if maxResults <= 0 {
				maxResults = 1000
			}

			limit := input.Limit
			if limit <= 0 {
				limit = 50
			}

			re, err := regexp.Compile(input.Pattern)
			if err != nil {
				return "", fmt.Errorf("invalid regex pattern: %w", err)
			}

			absPath, err := filepath.Abs(path)
			if err != nil {
				return "", fmt.Errorf("invalid path: %w", err)
			}

			info, err := os.Stat(absPath)
			if err != nil {
				return "", fmt.Errorf("path not found: %w", err)
			}

			// Collect all matches into a slice
			var matches []string

			// Default recursive to true
			recursive := true
			if input.Recursive != nil {
				recursive = *input.Recursive
			}

			maxDepth := input.MaxDepth
			if maxDepth <= 0 {
				maxDepth = 10
			}

			if info.IsDir() {
				baseDepth := strings.Count(absPath, string(filepath.Separator))
				err = filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
					if err != nil {
						return err
					}

					// Skip noisy directories
					if d.IsDir() {
						name := d.Name()
						if name == ".git" || name == "node_modules" || name == "__pycache__" ||
							name == ".tox" || name == ".eggs" || name == "build" || name == "dist" {
							return filepath.SkipDir
						}
						// Depth check
						if recursive {
							depth := strings.Count(path, string(filepath.Separator)) - baseDepth
							if depth > maxDepth {
								return filepath.SkipDir
							}
						}
						return nil
					}

					if !recursive && filepath.Dir(path) != absPath {
						return filepath.SkipDir
					}
					if strings.HasPrefix(d.Name(), ".") {
						return nil
					}
					found := searchFileToSlice(path, absPath, re, maxResults-len(matches))
					matches = append(matches, found...)
					if len(matches) >= maxResults {
						return filepath.SkipAll
					}
					return nil
				})
			} else {
				matches = searchFileToSlice(absPath, filepath.Dir(absPath), re, maxResults)
			}

			if err != nil {
				return "", err
			}

			if len(matches) == 0 {
				return fmt.Sprintf("No matches found for %q in %s", input.Pattern, absPath), nil
			}

			// Paginate the matches slice
			total := len(matches)
			offset := input.Offset
			if offset < 0 {
				offset = 0
			}
			if offset >= total {
				return fmt.Sprintf("No matches at offset %d (total: %d)", offset, total), nil
			}

			end := offset + limit
			if end > total {
				end = total
			}

			page := strings.Join(matches[offset:end], "")
			hasMore := end < total

			header := fmt.Sprintf("Found %d match(es) for %q (showing %d-%d):\n\n", total, input.Pattern, offset+1, end)
			result := header + page

			if hasMore {
				result += fmt.Sprintf("---\nTotal: %d matches. Next offset: %d", total, end)
			}

			return result, nil
		},
	)
}

func searchFileToSlice(path, basePath string, re *regexp.Regexp, limit int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil // skip unreadable files
	}
	defer f.Close()

	relPath, _ := filepath.Rel(basePath, path)
	scanner := bufio.NewScanner(f)
	lineNum := 0
	var matches []string

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			matches = append(matches, fmt.Sprintf("%s:%d: %s\n", relPath, lineNum, line))
			if len(matches) >= limit {
				break
			}
		}
	}
	return matches
}
