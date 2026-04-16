package cc

import (
	"fmt"
	"strings"
)

// ReadTracker detects when the model repeatedly reads the same files
// without making changes, and generates nudge messages to encourage action.
type ReadTracker struct {
	reads     map[string]int // tool_key -> read count
	threshold int            // nudge after this many repeated reads
}

// NewReadTracker creates a tracker that nudges after 3 repeated reads of the same target.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		reads:     make(map[string]int),
		threshold: 3,
	}
}

// Track records tool calls and returns a nudge message if repeated reads are detected.
// Returns empty string if no nudge is needed.
func (t *ReadTracker) Track(toolUses []ToolUseContent) string {
	for _, tu := range toolUses {
		switch tu.Name {
		case "read_file", "grep", "list_files":
			key := tu.Name + ":" + string(tu.Input)
			t.reads[key]++
			if t.reads[key] >= t.threshold {
				return t.nudgeMessage(t.reads[key])
			}
		case "shell":
			// Track shell commands that are read-only (sed -n, cat, head, tail, grep)
			cmd := string(tu.Input)
			if isReadOnlyShell(cmd) {
				t.reads["shell:"+cmd]++
				if t.reads["shell:"+cmd] >= t.threshold {
					return t.nudgeMessage(t.reads["shell:"+cmd])
				}
			}
			// Shell commands that write files reset the counter
			if isWriteShell(cmd) {
				t.reads = make(map[string]int)
				return ""
			}
		case "write_file", "edit_file":
			t.reads = make(map[string]int)
			return ""
		}
	}

	// Also check total read count across all keys
	totalReads := 0
	for _, count := range t.reads {
		totalReads += count
	}
	if totalReads >= t.threshold*3 {
		return fmt.Sprintf("[System notice] You have made %d read operations without any code changes. You likely have enough context. Please use edit_file now to make the fix, or respond with text if no changes are needed.", totalReads)
	}

	return ""
}

func (t *ReadTracker) nudgeMessage(count int) string {
	return fmt.Sprintf("[System notice] You have read the same content %d times. You likely have enough information. Please use edit_file to modify the code now, or respond with text if you believe no changes are needed.", count)
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
