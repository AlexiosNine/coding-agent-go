package cc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// AgentTool wraps an Agent as a Tool, enabling LLM-driven sub-agent delegation.
type AgentTool struct {
	name           string
	desc           string
	agent          *Agent
	contextBuilder func(parentMessages []Message) string // inject parent context
}

// AgentToolOption configures an AgentTool.
type AgentToolOption func(*AgentTool)

// WithContextBuilder sets a function that builds context from the parent's messages.
// The returned string is prepended to the sub-agent's system prompt.
func WithContextBuilder(fn func(parentMessages []Message) string) AgentToolOption {
	return func(t *AgentTool) { t.contextBuilder = fn }
}

type agentToolInput struct {
	Task    string `json:"task" desc:"The task to delegate to the sub-agent"`
	Context string `json:"context" desc:"Additional context from the parent agent (optional)"`
}

// AsAgentTool creates a Tool that delegates to another Agent.
// The sub-agent runs in its own session with independent memory.
//
// Communication:
//   - Input: task string + optional context string from parent LLM
//   - Output: structured AgentToolResult (output, metadata, message count, token usage)
//   - Shared state: use SharedState via context for cross-agent key-value sharing
//   - Context injection: use WithContextBuilder to inject parent conversation context
//
// Example:
//
//	researcher := cc.New(cc.WithProvider(p), cc.WithSystem("You are a researcher"))
//	agent := cc.New(
//	    cc.WithProvider(p),
//	    cc.WithTools(cc.AsAgentTool("researcher", "Research a topic", researcher)),
//	)
func AsAgentTool(name, description string, agent *Agent, opts ...AgentToolOption) *AgentTool {
	t := &AgentTool{name: name, desc: description, agent: agent}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *AgentTool) Name() string       { return t.name }
func (t *AgentTool) Description() string { return t.desc }

func (t *AgentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"The task to delegate to the sub-agent"},"context":{"type":"string","description":"Additional context from the conversation (optional)"}},"required":["task"]}`)
}

func (t *AgentTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params agentToolInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("subagent %s: unmarshal input: %w", t.name, err)
	}
	if params.Task == "" {
		return "", fmt.Errorf("subagent %s: empty task", t.name)
	}

	// Create a session for the sub-agent
	session := t.agent.NewSession()

	// Inject parent context into system suffix (not task message) so it survives
	// compression in long sub-agent conversations.
	if t.contextBuilder != nil {
		// Get parent session's messages from the shared state if available
		if parentSession := getParentSession(ctx); parentSession != nil {
			contextStr := t.contextBuilder(parentSession.Messages())
			if contextStr != "" {
				session.systemSuffix = "\n\n## Parent Context\n" + contextStr
			}
		}
	}

	// Inject explicit context from parent LLM
	taskInput := params.Task
	if params.Context != "" {
		taskInput = "Context: " + params.Context + "\n\nTask: " + params.Task
	}

	result, err := session.Run(ctx, taskInput)
	if err != nil {
		return "", fmt.Errorf("subagent %s: %w", t.name, err)
	}

	// Return structured result as JSON
	structured := AgentToolResult{
		Output:       result.Output,
		Turns:        result.Turns,
		MessageCount: len(result.Messages),
		Usage:        result.Usage,
	}
	data, _ := json.Marshal(structured)
	return string(data), nil
}

// AgentToolResult is the structured result returned by a sub-agent.
type AgentToolResult struct {
	Output       string `json:"output"`
	Turns        int    `json:"turns"`
	MessageCount int    `json:"message_count"`
	Usage        Usage  `json:"usage"`
}

// --- Shared State ---

type sharedStateKey struct{}

// SharedState is a thread-safe key-value store shared across agents.
type SharedState struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewSharedState creates a new shared state.
func NewSharedState() *SharedState {
	return &SharedState{data: make(map[string]any)}
}

// Set stores a value.
func (s *SharedState) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

// Get retrieves a value. Returns nil if not found.
func (s *SharedState) Get(key string) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

// GetString retrieves a string value. Returns "" if not found or not a string.
func (s *SharedState) GetString(key string) string {
	v := s.Get(key)
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// WithSharedState attaches a SharedState to a context.
func WithSharedState(ctx context.Context, state *SharedState) context.Context {
	return context.WithValue(ctx, sharedStateKey{}, state)
}

// GetSharedState retrieves the SharedState from a context.
func GetSharedState(ctx context.Context) *SharedState {
	if v := ctx.Value(sharedStateKey{}); v != nil {
		return v.(*SharedState)
	}
	return nil
}

// --- Parent Session tracking ---

type parentSessionKey struct{}

func withParentSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, parentSessionKey{}, s)
}

func getParentSession(ctx context.Context) *Session {
	if v := ctx.Value(parentSessionKey{}); v != nil {
		return v.(*Session)
	}
	return nil
}
