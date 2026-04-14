package cc

import (
	"context"
	"fmt"
)

// Session holds the state for a single conversation.
// Create via Agent.NewSession().
type Session struct {
	agent  *Agent
	memory Memory
}

// Run executes the agent loop within this session's conversation context.
func (s *Session) Run(ctx context.Context, input string) (*RunResult, error) {
	if input == "" {
		return nil, ErrEmptyInput
	}
	if s.agent.provider == nil {
		return nil, ErrNoProvider
	}

	s.memory.Add(NewUserMessage(input))

	var totalUsage Usage

	for turn := range s.agent.maxTurns {
		resp, err := s.step(ctx)
		if err != nil {
			return nil, fmt.Errorf("turn %d: %w", turn+1, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)

		if s.agent.hooks.OnModelResponse != nil {
			s.agent.hooks.OnModelResponse(ctx, resp)
		}

		s.memory.Add(Message{Role: RoleAssistant, Content: resp.Content})

		toolUses := resp.ToolUses()
		if len(toolUses) == 0 {
			return &RunResult{
				Output:   resp.Text(),
				Messages: s.memory.Messages(),
				Turns:    turn + 1,
				Usage:    totalUsage,
			}, nil
		}

		results := s.executeTools(ctx, toolUses)
		s.memory.Add(NewToolResultMessage(results...))
	}

	return &RunResult{
		Output:   "Max turns exceeded",
		Messages: s.memory.Messages(),
		Turns:    s.agent.maxTurns,
		Usage:    totalUsage,
	}, ErrMaxTurns
}

// step makes a single LLM call with the current conversation state.
func (s *Session) step(ctx context.Context) (*ChatResponse, error) {
	return s.agent.provider.Chat(ctx, ChatParams{
		Model:     s.agent.model,
		System:    s.agent.system,
		Messages:  s.memory.Messages(),
		Tools:     s.agent.toolDefs(),
		MaxTokens: s.agent.maxTokens,
	})
}

// executeTools runs all tool calls from the LLM response.
func (s *Session) executeTools(ctx context.Context, toolUses []ToolUseContent) []ToolResultContent {
	results := make([]ToolResultContent, 0, len(toolUses))
	for _, tu := range toolUses {
		results = append(results, s.executeSingleTool(ctx, tu))
	}
	return results
}

// executeSingleTool runs a single tool call.
func (s *Session) executeSingleTool(ctx context.Context, tu ToolUseContent) ToolResultContent {
	tool, ok := s.agent.tools[tu.Name]
	if !ok {
		return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("tool %q not found", tu.Name), IsError: true}
	}

	if s.agent.hooks.BeforeToolCall != nil {
		if err := s.agent.hooks.BeforeToolCall(ctx, tu.Name, tu.Input); err != nil {
			return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("tool call blocked: %s", err.Error()), IsError: true}
		}
	}

	output, err := tool.Execute(ctx, tu.Input)

	if s.agent.hooks.AfterToolCall != nil {
		s.agent.hooks.AfterToolCall(ctx, tu.Name, output, err)
	}

	if err != nil {
		return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("error: %s", err.Error()), IsError: true}
	}
	return ToolResultContent{ToolUseID: tu.ID, Content: output}
}

// Messages returns the session's conversation history.
func (s *Session) Messages() []Message {
	return s.memory.Messages()
}

// ClearMemory resets the session's conversation history.
func (s *Session) ClearMemory() {
	s.memory.Clear()
}
