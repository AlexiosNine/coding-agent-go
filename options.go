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

// WithMaxExplorationTurns aborts the agent if it spends too many consecutive turns
// reading/searching without making code changes (edit_file/write_file).
// Set to 0 to disable (default).
func WithMaxExplorationTurns(n int) Option {
	return func(a *Agent) { a.maxExplorationTurns = n }
}

// WithExplorationBudget enables unified exploration tracking with a token budget.
// Each read-only tool call costs 1 token; repeated reads cost 2 tokens.
// Any mutating tool call resets the budget.
// When budget is exhausted, a strong nudge is injected.
// This replaces the separate ReadTracker and consecutiveExplorationTurns mechanisms.
func WithExplorationBudget(budget int) Option {
	return func(a *Agent) { a.explorationBudget = budget }
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

// WithApprover sets the tool approval strategy.
// Default is nil (auto-approve all tools).
func WithApprover(approver Approver) Option {
	return func(a *Agent) { a.approver = approver }
}

// WithAutoApprove enables automatic approval of all tool calls (default behavior).
func WithAutoApprove() Option {
	return func(a *Agent) { a.approver = AutoApprover{} }
}

// WithInteractiveApprove enables interactive approval prompts for each tool call.
// User can respond with y (yes), n (no), or a (approve all remaining).
func WithInteractiveApprove() Option {
	return func(a *Agent) { a.approver = NewPromptApprover() }
}

// WithPatternApprove auto-approves tools matching the allowed list, prompts for others.
func WithPatternApprove(allowedTools []string) Option {
	return func(a *Agent) {
		a.approver = NewPatternApprover(allowedTools, NewPromptApprover())
	}
}

// WithSandbox sets file/command access restrictions for tools.
func WithSandbox(s *Sandbox) Option {
	return func(a *Agent) { a.sandbox = s }
}

// WithDefaultSandbox enables the default sandbox that blocks dangerous commands.
func WithDefaultSandbox() Option {
	return func(a *Agent) { a.sandbox = DefaultSandbox() }
}

// WithStrictSandbox enables a sandbox that restricts file access to allowed paths
// and blocks dangerous commands.
func WithStrictSandbox(allowedPaths ...string) Option {
	return func(a *Agent) { a.sandbox = StrictSandbox(allowedPaths) }
}

// WithCompressMemory enables automatic context compression when messages exceed maxMessages.
// recentWindow controls how many recent messages are preserved during compression.
func WithCompressMemory(recentWindow, maxMessages int) Option {
	return func(a *Agent) {
		a.memoryFactory = func() Memory {
			return NewCompressMemory(recentWindow, maxMessages)
		}
	}
}

// WithTokenAwareCompressMemory enables automatic context compression based on estimated token usage.
// Compression triggers when estimated tokens reach 70% of contextWindowSize.
// contextWindowSize is the model's context window in tokens (e.g. 200000 for 200k models).
// recentWindow controls how many recent messages are preserved during compression.
func WithTokenAwareCompressMemory(contextWindowSize, recentWindow int) Option {
	return func(a *Agent) {
		a.memoryFactory = func() Memory {
			return NewTokenAwareCompressMemory(contextWindowSize, recentWindow)
		}
	}
}

// WithOSSandboxOption enables OS-level sandboxing for shell commands.
// Requires external dependencies: sandbox-exec (macOS) or Docker (Linux).
// Commands are restricted to allowedPaths with network isolation.
func WithOSSandboxOption(allowedPaths ...string) Option {
	return func(a *Agent) {
		a.osSandbox = NewOSSandbox(allowedPaths...)
	}
}

// WithToolOutputMaxSize enables tool output compression.
// Outputs exceeding maxSize chars are smart-truncated based on tool type.
func WithToolOutputMaxSize(maxSize int) Option {
	return func(a *Agent) {
		a.toolOutputCompressor = NewToolOutputCompressor(maxSize)
	}
}
