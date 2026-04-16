package cc

import "fmt"

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
		// Only track read-only tools
		switch tu.Name {
		case "read_file", "grep", "list_files":
			key := tu.Name + ":" + string(tu.Input)
			t.reads[key]++
			if t.reads[key] >= t.threshold {
				return fmt.Sprintf("[System notice] You have read the same content %d times. You likely have enough information to make the fix now. Please use edit_file to modify the code, or respond with text if you believe no changes are needed.", t.reads[key])
			}
		case "write_file", "edit_file":
			// Reset all counters when a write happens
			t.reads = make(map[string]int)
			return ""
		}
	}
	return ""
}
