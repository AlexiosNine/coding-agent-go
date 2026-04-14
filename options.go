package cc

import (
	"context"
	"fmt"

	"github.com/alexioschen/cc-connect/goagent/mcp"
)

// Option configures an Agent.
type Option func(*Agent)

// WithProvider sets the LLM provider.
func WithProvider(p Provider) Option {
	return func(a *Agent) { a.provider = p }
}

// WithTools registers tools that the agent can call.
func WithTools(tools ...Tool) Option {
	return func(a *Agent) {
		for _, t := range tools {
			a.tools[t.Name()] = t
		}
	}
}

// WithSystem sets the system prompt.
func WithSystem(system string) Option {
	return func(a *Agent) { a.system = system }
}

// WithModel sets the model name.
func WithModel(model string) Option {
	return func(a *Agent) { a.model = model }
}

// WithMaxTurns sets the maximum number of agent loop turns.
// A turn is one LLM call + optional tool executions.
// Default is 10.
func WithMaxTurns(n int) Option {
	return func(a *Agent) { a.maxTurns = n }
}

// WithMaxTokens sets the maximum tokens for each LLM response.
// Default is 4096.
func WithMaxTokens(n int) Option {
	return func(a *Agent) { a.maxTokens = n }
}

// WithHooks sets lifecycle hooks for the agent loop.
func WithHooks(h Hooks) Option {
	return func(a *Agent) { a.hooks = h }
}

// WithMemoryFactory sets the factory function used to create per-session memory.
// Default creates an unbounded BufferMemory.
func WithMemoryFactory(f func() Memory) Option {
	return func(a *Agent) { a.memoryFactory = f }
}

// WithMaxConcurrency limits the number of tools that can execute in parallel.
// Default is 0 (unlimited).
func WithMaxConcurrency(n int) Option {
	return func(a *Agent) { a.maxConcurrency = n }
}

// WithRetry sets the retry configuration for provider calls.
func WithRetry(cfg RetryConfig) Option {
	return func(a *Agent) { a.retry = &cfg }
}

// WithMaxRetries enables retry with the given max attempts and default delays.
func WithMaxRetries(n int) Option {
	return func(a *Agent) {
		cfg := DefaultRetryConfig()
		cfg.MaxRetries = n
		a.retry = &cfg
	}
}

// WithMCPServer connects to an MCP Server via stdio transport.
// It starts the subprocess, initializes the MCP connection, discovers tools,
// and registers them with the agent. Call Agent.Close() to shut down.
func WithMCPServer(command string, args ...string) Option {
	return func(a *Agent) {
		transport, err := mcp.NewStdioTransport(command, args...)
		if err != nil {
			panic(fmt.Sprintf("mcp: start server %q: %v", command, err))
		}

		client := mcp.NewClient(transport, mcp.ClientInfo{Name: "goagent", Version: "1.0.0"})

		ctx := context.Background()
		if _, err := client.Initialize(ctx); err != nil {
			_ = client.Close()
			panic(fmt.Sprintf("mcp: initialize %q: %v", command, err))
		}

		tools, err := client.ListTools(ctx)
		if err != nil {
			_ = client.Close()
			panic(fmt.Sprintf("mcp: list tools %q: %v", command, err))
		}

		for _, info := range tools {
			a.tools[info.Name] = mcp.NewMCPTool(client, info)
		}
		a.closers = append(a.closers, client)
	}
}

// WithMCPClient registers a pre-configured MCP Client's tools with the agent.
// The caller must have already called client.Initialize() and client.ListTools().
func WithMCPClient(client *mcp.Client, tools []mcp.ToolInfo) Option {
	return func(a *Agent) {
		for _, info := range tools {
			a.tools[info.Name] = mcp.NewMCPTool(client, info)
		}
		a.closers = append(a.closers, client)
	}
}
