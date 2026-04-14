package cc

import (
	"context"
	"fmt"
)

const (
	defaultMaxTurns  = 10
	defaultMaxTokens = 4096
)

// Agent is the core runtime that orchestrates LLM calls and tool execution.
type Agent struct {
	provider  Provider
	tools     map[string]Tool
	system    string
	model     string
	maxTurns  int
	maxTokens int
	hooks     Hooks
	memory    Memory
}

// RunResult contains the outcome of an agent run.
type RunResult struct {
	// Output is the final text response from the agent.
	Output string
	// Messages is the complete conversation history for this run.
	Messages []Message
	// Turns is the number of LLM calls made.
	Turns int
	// Usage is the total token usage across all turns.
	Usage Usage
}

// New creates a new Agent with the given options.
func New(opts ...Option) *Agent {
	a := &Agent{
		tools:     make(map[string]Tool),
		maxTurns:  defaultMaxTurns,
		maxTokens: defaultMaxTokens,
		memory:    NewBufferMemory(),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Run executes the agent loop with the given user input.
//
// The loop:
//  1. Send messages + tool definitions to the LLM
//  2. If response contains tool_use → execute tools → append results → goto 1
//  3. If response is text only → return
//  4. If max turns exceeded → return with ErrMaxTurns
func (a *Agent) Run(ctx context.Context, input string) (*RunResult, error) {
	if input == "" {
		return nil, ErrEmptyInput
	}
	if a.provider == nil {
		return nil, ErrNoProvider
	}

	// Append user message to memory
	userMsg := NewUserMessage(input)
	a.memory.Add(userMsg)

	var totalUsage Usage

	for turn := range a.maxTurns {
		resp, err := a.step(ctx, a.memory.Messages())
		if err != nil {
			return nil, fmt.Errorf("turn %d: %w", turn+1, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)

		if a.hooks.OnModelResponse != nil {
			a.hooks.OnModelResponse(ctx, resp)
		}

		// Build assistant message from response
		assistantMsg := Message{Role: RoleAssistant, Content: resp.Content}
		a.memory.Add(assistantMsg)

		// Check for tool use
		toolUses := resp.ToolUses()
		if len(toolUses) == 0 {
			return &RunResult{
				Output:   resp.Text(),
				Messages: a.memory.Messages(),
				Turns:    turn + 1,
				Usage:    totalUsage,
			}, nil
		}

		// Execute tools and collect results
		results, err := a.executeTools(ctx, toolUses)
		if err != nil {
			return nil, fmt.Errorf("turn %d tools: %w", turn+1, err)
		}

		// Append tool results as a user message
		toolMsg := NewToolResultMessage(results...)
		a.memory.Add(toolMsg)
	}

	return &RunResult{
		Output:   "Max turns exceeded",
		Messages: a.memory.Messages(),
		Turns:    a.maxTurns,
		Usage:    totalUsage,
	}, ErrMaxTurns
}

// step makes a single LLM call with the current conversation state.
func (a *Agent) step(ctx context.Context, messages []Message) (*ChatResponse, error) {
	toolDefs := a.toolDefs()
	return a.provider.Chat(ctx, ChatParams{
		Model:     a.model,
		System:    a.system,
		Messages:  messages,
		Tools:     toolDefs,
		MaxTokens: a.maxTokens,
	})
}

// executeTools runs all tool calls from the LLM response.
func (a *Agent) executeTools(ctx context.Context, toolUses []ToolUseContent) ([]ToolResultContent, error) {
	results := make([]ToolResultContent, 0, len(toolUses))

	for _, tu := range toolUses {
		tool, ok := a.tools[tu.Name]
		if !ok {
			results = append(results, ToolResultContent{
				ToolUseID: tu.ID,
				Content:   fmt.Sprintf("tool %q not found", tu.Name),
				IsError:   true,
			})
			continue
		}

		// Before hook
		if a.hooks.BeforeToolCall != nil {
			if err := a.hooks.BeforeToolCall(ctx, tu.Name, tu.Input); err != nil {
				results = append(results, ToolResultContent{
					ToolUseID: tu.ID,
					Content:   fmt.Sprintf("tool call blocked: %s", err.Error()),
					IsError:   true,
				})
				continue
			}
		}

		output, err := tool.Execute(ctx, tu.Input)

		// After hook
		if a.hooks.AfterToolCall != nil {
			a.hooks.AfterToolCall(ctx, tu.Name, output, err)
		}

		if err != nil {
			results = append(results, ToolResultContent{
				ToolUseID: tu.ID,
				Content:   fmt.Sprintf("error: %s", err.Error()),
				IsError:   true,
			})
			continue
		}

		results = append(results, ToolResultContent{
			ToolUseID: tu.ID,
			Content:   output,
		})
	}

	return results, nil
}

// toolDefs returns the tool definitions for the LLM.
func (a *Agent) toolDefs() []ToolDef {
	if len(a.tools) == 0 {
		return nil
	}
	defs := make([]ToolDef, 0, len(a.tools))
	for _, t := range a.tools {
		defs = append(defs, ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

// AddTool registers a tool with the agent.
func (a *Agent) AddTool(t Tool) {
	a.tools[t.Name()] = t
}

// SetSystem updates the system prompt.
func (a *Agent) SetSystem(system string) {
	a.system = system
}

// ClearMemory resets the conversation history.
func (a *Agent) ClearMemory() {
	a.memory.Clear()
}

// Messages returns the current conversation history.
func (a *Agent) Messages() []Message {
	return a.memory.Messages()
}
