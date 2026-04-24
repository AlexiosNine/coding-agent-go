package cc

import (
	"strings"
	"testing"
)

// helper: add n filler messages to memory
func addFillers(m Memory, n int) {
	for range n {
		m.Add(NewUserMessage("filler"))
		m.Add(NewAssistantMessage("response"))
	}
}

// helper: count messages whose text has the given prefix
func countPrefix(msgs []Message, prefix string) int {
	count := 0
	for _, msg := range msgs {
		if strings.HasPrefix(msg.Text(), prefix) {
			count++
		}
	}
	return count
}

// helper: exhaust a budget with n read-only turns
func exhaustBudget(b *ExplorationBudget, n int) {
	for range n {
		toolUses := []ToolUseContent{{Name: "read_file"}}
		results := []ToolResultContent{{ToolUseID: "1", Content: "data"}}
		b.Consume(toolUses, results)
	}
}

// ─── Core nudge lifecycle ────────────────────────────────────────────────────

// TestNudgeInjectedOnExhaustion verifies that ActiveNudge is set exactly when
// the budget hits zero, and not before.
func TestNudgeInjectedOnExhaustion(t *testing.T) {
	b := NewExplorationBudget(3)

	for i := range 2 {
		exhaustBudget(b, 1)
		if b.ActiveNudge() != "" {
			t.Errorf("turn %d: expected no nudge before exhaustion, got %q", i, b.ActiveNudge())
		}
	}

	exhaustBudget(b, 1)
	nudge := b.ActiveNudge()
	if nudge == "" {
		t.Fatal("expected nudge on exhaustion, got empty")
	}
	if !strings.HasPrefix(nudge, NudgePrefix) {
		t.Errorf("nudge missing prefix: %q", nudge)
	}
	if !b.nudgeActive {
		t.Error("expected nudgeActive=true after exhaustion")
	}
}

// TestNudgeClearedOnSuccessfulMutation verifies Reset clears nudgeActive.
func TestNudgeClearedOnSuccessfulMutation(t *testing.T) {
	b := NewExplorationBudget(2)
	exhaustBudget(b, 2)

	if !b.nudgeActive {
		t.Fatal("precondition: nudgeActive should be true")
	}

	toolUses := []ToolUseContent{{Name: "edit_file"}}
	results := []ToolResultContent{{ToolUseID: "2", Content: "success", IsError: false}}
	b.Consume(toolUses, results)

	if b.ActiveNudge() != "" {
		t.Errorf("expected no nudge after mutation, got %q", b.ActiveNudge())
	}
	if b.nudgeActive {
		t.Error("expected nudgeActive=false after successful mutation")
	}
}

// EC5: Failed mutation must NOT reset the budget or clear nudgeActive.
func TestFailedMutationDoesNotReset(t *testing.T) {
	b := NewExplorationBudget(2)
	exhaustBudget(b, 2)

	if !b.nudgeActive {
		t.Fatal("precondition: nudgeActive should be true")
	}

	toolUses := []ToolUseContent{{Name: "edit_file"}}
	results := []ToolResultContent{{ToolUseID: "2", Content: "permission denied", IsError: true}}
	b.Consume(toolUses, results)

	if !b.nudgeActive {
		t.Error("nudgeActive should remain true after failed mutation")
	}
	if b.remaining > 0 {
		t.Errorf("budget should not be restored after failed mutation, got remaining=%d", b.remaining)
	}
}

// EC2: Budget can be exhausted, reset, and exhausted again.
func TestMultipleExhaustionCycles(t *testing.T) {
	b := NewExplorationBudget(2)

	// First cycle
	exhaustBudget(b, 2)
	if b.ActiveNudge() == "" {
		t.Fatal("cycle 1: expected nudge on exhaustion")
	}

	// Reset via mutation
	b.Consume(
		[]ToolUseContent{{Name: "edit_file"}},
		[]ToolResultContent{{ToolUseID: "x", Content: "ok"}},
	)
	if b.nudgeActive {
		t.Fatal("cycle 1: nudgeActive should be false after mutation")
	}

	// Second cycle
	exhaustBudget(b, 2)
	if b.ActiveNudge() == "" {
		t.Fatal("cycle 2: expected nudge on second exhaustion")
	}
	if !b.nudgeActive {
		t.Error("cycle 2: nudgeActive should be true")
	}
}

