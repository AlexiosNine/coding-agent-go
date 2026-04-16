package cc

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const regionSize = 50 // lines per region bucket

// ReadTracker detects when the model repeatedly reads the same file regions
// without making changes, and generates nudge messages to encourage action.
// Tracks by file path + line region (50-line buckets), so reading lines 251-260
// and 240-265 both count as reading region #5 (lines 250-299).
type ReadTracker struct {
	regionReads map[string]int // "path#region" -> read count
	totalReads  int
	threshold   int // nudge after this many reads of same region
}

func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		regionReads: make(map[string]int),
		threshold:   3,
	}
}

// Track records tool calls and returns a nudge message if repeated reads are detected.
func (t *ReadTracker) Track(toolUses []ToolUseContent) string {
	for _, tu := range toolUses {
		switch tu.Name {
		case "read_file":
			path, startLine, endLine := extractPathAndLines(tu.Input)
			if path != "" {
				if nudge := t.trackRegions(path, startLine, endLine); nudge != "" {
					return nudge
				}
			}
		case "grep", "list_files":
			path := extractPath(tu.Input)
			if path != "" {
				t.regionReads[path+"#all"]++
				t.totalReads++
			}
		case "shell":
			cmd := extractShellCommand(tu.Input)
			if isWriteShell(cmd) {
				t.regionReads = make(map[string]int)
				t.totalReads = 0
				return ""
			}
			if isReadOnlyShell(cmd) {
				path, startLine, endLine := extractPathAndLinesFromShell(cmd)
				if path != "" {
					if nudge := t.trackRegions(path, startLine, endLine); nudge != "" {
						return nudge
					}
				}
			}
		case "write_file", "edit_file":
			t.regionReads = make(map[string]int)
			t.totalReads = 0
			return ""
		}
	}

	if t.totalReads >= t.threshold*3 {
		return fmt.Sprintf("[System notice] You have made %d read operations without any code changes. You likely have enough context. Please use edit_file now to make the fix, or respond with text if no changes are needed.", t.totalReads)
	}

	return ""
}

// trackRegions increments read counts for all 50-line regions covered by [startLine, endLine].
func (t *ReadTracker) trackRegions(path string, startLine, endLine int) string {
	if startLine <= 0 && endLine <= 0 {
		// No line info, track as whole-file read
		t.regionReads[path+"#all"]++
		t.totalReads++
		if t.regionReads[path+"#all"] >= t.threshold {
			return fmt.Sprintf("[System notice] You have read %q %d times. You likely have enough information. Please use edit_file to modify the code now, or respond with text if no changes are needed.", path, t.regionReads[path+"#all"])
		}
		return ""
	}

	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 {
		endLine = startLine + regionSize
	}

	startRegion := (startLine - 1) / regionSize
	endRegion := (endLine - 1) / regionSize

	for r := startRegion; r <= endRegion; r++ {
		key := fmt.Sprintf("%s#%d", path, r)
		t.regionReads[key]++
		t.totalReads++
		if t.regionReads[key] >= t.threshold {
			regionStart := r*regionSize + 1
			regionEnd := (r + 1) * regionSize
			return fmt.Sprintf("[System notice] You have read %q lines %d-%d region %d times. You likely have enough information. Please use edit_file to modify the code now, or respond with text if no changes are needed.", path, regionStart, regionEnd, t.regionReads[key])
		}
	}
	return ""
}

// extractPathAndLines gets path, start_line, end_line from tool input JSON.
func extractPathAndLines(input json.RawMessage) (string, int, int) {
	var raw struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if json.Unmarshal(input, &raw) == nil {
		return raw.Path, raw.StartLine, raw.EndLine
	}
	return "", 0, 0
}

// extractPath gets the "path" field from tool input JSON.
func extractPath(input json.RawMessage) string {
	var raw struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(input, &raw) == nil {
		return raw.Path
	}
	return ""
}

// extractShellCommand gets the "command" field from shell tool input.
func extractShellCommand(input json.RawMessage) string {
	var raw struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &raw) == nil {
		return raw.Command
	}
	return ""
}

// sedPattern matches: sed -n '100,200p' /path/to/file
var sedPattern = regexp.MustCompile(`sed\s+-n\s+'(\d+),(\d+)p'\s+(/\S+)`)

// headTailPattern matches: head -N /path or tail -N /path
var headTailPattern = regexp.MustCompile(`(?:head|tail)\s+(?:-(\d+)\s+)?(/\S+)`)

// extractPathAndLinesFromShell extracts file path and line range from shell commands.
func extractPathAndLinesFromShell(cmd string) (string, int, int) {
	// Try sed -n 'start,endp' path
	if m := sedPattern.FindStringSubmatch(cmd); len(m) >= 4 {
		start, _ := strconv.Atoi(m[1])
		end, _ := strconv.Atoi(m[2])
		return m[3], start, end
	}

	// Try head/tail -N path
	if m := headTailPattern.FindStringSubmatch(cmd); len(m) >= 3 {
		n, _ := strconv.Atoi(m[1])
		path := m[2]
		if strings.Contains(cmd, "head") {
			return path, 1, n
		}
		// tail: we don't know total lines, track as whole-file
		return path, 0, 0
	}

	// Fallback: find last path-like argument
	parts := strings.Fields(cmd)
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasPrefix(parts[i], "/") && !strings.HasPrefix(parts[i], "-") {
			return parts[i], 0, 0
		}
	}
	return "", 0, 0
}

func isReadOnlyShell(cmd string) bool {
	readPatterns := []string{"sed -n", "cat ", "head ", "tail ", "grep ", "find ", "ls ", "wc "}
	for _, p := range readPatterns {
		if strings.Contains(cmd, p) {
			return true
		}
	}
	return false
}

func isWriteShell(cmd string) bool {
	writePatterns := []string{">", " -i ", "open(", "write(", "git checkout", "mv ", "cp ", "mkdir "}
	for _, p := range writePatterns {
		if strings.Contains(cmd, p) {
			return true
		}
	}
	return false
}
