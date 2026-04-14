package cc

import (
	"context"
	"encoding/json"
	"fmt"
)

// AgentTool wraps an Agent as a Tool, enabling LLM-driven sub-agent delegation.
// When the parent LLM calls this tool, it runs the sub-agent with the given task
// and returns the sub-agent's output.
type AgentTool struct {
	name  string
	desc  string
	agent *Agent
}

type agentToolInput struct {
	Task string `json:"task" desc:"The task to delegate to the sub-agent"`
}

// AsAgentTool creates a Tool that delegates to another Agent.
// The sub-agent runs in its own session with independent memory.
//
// Example:
//
//	researcher := cc.New(cc.WithProvider(p), cc.WithSystem("You are a researcher"))
//	agent := cc.New(
//	    cc.WithProvider(p),
//	    cc.WithTools(cc.AsAgentTool("researcher", "Research a topic in depth", researcher)),
//	)
func AsAgentTool(name, description string, agent *Agent) *AgentTool {
	return &AgentTool{name: name, desc: description, agent: agent}
}

func (t *AgentTool) Name() string        { return t.name }
func (t *AgentTool) Description() string  { return t.desc }

func (t *AgentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"The task to delegate to the sub-agent"}},"required":["task"]}`)
}

func (t *AgentTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params agentToolInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("subagent %s: unmarshal input: %w", t.name, err)
	}
	if params.Task == "" {
		return "", fmt.Errorf("subagent %s: empty task", t.name)
	}

	result, err := t.agent.Run(ctx, params.Task)
	if err != nil {
		return "", fmt.Errorf("subagent %s: %w", t.name, err)
	}
	return result.Output, nil
}
