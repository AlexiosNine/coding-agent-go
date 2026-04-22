package cc

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

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

// ErrStreamNotSupported is returned when streaming is requested but the provider doesn't support it.
var ErrStreamNotSupported = errors.New("provider: streaming not supported")

// ProviderError contains structured API error information.
type ProviderError struct {
	Provider   string
	StatusCode int
	Type       string // "rate_limit", "auth", "server", "overloaded"
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("%s: %s (status %d)", e.Provider, e.Message, e.StatusCode)
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

// IsRetryable returns true if the error is retryable.
// It recognizes ProviderError (for LLM calls) and TransientToolError (for tool execution).
func IsRetryable(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable
	}
	var te *TransientToolError
	if errors.As(err, &te) {
		return true
	}
	return false
}

// TransientToolError marks a tool execution error as transient (retryable).
type TransientToolError struct {
	Err error
}

func (e *TransientToolError) Error() string { return e.Err.Error() }
func (e *TransientToolError) Unwrap() error { return e.Err }

// isTransientToolError checks whether a tool execution error is transient
// (network timeout, connection refused, rate limit, etc.).
func isTransientToolError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "temporary") ||
		strings.Contains(msg, "unavailable") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "503")
}

// IsRateLimited returns true if the error is a rate limit ProviderError.
func IsRateLimited(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Type == "rate_limit"
	}
	return false
}

// ClassifyHTTPStatus returns the error type and retryable flag for an HTTP status code.
func ClassifyHTTPStatus(status int) (errType string, retryable bool) {
	switch {
	case status == 429:
		return "rate_limit", true
	case status == 401 || status == 403:
		return "auth", false
	case status == 529:
		return "overloaded", true
	case status >= 500:
		return "server", true
	default:
		return "client", false
	}
}
