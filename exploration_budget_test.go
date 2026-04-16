package cc

import (
	"encoding/json"
	"testing"
)

func TestExplorationBudget_DeductsOnReadOnly(t *testing.T) {
	b := NewExplorationBudget(10)

	toolUses := []ToolUseContent{
		{Name: "read_file", Input: mustMarshal(map[string]interface{}{"path": "test.go"})},
	}

	nudge := b.Consume(toolUses)
	if nudge != "" {
		t.Errorf("unexpected nudge on first read")
	}
	if b.Remaining() != 9 {
		t.Errorf("expected 9 remaining, got %d", b.Remaining())
	}
}

func TestExplorationBudget_RepeatedReadCostsMore(t *testing.T) {
	b := NewExplorationBudget(10)

	// Read same file region 3 times (triggers ReadTracker)
	for i := 0; i < 3; i++ {
		toolUses := []ToolUseContent{
			{Name: "read_file", Input: mustMarshal(map[string]interface{}{
				"path":       "test.go",
				"start_line": 1,
				"end_line":   50,
			})},
		}
		b.Consume(toolUses)
	}

	// First 2 reads cost 1 each, 3rd read costs 2 (repeated)
	// Total: 1 + 1 + 2 = 4
	if b.Remaining() != 6 {
		t.Errorf("expected 6 remaining after repeated reads, got %d", b.Remaining())
	}
}

func TestExplorationBudget_ResetOnMutation(t *testing.T) {
	b := NewExplorationBudget(10)

	// Consume some budget
	b.Consume([]ToolUseContent{
		{Name: "read_file", Input: mustMarshal(map[string]interface{}{"path": "test.go"})},
	})
	b.Consume([]ToolUseContent{
		{Name: "grep", Input: mustMarshal(map[string]interface{}{"pattern": "foo"})},
	})

	if b.Remaining() != 8 {
		t.Errorf("expected 8 remaining, got %d", b.Remaining())
	}

	// Mutating tool resets budget
	b.Consume([]ToolUseContent{
		{Name: "edit_file", Input: mustMarshal(map[string]interface{}{
			"path":       "test.go",
			"old_string": "foo",
			"new_string": "bar",
		})},
	})

	if b.Remaining() != 10 {
		t.Errorf("expected budget reset to 10, got %d", b.Remaining())
	}
}

func TestExplorationBudget_ExhaustionNudge(t *testing.T) {
	b := NewExplorationBudget(3)

	// Consume all budget
	for i := 0; i < 3; i++ {
		nudge := b.Consume([]ToolUseContent{
			{Name: "read_file", Input: mustMarshal(map[string]interface{}{"path": "test.go"})},
		})
		if i < 2 && nudge != "" {
			t.Errorf("unexpected nudge at iteration %d", i)
		}
		if i == 2 && nudge == "" {
			t.Errorf("expected exhaustion nudge at iteration %d", i)
		}
	}

	if b.Remaining() > 0 {
		t.Errorf("expected budget exhausted, got %d remaining", b.Remaining())
	}
}

func TestExplorationBudget_ShellMutationDetection(t *testing.T) {
	b := NewExplorationBudget(10)

	// Consume some budget
	b.Consume([]ToolUseContent{
		{Name: "read_file", Input: mustMarshal(map[string]interface{}{"path": "test.go"})},
	})

	// Shell with mutation (redirect) should reset
	b.Consume([]ToolUseContent{
		{Name: "shell", Input: mustMarshal(map[string]interface{}{
			"command": "echo hello > output.txt",
		})},
	})

	if b.Remaining() != 10 {
		t.Errorf("expected budget reset after shell mutation, got %d", b.Remaining())
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
