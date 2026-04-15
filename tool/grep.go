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
	Pattern   string `json:"pattern" desc:"Search pattern (regex supported)"`
	Path      string `json:"path" desc:"File or directory to search in"`
	Recursive bool   `json:"recursive" desc:"Search recursively in directories"`
	MaxResults int   `json:"max_results" desc:"Maximum number of results to return (default 50)"`
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
				maxResults = 50
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

			var result strings.Builder
			count := 0

			if info.IsDir() {
				err = filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
					if err != nil || d.IsDir() {
						return err
					}
					if !input.Recursive && filepath.Dir(path) != absPath {
						return filepath.SkipDir
					}
					// Skip binary/hidden files
					if strings.HasPrefix(d.Name(), ".") {
						return nil
					}
					n, _ := searchFile(path, absPath, re, &result, maxResults-count)
					count += n
					if count >= maxResults {
						return filepath.SkipAll
					}
					return nil
				})
			} else {
				count, err = searchFile(absPath, filepath.Dir(absPath), re, &result, maxResults)
			}

			if err != nil {
				return "", err
			}

			if count == 0 {
				return fmt.Sprintf("No matches found for %q in %s", input.Pattern, absPath), nil
			}

			header := fmt.Sprintf("Found %d match(es) for %q:\n\n", count, input.Pattern)
			return header + result.String(), nil
		},
	)
}

func searchFile(path, basePath string, re *regexp.Regexp, result *strings.Builder, limit int) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil // skip unreadable files
	}
	defer f.Close()

	relPath, _ := filepath.Rel(basePath, path)
	scanner := bufio.NewScanner(f)
	lineNum := 0
	count := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			result.WriteString(fmt.Sprintf("%s:%d: %s\n", relPath, lineNum, line))
			count++
			if count >= limit {
				break
			}
		}
	}
	return count, nil
}
