package cc

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ReadTracker detects when the model repeatedly reads the same files
// without making changes, and generates nudge messages to encourage action.
type ReadTracker struct {
	fileReads  map[string]int // file path -> read count
	totalReads int
	threshold  int // nudge after this many reads of same file
}

// NewReadTracker creates a tracker that nudges after 3 reads of the same file.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		fileReads: make(map[string]int),
		threshold: 3,
	}
}

// pathFromShellCmd extracts file path from common shell read commands.
var shellPathPattern = regexp.MustCompile(`(?:sed\s+-n\s+'[^']+'\s+|cat\s+|head\s+(?:-\d+\s+)?|tail\s+(?:-\d+\s+)?|grep\s+(?:-[^\s]+\s+)*(?:'[^']*'|"[^"]*"|\S+)\s+)(/[^\s|>]+)`)

// Track records tool calls and returns a nudge message if repeated reads are detected.
func (t *ReadTracker) Track(toolUses []ToolUseContent) string {
	for _, tu := range toolUses {
		switch tu.Name {
		case "read_file":
			path := extractPath(tu.Input)
			if path != "" {
				t.fileReads[path]++
				t.totalReads++
				if t.fileReads[path] >= t.threshold {
					return fmt.Sprintf("[System notice] You have read %q %d times. You likely have enough information. Please use edit_file to modify the code now, or respond with text if no changes are needed.", path, t.fileReads[path])
				}
			}
		case "grep", "list_files":
			path := extractPath(tu.Input)
			if path != "" {
				t.fileReads[path]++
				t.totalReads++
			}
		case "shell":
			cmd := extractShellCommand(tu.Input)
			if isWriteShell(cmd) {
				t.fileReads = make(map[string]int)
				t.totalReads = 0
				return ""
			}
			if isReadOnlyShell(cmd) {
				path := extractPathFromShell(cmd)
				if path != "" {
					t.fileReads[path]++
					t.totalReads++
					if t.fileReads[path] >= t.threshold {
						return fmt.Sprintf("[System notice] You have read %q %d times. You likely have enough information. Please use edit_file to modify the code now, or respond with text if no changes are needed.", path, t.fileReads[path])
					}
				}
			}
		case "write_file", "edit_file":
			t.fileReads = make(map[string]int)
			t.totalReads = 0
			return ""
		}
	}

	// Global nudge: too many total reads without any write
	if t.totalReads >= t.threshold*3 {
		return fmt.Sprintf("[System notice] You have made %d read operations without any code changes. You likely have enough context. Please use edit_file now to make the fix, or respond with text if no changes are needed.", t.totalReads)
	}

	return ""
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

// extractPathFromShell extracts the file path from a shell read command.
func extractPathFromShell(cmd string) string {
	matches := shellPathPattern.FindStringSubmatch(cmd)
	if len(matches) >= 2 {
		return matches[1]
	}
	// Fallback: find last argument that looks like a path
	parts := strings.Fields(cmd)
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasPrefix(parts[i], "/") && !strings.HasPrefix(parts[i], "-") {
			return parts[i]
		}
	}
	return ""
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
