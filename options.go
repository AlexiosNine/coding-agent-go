package cc

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
