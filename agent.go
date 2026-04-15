package cc

import (
	"context"
	"io"
)

const (
	defaultMaxTurns  = 10
	defaultMaxTokens = 4096
)

// Agent is the core runtime that orchestrates LLM calls and tool execution.
// Agent itself is stateless — conversation state lives in Session.
type Agent struct {
	provider            Provider
	tools               map[string]Tool
	cachedToolDefs      []ToolDef
	system              string
	model               string
	maxTurns            int
	maxTokens           int
	maxConcurrency      int
	maxExplorationTurns int // abort if this many consecutive turns have no edit/write (0 = disabled)
	retry               *RetryConfig
	hooks               Hooks
	memoryFactory       func() Memory
	approver            Approver   // tool approval strategy
	sandbox             *Sandbox   // file/command access restrictions
	osSandbox           *OSSandbox // OS-level sandbox (opt-in)
	closers             []io.Closer // MCP clients and other resources to clean up
}

// RunResult contains the outcome of an agent run.
type RunResult struct {
	Output   string
	Messages []Message
	Turns    int
	Usage    Usage
}

// New creates a new Agent with the given options.
func New(opts ...Option) *Agent {
	a := &Agent{
		tools:         make(map[string]Tool),
		maxTurns:      defaultMaxTurns,
		maxTokens:     defaultMaxTokens,
		memoryFactory: func() Memory { return NewBufferMemory() },
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// NewSession creates a new conversation session with independent memory.
func (a *Agent) NewSession() *Session {
	return &Session{
		agent:        a,
		memory:       a.memoryFactory(),
		outputBuffer: NewOutputBuffer(50 << 20),
	}
}

// Run is a convenience method for single-shot use.
// It creates a temporary session, runs the input, and returns the result.
func (a *Agent) Run(ctx context.Context, input string) (*RunResult, error) {
	return a.NewSession().Run(ctx, input)
}

// AddTool registers a tool with the agent.
func (a *Agent) AddTool(t Tool) {
	a.tools[t.Name()] = t
	a.cachedToolDefs = nil
}

// SetSystem updates the system prompt.
func (a *Agent) SetSystem(system string) {
	a.system = system
}

// toolDefs returns the tool definitions for the LLM.
func (a *Agent) toolDefs() []ToolDef {
	if len(a.tools) == 0 {
		return nil
	}
	if a.cachedToolDefs == nil {
		defs := make([]ToolDef, 0, len(a.tools))
		for _, t := range a.tools {
			defs = append(defs, ToolDef{
				Name:        t.Name(),
				Description: t.Description(),
				InputSchema: t.InputSchema(),
			})
		}
		a.cachedToolDefs = defs
	}
	return a.cachedToolDefs
}

// DebugToolDefsForTest exposes cached tool defs for tests only.
func (a *Agent) DebugToolDefsForTest() []ToolDef {
	return a.toolDefs()
}

// Close shuts down all MCP clients and other resources.
func (a *Agent) Close() error {
	var firstErr error
	for _, c := range a.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