// ─── System prompt injection ──────────────────────────────────────────────────

// TestNudgeNotInjectedIntoConversationHistory verifies that nudge is surfaced via
// ActiveNudge() only, and NOT injected as a user message in conversation history.
func TestNudgeNotInjectedIntoConversationHistory(t *testing.T) {
	b := NewExplorationBudget(2)
	memory := NewBufferMemory()

	memory.Add(NewUserMessage("task"))
	memory.Add(NewAssistantMessage("ok"))

	exhaustBudget(b, 2)

	// Nudge should be in ActiveNudge, not in conversation
	if b.ActiveNudge() == "" {
		t.Fatal("expected nudge after exhaustion")
	}

	msgs := memory.Messages()
	if countPrefix(msgs, NudgePrefix) > 0 {
		t.Error("nudge should NOT appear in conversation history")
	}
}

// TestActiveNudgeClearedAfterMutation verifies ActiveNudge returns "" after reset.
func TestActiveNudgeClearedAfterMutation(t *testing.T) {
	b := NewExplorationBudget(2)
	exhaustBudget(b, 2)

	nudge := b.ActiveNudge()
	if nudge == "" {
		t.Fatal("expected nudge after exhaustion")
	}
	if !strings.HasPrefix(nudge, NudgePrefix) {
		t.Errorf("nudge missing prefix: %q", nudge)
	}

	// Successful mutation
	b.Consume(
		[]ToolUseContent{{Name: "edit_file"}},
		[]ToolResultContent{{ToolUseID: "x", Content: "ok"}},
	)

	if b.ActiveNudge() != "" {
		t.Errorf("ActiveNudge should be empty after mutation, got %q", b.ActiveNudge())
	}
}

// ─── Compression: nudge no longer needs keepCondition ────────────────────────

// TestConversationHistoryStaysCleanAfterCompression verifies that after compression,
// no NudgePrefix appears in conversation history (since nudge lives in system prompt).
func TestConversationHistoryStaysCleanAfterCompression(t *testing.T) {
	memory := NewCompressMemory(5, 20)
	budget := NewExplorationBudget(2)

	memory.Add(NewUserMessage("task"))
	memory.Add(NewAssistantMessage("ok"))

	exhaustBudget(budget, 2)
	// Budget is exhausted; nudge is available via ActiveNudge()
	// In the new design, Session would inject nudge via systemSuffix, NOT memory.Add

	// Trigger compression with fillers (do NOT add nudge to memory)
	addFillers(memory, 10)

	msgs := memory.Messages()
	if countPrefix(msgs, NudgePrefix) > 0 {
		t.Error("nudge should never appear in conversation history under new design")
	}
}

// ─── Multiple keepConditions ──────────────────────────────────────────────────

// TestKeepConditionMultiple verifies that multiple keepConditions coexist correctly.
func TestKeepConditionMultiple(t *testing.T) {
	memory := NewCompressMemory(3, 20)

	memory.AddKeepCondition(func(msg Message) bool {
		return strings.Contains(msg.Text(), "[IMPORTANT]")
	})
	memory.AddKeepCondition(func(msg Message) bool {
		return strings.Contains(msg.Text(), "[CRITICAL]")
	})

	memory.Add(NewUserMessage("normal 1"))
	memory.Add(NewUserMessage("[IMPORTANT] keep this"))
	memory.Add(NewUserMessage("normal 2"))
	memory.Add(NewUserMessage("[CRITICAL] keep this too"))
	memory.Add(NewUserMessage("normal 3"))

	addFillers(memory, 9) // trigger compression

	msgs := memory.Messages()

	foundImportant := false
	foundCritical := false
	for _, msg := range msgs {
		text := msg.Text()
		if strings.Contains(text, "[IMPORTANT]") {
			foundImportant = true
		}
		if strings.Contains(text, "[CRITICAL]") {
			foundCritical = true
		}
	}

	if !foundImportant {
		t.Error("[IMPORTANT] message was not preserved")
	}
	if !foundCritical {
		t.Error("[CRITICAL] message was not preserved")
	}
}

