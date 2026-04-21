package tool

import (
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
	Offset    int    `json:"offset,omitempty" desc:"Optional: line offset for pagination (0-indexed)"`
	Limit     int    `json:"limit,omitempty" desc:"Optional: maximum lines per page (default 200)"`
}

// ReadFile returns a tool that reads file contents.
// Supports optional line range: start_line and end_line (1-indexed, inclusive).
// Supports pagination via offset and limit for large files (default 500 lines per page).
func ReadFile() cc.Tool {
	// Track which regions have been read to provide nudges (not blocking)
	type readRegion struct {
		path  string
		start int
		end   int
	}
	var readHistory []readRegion

	return cc.NewFuncTool("read_file", "Read the contents of a file. Optionally specify start_line and end_line to read a specific range, or use offset/limit for pagination.", func(ctx context.Context, in readFileInput) (string, error) {
		if in.Path == "" {
			return "", fmt.Errorf("path is required")
		}

		// Determine effective range for nudge detection
		effectiveStart := in.Offset
		effectiveEnd := in.Offset + 500
		if in.StartLine > 0 {
			effectiveStart = in.StartLine - 1
			effectiveEnd = effectiveStart + 50
			if in.EndLine > 0 {
				effectiveEnd = in.EndLine
			}
		}
		if in.Limit > 0 {
			effectiveEnd = effectiveStart + in.Limit
		}

		// Check for repeated reads (nudge only, never block)
		var nudge string
		for _, prev := range readHistory {
			if prev.path != in.Path {
				continue
			}
			// Calculate overlap
			overlapStart := prev.start
			if effectiveStart > overlapStart {
				overlapStart = effectiveStart
			}
			overlapEnd := prev.end
			if effectiveEnd < overlapEnd {
				overlapEnd = effectiveEnd
			}
			overlap := overlapEnd - overlapStart
			regionSize := effectiveEnd - effectiveStart
			if regionSize > 0 && overlap > 0 && float64(overlap)/float64(regionSize) > 0.8 {
				nudge = fmt.Sprintf("\n[Note: You've already read %s lines %d-%d. If you need to make changes, use edit_file.]", in.Path, prev.start+1, prev.end)
				break
			}
		}

		// Record this read
		readHistory = append(readHistory, readRegion{path: in.Path, start: effectiveStart, end: effectiveEnd})

		// Check if pagination is requested (offset > 0)
		if in.Offset > 0 {
			buf := cc.GetOutputBuffer(ctx)
			if buf != nil {
				page, total, exists := buf.TryGetPage(in.Path, in.Offset, in.Limit)
				if exists {
					// Serve from buffer
					if in.Limit <= 0 {
						in.Limit = 500
					}
					hasMore := (in.Offset + in.Limit) < total
					if hasMore {
						nextOffset := in.Offset + in.Limit
						return fmt.Sprintf("%s\n---\nTotal: %d lines. Next offset: %d", page, total, nextOffset) + nudge, nil
					}
					return page + nudge, nil
				}
			}
		}

		// Read file content
		data, err := os.ReadFile(in.Path)
		if err != nil {
			return "", fmt.Errorf("read file %s: %w", in.Path, err)
		}

		content := string(data)
		lines := strings.Split(content, "\n")

		// Handle start_line/end_line logic (legacy behavior, takes precedence over offset/limit)
		if in.StartLine > 0 || in.EndLine > 0 {
			if in.StartLine < 1 {
				in.StartLine = 1
			}
			if in.EndLine < in.StartLine {
				in.EndLine = in.StartLine + 50
			}
			if in.StartLine > len(lines) {
				return "", fmt.Errorf("start_line %d exceeds file length %d", in.StartLine, len(lines))
			}
			if in.EndLine > len(lines) {
				in.EndLine = len(lines)
			}
			// Convert to 0-indexed and return the range
			// Note: offset/limit are ignored when start_line/end_line are specified
			lines = lines[in.StartLine-1 : in.EndLine]
			return strings.Join(lines, "\n") + nudge, nil
		}

		// Store in buffer for future pagination
		buf := cc.GetOutputBuffer(ctx)
		if buf != nil {
			buf.Store(in.Path, content)
		}

		// Apply pagination
		if in.Limit <= 0 {
			in.Limit = 500
		}
		if in.Offset < 0 {
			in.Offset = 0
		}

		total := len(lines)
		if in.Offset >= total {
			return "", fmt.Errorf("offset %d exceeds total lines %d", in.Offset, total)
		}

		end := in.Offset + in.Limit
		if end > total {
			end = total
		}

		page := strings.Join(lines[in.Offset:end], "\n")
		hasMore := end < total

		if hasMore {
			nextOffset := end
			return fmt.Sprintf("%s\n---\nTotal: %d lines. Next offset: %d", page, total, nextOffset) + nudge, nil
		}

		return page + nudge, nil
	})
}
