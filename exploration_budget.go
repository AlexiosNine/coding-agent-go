package cc

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NudgePrefix is the prefix used to identify nudge messages.
const NudgePrefix = "[System notice] Exploration budget exhausted"

// ExplorationBudget tracks remaining exploration tokens and generates nudges.
// It unifies ReadTracker (repeated read detection) and consecutiveExplorationTurns
// into a single budget mechanism.
//
// Each read-only tool call costs 1 token; repeated reads cost 2.
// Any mutating tool call resets the budget.
// When budget is exhausted, the nudge is surfaced via ActiveNudge() and injected
// into the system prompt by Session — not into the conversation history.
type ExplorationBudget struct {
	budget      int // initial budget
	remaining   int
	tracker     *ReadTracker
	nudgeActive bool   // true while a nudge is in effect (cleared on successful mutation)
	nudgeMsg    string // set when nudge is first activated; cleared on Reset
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
func (b *ExplorationBudget) Consume(toolUses []ToolUseContent, results []ToolResultContent) {
	// Check for any successful mutating tool
	for i, tu := range toolUses {
		if isMutatingToolUse(tu) {
			if i < len(results) && !results[i].IsError {
				b.Reset()
				return
			}
		}
	}

	// Deduct tokens for read-only tools
	for _, tu := range toolUses {
		cost := 1
		// Repeated read costs 2
		if nudge := b.tracker.Track([]ToolUseContent{tu}); nudge != "" {
			cost = 2
		}
		b.remaining -= cost
	}

	// Activate nudge on first exhaustion
	if b.remaining <= 0 && !b.nudgeActive {
		b.nudgeActive = true
		b.nudgeMsg = fmt.Sprintf(
			"%s (%d/%d tokens used). You MUST use edit_file now to make changes, or respond with text if no changes are needed.",
			NudgePrefix, b.budget-b.remaining, b.budget,
		)
	}
}

// ActiveNudge returns the nudge text if the budget is currently exhausted, "" otherwise.
// Session calls this before each LLM call and appends the result to the system prompt.
func (b *ExplorationBudget) ActiveNudge() string {
	return b.nudgeMsg
}

// Reset resets remaining budget to initial value and clears the nudge.
func (b *ExplorationBudget) Reset() {
	b.remaining = b.budget
	b.tracker = NewReadTracker()
	b.nudgeActive = false
	b.nudgeMsg = ""
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