// TestKeepConditionPlacement verifies pinned messages appear before recent window.
func TestKeepConditionPlacement(t *testing.T) {
	// recentWindow=3, maxMessages=20 — well-separated so recent window is predictable
	memory := NewCompressMemory(3, 20)

	memory.AddKeepCondition(func(msg Message) bool {
		return strings.Contains(msg.Text(), "[PIN]")
	})

	memory.Add(NewUserMessage("first"))
	memory.Add(NewUserMessage("[PIN] pinned"))

	// Add enough to push [PIN] into middle and trigger compression
	addFillers(memory, 9) // 18 more → 20 total (not yet triggered)
	memory.Add(NewUserMessage("recent1"))
	memory.Add(NewUserMessage("recent2"))
	memory.Add(NewUserMessage("recent3")) // 23 total → compression triggers

	msgs := memory.Messages()

	t.Logf("Total messages after compression: %d", len(msgs))
	for i, msg := range msgs {
		t.Logf("  [%d] %s", i, msg.Text())
	}

	pinnedIdx := -1
	recent3Idx := -1
	for i, msg := range msgs {
		text := msg.Text()
		if strings.Contains(text, "[PIN]") {
			pinnedIdx = i
		}
		if text == "recent3" {
			recent3Idx = i
		}
	}

	if pinnedIdx == -1 {
		t.Fatal("pinned message not found after compression")
	}
	if recent3Idx == -1 {
		t.Fatal("recent3 not found — it should be in the recent window")
	}
	if pinnedIdx >= recent3Idx {
		t.Errorf("pinned (idx=%d) should appear before recent3 (idx=%d)", pinnedIdx, recent3Idx)
	}
}

// ─── Drop condition ───────────────────────────────────────────────────────────

// TestDropConditionPriorityOverKeep verifies that keep wins when both conditions match.
func TestDropConditionPriorityOverKeep(t *testing.T) {
	memory := NewCompressMemory(3, 20)

	// Both keep and drop match the same message
	memory.AddKeepCondition(func(msg Message) bool {
		return strings.Contains(msg.Text(), "BOTH")
	})
	memory.AddDropCondition(func(msg Message) bool {
		return strings.Contains(msg.Text(), "BOTH")
	})

	memory.Add(NewUserMessage("first"))
	memory.Add(NewUserMessage("BOTH: keep wins"))

	addFillers(memory, 9) // trigger compression

	msgs := memory.Messages()
	found := false
	for _, msg := range msgs {
		if strings.Contains(msg.Text(), "BOTH") {
			found = true
			break
		}
	}
	if !found {
		t.Error("keep should win over drop when both conditions match")
	}
}

// TestDropConditionRemovesStaleMessage verifies that a message matching dropCondition
// (but not keepCondition) is silently discarded during compression.
func TestDropConditionRemovesStaleMessage(t *testing.T) {
	memory := NewCompressMemory(3, 20)

	memory.AddDropCondition(func(msg Message) bool {
		return strings.Contains(msg.Text(), "[STALE]")
	})

	memory.Add(NewUserMessage("first"))
	memory.Add(NewUserMessage("[STALE] this should be dropped"))

	addFillers(memory, 10) // trigger compression (21 total > maxMessages=20)

	msgs := memory.Messages()
	for _, msg := range msgs {
		if strings.Contains(msg.Text(), "[STALE]") {
			t.Error("[STALE] message should have been dropped during compression")
		}
	}
}

// ─── Agent-level integration ──────────────────────────────────────────────────

