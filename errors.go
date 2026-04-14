package cc

import "errors"

var (
	// ErrNoProvider is returned when an Agent has no provider configured.
	ErrNoProvider = errors.New("agent: no provider configured")

	// ErrMaxTurns is returned when the agent loop exceeds the maximum number of turns.
	ErrMaxTurns = errors.New("agent: max turns exceeded")

	// ErrToolNotFound is returned when a tool_use references an unknown tool.
	ErrToolNotFound = errors.New("agent: tool not found")

	// ErrEmptyInput is returned when Run is called with an empty input string.
	ErrEmptyInput = errors.New("agent: empty input")

	// ErrProviderRequest is returned when the LLM provider returns an API error.
	ErrProviderRequest = errors.New("provider: request failed")

	// ErrToolExecution is returned when a tool fails during execution.
	ErrToolExecution = errors.New("tool: execution failed")
)

// ToolError wraps an error from tool execution with the tool name.
type ToolError struct {
	ToolName string
	Err      error
}

func (e *ToolError) Error() string {
	return "tool " + e.ToolName + ": " + e.Err.Error()
}

func (e *ToolError) Unwrap() error {
	return e.Err
}
