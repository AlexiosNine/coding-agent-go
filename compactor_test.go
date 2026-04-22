package cc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

// compactorMockProvider implements cc.Provider for testing LLMCompactor.
// Separate from the mockProvider in agent_test.go to track call details.
type compactorMockProvider struct {
	response *cc.ChatResponse
	err      error
	called   bool
	params   cc.ChatParams
}

func (m *compactorMockProvider) Chat(_ context.Context, params cc.ChatParams) (*cc.ChatResponse, error) {
	m.called = true
	m.params = params
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func TestRuleCompactor_Compact(t *testing.T) {
	compactor := &cc.RuleCompactor{}

	msgs := []cc.Message{
		cc.NewUserMessage("hello"),
		cc.NewAssistantMessage("hi there"),
		cc.NewUserMessage("edit file.go"),
		cc.NewAssistantMessage("done"),
	}

	result, err := compactor.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == "" {
		t.Error("expected non-empty result from RuleCompactor")
	}
}

func TestLLMCompactor_Compact(t *testing.T) {
	provider := &compactorMockProvider{
		response: &cc.ChatResponse{
			Content: []cc.Content{
				cc.TextContent{Text: "Summary: User edited file.go and fixed a bug."},
			},
			StopReason: "end_turn",
		},
	}

	compactor := cc.NewLLMCompactor(provider, "test-model")

	msgs := []cc.Message{
		cc.NewUserMessage("fix the bug in file.go"),
		cc.NewAssistantMessage("I found the issue and fixed it"),
		cc.NewUserMessage("thanks"),
	}

	result, err := compactor.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !provider.called {
		t.Error("expected provider.Chat to be called")
	}

	if provider.params.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", provider.params.Model)
	}

	if !strings.Contains(result, "Summary") {
		t.Errorf("expected summary in result, got %q", result)
	}
}

func TestLLMCompactor_Error(t *testing.T) {
	provider := &compactorMockProvider{
		err: errors.New("api error"),
	}

	compactor := cc.NewLLMCompactor(provider, "test-model")

	msgs := []cc.Message{
		cc.NewUserMessage("hello"),
	}

	_, err := compactor.Compact(context.Background(), msgs)
	if err == nil {
		t.Error("expected error from LLMCompactor")
	}
	if !strings.Contains(err.Error(), "llm compactor") {
		t.Errorf("expected wrapped error, got %q", err.Error())
	}
}

func TestLLMCompactor_PromptContainsMessages(t *testing.T) {
	provider := &compactorMockProvider{
		response: &cc.ChatResponse{
			Content: []cc.Content{
				cc.TextContent{Text: "summary"},
			},
			StopReason: "end_turn",
		},
	}

	compactor := cc.NewLLMCompactor(provider, "test-model")

	msgs := []cc.Message{
		cc.NewUserMessage("edit /path/to/file.go"),
		cc.NewAssistantMessage("done editing"),
	}

	_, err := compactor.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the prompt sent to the LLM contains the message content
	promptMsg := provider.params.Messages[0].Text()
	if !strings.Contains(promptMsg, "/path/to/file.go") {
		t.Errorf("expected prompt to contain file path, got %q", promptMsg)
	}
	if !strings.Contains(promptMsg, "[user]") {
		t.Errorf("expected prompt to contain role markers, got %q", promptMsg)
	}
}

func TestCompressMemory_WithLLMCompactor(t *testing.T) {
	provider := &compactorMockProvider{
		response: &cc.ChatResponse{
			Content: []cc.Content{
				cc.TextContent{Text: "LLM summary of conversation"},
			},
			StopReason: "end_turn",
		},
	}

	mem := cc.NewCompressMemory(2, 10)
	mem.SetCompactor(cc.NewLLMCompactor(provider, "test-model"))

	// Add 11 messages to trigger compression
	for i := range 11 {
		mem.Add(cc.NewUserMessage(strings.Repeat("x", 100) + string(rune('A'+i))))
	}

	// Should have compressed
	if mem.Len() >= 11 {
		t.Errorf("expected compression, got %d messages", mem.Len())
	}

	if !provider.called {
		t.Error("expected LLM compactor to be called during compression")
	}

	// Verify the compressed summary contains the LLM marker
	found := false
	for _, msg := range mem.Messages() {
		if strings.Contains(msg.Text(), "LLM-compressed") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected LLM-compressed marker in summary message")
	}
}

func TestCompressMemory_FallbackOnLLMError(t *testing.T) {
	provider := &compactorMockProvider{
		err: errors.New("api error"),
	}

	mem := cc.NewCompressMemory(2, 10)
	mem.SetCompactor(cc.NewLLMCompactor(provider, "test-model"))

	// Add 11 messages to trigger compression
	for i := range 11 {
		mem.Add(cc.NewUserMessage(strings.Repeat("x", 100) + string(rune('A'+i))))
	}

	// Should still compress (fallback to rule-based)
	if mem.Len() >= 11 {
		t.Errorf("expected fallback compression, got %d messages", mem.Len())
	}

	// Should NOT contain LLM marker (fell back to rule-based)
	for _, msg := range mem.Messages() {
		if strings.Contains(msg.Text(), "LLM-compressed") {
			t.Error("should not contain LLM marker when LLM failed")
		}
	}
}

func TestCompressMemory_NilCompactorUsesRuleBased(t *testing.T) {
	mem := cc.NewCompressMemory(2, 10)
	// No compactor set — should use rule-based (existing behavior)

	for i := range 11 {
		mem.Add(cc.NewUserMessage(strings.Repeat("x", 100) + string(rune('A'+i))))
	}

	if mem.Len() >= 11 {
		t.Errorf("expected rule-based compression, got %d messages", mem.Len())
	}
}
