package cc

import "testing"

func TestReadTracker_DoesNotTreatRepeatedGrepAsRepeatedRead(t *testing.T) {
	tracker := NewReadTracker()
	for range 5 {
		nudge := tracker.Track([]ToolUseContent{{
			Name:  "grep",
			Input: mustMarshal(map[string]any{"path": "sympy/printing/ccode.py", "pattern": "_print_"}),
		}})
		if nudge != "" {
			t.Fatalf("grep should not trigger repeated read nudge, got %q", nudge)
		}
	}
}

func TestReadTracker_AllowsSecondOverlappingRangeRead(t *testing.T) {
	tracker := NewReadTracker()
	reads := [][]int{
		{180, 260},
		{220, 280},
	}
	for _, r := range reads {
		nudge := tracker.Track([]ToolUseContent{{
			Name: "read_file",
			Input: mustMarshal(map[string]any{
				"path":       "sympy/printing/ccode.py",
				"start_line": r[0],
				"end_line":   r[1],
			}),
		}})
		if nudge != "" {
			t.Fatalf("second overlapping range should not trigger repeated read nudge, got %q", nudge)
		}
	}
}
