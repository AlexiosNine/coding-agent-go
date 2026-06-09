package cc

import (
	"context"
	"time"
)

const (
	EventRunStarted       = "run_started"
	EventModelResponse    = "model_response"
	EventToolCallStarted  = "tool_call_started"
	EventToolCallFinished = "tool_call_finished"
	EventGuardTriggered   = "guard_triggered"
	EventRunFinished      = "run_finished"
)

const (
	StopReasonSuccessText    = "success_text"
	StopReasonMaxTurns       = "max_turns"
	StopReasonStoppedByGuard = "stopped_by_guard"
	StopReasonToolError      = "tool_error"
	StopReasonProviderError  = "provider_error"
)

// AgentEvent is a structured trajectory event emitted during a run.
type AgentEvent struct {
	TS           time.Time `json:"ts"`
	RunID        string    `json:"run_id,omitempty"`
	InstanceID   string    `json:"instance_id,omitempty"`
	Event        string    `json:"event"`
	Turn         int       `json:"turn,omitempty"`
	Tool         string    `json:"tool,omitempty"`
	IsError      bool      `json:"is_error,omitempty"`
	InputChars   int       `json:"input_chars,omitempty"`
	InputPreview string    `json:"input_preview,omitempty"`
	OutputChars  int       `json:"output_chars,omitempty"`
	StopReason   string    `json:"stop_reason,omitempty"`
	GuardReason  string    `json:"guard_reason,omitempty"`
	Usage        Usage     `json:"usage,omitempty"`
}

// EventSink receives structured agent trajectory events.
type EventSink interface {
	EmitAgentEvent(ctx context.Context, event AgentEvent)
}

// EventSinkFunc adapts a function into an EventSink.
type EventSinkFunc func(ctx context.Context, event AgentEvent)

func (f EventSinkFunc) EmitAgentEvent(ctx context.Context, event AgentEvent) {
	f(ctx, event)
}

// ConvergenceGuardConfig controls hard-stop trajectory convergence guards.
type ConvergenceGuardConfig struct {
	Enabled                     bool
	MaxConsecutiveReadOnlyTurns int
	StopOnRepeatedRead          bool
	StopOnBudgetExhausted       bool
	StopReadOnlyAfterEdit       bool
}

// DefaultConvergenceGuardConfig returns the default hard-stop policy.
func DefaultConvergenceGuardConfig() ConvergenceGuardConfig {
	return ConvergenceGuardConfig{
		Enabled:                     true,
		MaxConsecutiveReadOnlyTurns: 8,
		StopOnRepeatedRead:          true,
		StopOnBudgetExhausted:       true,
		StopReadOnlyAfterEdit:       true,
	}
}

// RunMetrics aggregates trajectory and tool-call metrics for a run.
type RunMetrics struct {
	ToolCallCount           int
	ReadOnlyCount           int
	MutationCount           int
	GuardTriggers           int
	ReadOnlyAfterEditBlocks int
	FirstEditTurn           int
	GuardReason             string
	GrepCalls               int
	ReadFileCalls           int
	EditFileCalls           int
}