// TestAgentIntegrationWithExplorationBudget verifies that Agent.NewSession wires
// ExplorationBudget correctly (no longer wires keepCondition/dropCondition).
func TestAgentIntegrationWithExplorationBudget(t *testing.T) {
	agent := New(
		WithExplorationBudget(2),
		WithMemoryFactory(func() Memory {
			return NewCompressMemory(5, 20)
		}),
	)

	session := agent.NewSession()

	if session.explorationBudget == nil {
		t.Fatal("explorationBudget not created")
	}

	// In new design, keepCondition/dropCondition are NOT registered for nudge management
	// (nudge lives in system prompt now)
	cm, ok := session.memory.(*CompressMemory)
	if !ok {
		t.Fatal("memory is not CompressMemory")
	}
	if len(cm.keepConditions) != 0 {
		t.Errorf("expected no keepConditions (nudge moved to system prompt), got %d", len(cm.keepConditions))
	}
	if len(cm.dropConditions) != 0 {
		t.Errorf("expected no dropConditions (nudge moved to system prompt), got %d", len(cm.dropConditions))
	}

	// Exhaust budget and verify nudge is accessible via ActiveNudge
	exhaustBudget(session.explorationBudget, 2)
	if session.explorationBudget.ActiveNudge() == "" {
		t.Error("expected nudge accessible via ActiveNudge after exhaustion")
	}

	// Verify conversation history is clean
	msgs := session.memory.Messages()
	if countPrefix(msgs, NudgePrefix) > 0 {
		t.Error("nudge should not appear in conversation history")
	}

	// Mutation → nudge should be cleared
	session.explorationBudget.Consume(
		[]ToolUseContent{{Name: "edit_file"}},
		[]ToolResultContent{{ToolUseID: "x", Content: "ok"}},
	)

	if session.explorationBudget.ActiveNudge() != "" {
		t.Error("nudge should be cleared after mutation")
	}
}

// TestNoExplorationBudgetNoConditions verifies that without WithExplorationBudget,
// no keepCondition or dropCondition is registered.
func TestNoExplorationBudgetNoConditions(t *testing.T) {
	agent := New(
		WithMemoryFactory(func() Memory {
			return NewCompressMemory(5, 20)
		}),
	)

	session := agent.NewSession()

	if session.explorationBudget != nil {
		t.Error("explorationBudget should be nil")
	}

	cm, ok := session.memory.(*CompressMemory)
	if !ok {
		t.Fatal("memory is not CompressMemory")
	}
	if len(cm.keepConditions) != 0 {
		t.Errorf("expected no keepConditions, got %d", len(cm.keepConditions))
	}
	if len(cm.dropConditions) != 0 {
		t.Errorf("expected no dropConditions, got %d", len(cm.dropConditions))
	}
}

// TestNonCompressMemoryNoRegistration verifies that non-CompressMemory doesn't panic.
func TestNonCompressMemoryNoRegistration(t *testing.T) {
	agent := New(
		WithExplorationBudget(3),
		WithMemoryFactory(func() Memory {
			return NewBufferMemory()
		}),
	)

	session := agent.NewSession()

	if session.explorationBudget == nil {
		t.Fatal("explorationBudget not created")
	}
	if _, ok := session.memory.(*CompressMemory); ok {
		t.Fatal("memory should not be CompressMemory")
	}
	// Must not panic
}

// EC1: Exhaustion immediately followed by mutation in the same logical turn.
func TestExhaustionThenImmediateMutation(t *testing.T) {
	b := NewExplorationBudget(1)

	// One read exhausts the budget
	exhaustBudget(b, 1)
	if b.ActiveNudge() == "" {
		t.Fatal("expected nudge after single read with budget=1")
	}
	if !b.nudgeActive {
		t.Fatal("nudgeActive should be true")
	}

	// Immediate successful mutation
	b.Consume(
		[]ToolUseContent{{Name: "edit_file"}},
		[]ToolResultContent{{ToolUseID: "x", Content: "ok"}},
	)

	if b.nudgeActive {
		t.Error("nudgeActive should be false after immediate mutation")
	}
	if b.remaining != 1 {
		t.Errorf("budget should be restored to 1, got %d", b.remaining)
	}
	if b.ActiveNudge() != "" {
		t.Error("ActiveNudge should be empty after mutation")
	}
}
