package cc

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExplorationBudget tracks remaining exploration tokens and generates nudges.
// It unifies ReadTracker (repeated read detection) and consecutiveExplorationTurns
// into a single budget mechanism.
//
// Each read-only tool call costs 1 token; repeated reads cost 2.
// Any mutating tool call resets the budget.
// When budget is exhausted, a strong nudge is injected.
type ExplorationBudget struct {
	budget    int // initial budget
	remaining int
	tracker   *ReadTracker
}

// NewExplorationBudget creates a budget with the given initial token count.
func NewExplorationBudget(budget int) *ExplorationBudget {
	return &ExplorationBudget{
		budget:    budget,
		remaining: budget,
		tracker:   NewReadTracker(),
	}
}

// Consume processes a batch of tool uses and their results, deducting tokens.
// Only resets budget when a mutating tool succeeds (not on error).
// Returns a nudge string if budget is exhausted, or "" otherwise.
func (b *ExplorationBudget) Consume(toolUses []ToolUseContent, results []ToolResultContent) string {
	// Check for any successful mutating tool
	for i, tu := range toolUses {
		if isMutatingToolUse(tu) {
			// Only reset if the tool succeeded (not an error)
			if i < len(results) && !results[i].IsError {
				b.Reset()
				return ""
			}
		}
	}

	// Deduct tokens for read-only tools
	for _, tu := range toolUses {
		cost := 1
		// Check if this is a repeated read (costs 2)
		nudge := b.tracker.Track([]ToolUseContent{tu})
		if nudge != "" {
			cost = 2
		}
		b.remaining -= cost
	}

	if b.remaining <= 0 {
		return fmt.Sprintf("[System notice] Exploration budget exhausted (%d/%d tokens used). You MUST use edit_file now to make changes, or respond with text if no changes are needed.", b.budget-b.remaining, b.budget)
	}

	return ""
}

// Reset resets remaining budget to initial value and clears the read tracker.
func (b *ExplorationBudget) Reset() {
	b.remaining = b.budget
	b.tracker = NewReadTracker()
}

// Remaining returns the current remaining budget.
func (b *ExplorationBudget) Remaining() int {
	return b.remaining
}

// isMutatingToolUse checks if a tool use is mutating (write/edit).
func isMutatingToolUse(tu ToolUseContent) bool {
	if tu.Name == "write_file" || tu.Name == "edit_file" {
		return true
	}
	// shell can also be mutating if it contains file-modifying commands
	if tu.Name == "shell" {
		var shellInput struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(tu.Input, &shellInput); err == nil {
			cmd := shellInput.Command
			if strings.Contains(cmd, ">") || strings.Contains(cmd, " -i") ||
				strings.Contains(cmd, "open(") || strings.Contains(cmd, "write(") ||
				strings.Contains(cmd, "rm ") || strings.Contains(cmd, "mv ") {
				return true
			}
		}
	}
	return false
}
