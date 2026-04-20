package cc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"golang.org/x/sync/errgroup"
)

// Session holds the state for a single conversation.
// Create via Agent.NewSession().
type Session struct {
	agent             *Agent
	memory            Memory
	outputBuffer      *OutputBuffer
	dedup             *MessageDeduplicator
	readTracker       *ReadTracker       // legacy path (when explorationBudget is nil)
	explorationBudget *ExplorationBudget // unified tracker; nil if not configured
	summarizer        *ToolResultSummarizer
	factCache         *SessionFactCache
	skillRegistry     *SkillRegistry
	skillTool         Tool     // use_skill tool instance
	activeSkills      []string // session-local active skill names
	maxActiveSkills   int      // max concurrent active skills (default 3)
	systemOverride    string   // if set, overrides agent.system for this session
}

// Run executes the agent loop within this session's conversation context.
func (s *Session) Run(ctx context.Context, input string) (*RunResult, error) {
	if input == "" {
		return nil, ErrEmptyInput
	}
	if s.agent.provider == nil {
		return nil, ErrNoProvider
	}

	s.memory.Add(NewUserMessage(input))

	// Track this session as parent for sub-agents
	ctx = withParentSession(ctx, s)

	var totalUsage Usage
	consecutiveExplorationTurns := 0

	for turn := range s.agent.maxTurns {
		resp, err := s.step(ctx)
		if err != nil {
			return nil, fmt.Errorf("turn %d: %w", turn+1, err)
		}

		totalUsage = totalUsage.Add(resp.Usage)

		if s.agent.hooks.OnModelResponse != nil {
			s.agent.hooks.OnModelResponse(ctx, resp)
		}

		s.memory.Add(Message{Role: RoleAssistant, Content: resp.Content})

		toolUses := resp.ToolUses()
		if len(toolUses) == 0 {
			// Implicit skill matching on text responses
			if s.skillRegistry != nil {
				if skill := s.skillRegistry.Match(resp.Text(), s.activeSkills); skill != nil {
					s.activateSkill(skill.Meta.Name)
					continue // next turn will see skill instructions in system prompt
				}
			}
			return &RunResult{
				Output:   resp.Text(),
				Messages: s.memory.Messages(),
				Turns:    turn + 1,
				Usage:    totalUsage,
			}, nil
		}

		// Check if any mutating tools were called
		hasMutatingTool := false
		for _, tu := range toolUses {
			if tu.Name == "write_file" || tu.Name == "edit_file" {
				hasMutatingTool = true
				break
			}
			// shell can also be mutating if it contains file-modifying commands
			if tu.Name == "shell" {
				var shellInput struct {
					Command string `json:"command"`
				}
				if err := json.Unmarshal(tu.Input, &shellInput); err == nil {
					cmd := shellInput.Command
					if strings.Contains(cmd, ">") || strings.Contains(cmd, " -i") ||
						strings.Contains(cmd, "open(") || strings.Contains(cmd, "write(") ||
						strings.Contains(cmd, "git checkout") || strings.Contains(cmd, "mv ") ||
						strings.Contains(cmd, "cp ") || strings.Contains(cmd, "mkdir ") {
						hasMutatingTool = true
						break
					}
				}
			}
		}

		if hasMutatingTool {
			consecutiveExplorationTurns = 0
		} else {
			consecutiveExplorationTurns++
		}

		// If stuck in exploration mode for too long, abort
		if s.agent.maxExplorationTurns > 0 && consecutiveExplorationTurns >= s.agent.maxExplorationTurns {
			return &RunResult{
				Output:   fmt.Sprintf("Aborted: %d consecutive turns without making code changes (stuck in exploration mode)", consecutiveExplorationTurns),
				Messages: s.memory.Messages(),
				Turns:    turn + 1,
				Usage:    totalUsage,
			}, fmt.Errorf("stuck in exploration: %d turns without edit_file/write_file", consecutiveExplorationTurns)
		}

		results := s.executeTools(ctx, toolUses)
		s.memory.Add(NewToolResultMessage(results...))

		// Unified exploration budget (replaces both consecutiveExplorationTurns and readTracker)
		if s.explorationBudget != nil {
			if nudge := s.explorationBudget.Consume(toolUses); nudge != "" {
				s.memory.Add(NewUserMessage(nudge))
			}
		} else {
			// Legacy path: simple counter + ReadTracker
			if hasMutatingTool {
				consecutiveExplorationTurns = 0
			} else {
				consecutiveExplorationTurns++
			}

			// If stuck in exploration mode for too long, abort
			if s.agent.maxExplorationTurns > 0 && consecutiveExplorationTurns >= s.agent.maxExplorationTurns {
				return &RunResult{
					Output:   fmt.Sprintf("Aborted: %d consecutive turns without making code changes (stuck in exploration mode)", consecutiveExplorationTurns),
					Messages: s.memory.Messages(),
					Turns:    turn + 1,
					Usage:    totalUsage,
				}, fmt.Errorf("stuck in exploration: %d turns without edit_file/write_file", consecutiveExplorationTurns)
			}

			// Detect repeated reads and nudge model to take action
			if s.readTracker != nil {
				nudge := s.readTracker.Track(toolUses)
				if nudge != "" {
					s.memory.Add(NewUserMessage(nudge))
				}
			}
		}
	}

	return &RunResult{
		Output:   "Max turns exceeded",
		Messages: s.memory.Messages(),
		Turns:    s.agent.maxTurns,
		Usage:    totalUsage,
	}, ErrMaxTurns
}

// step makes a single LLM call, with retry if configured.
func (s *Session) step(ctx context.Context) (*ChatResponse, error) {
	system := s.agent.system
	if s.systemOverride != "" {
		system = s.systemOverride
	}
	// Inject session facts into system prompt
	if s.factCache != nil {
		if facts := s.factCache.Render(); facts != "" {
			system += "\n" + facts
		}
	}
	// Inject skill summary + active instructions
	if s.skillRegistry != nil {
		if summary := s.skillRegistry.Summary(); summary != "" {
			system += "\n" + summary
		}
		if inst := s.skillRegistry.GetInstructions(s.activeSkills); inst != "" {
			system += "\n" + inst
		}
	}

	messages := s.memory.Messages()
	if s.dedup != nil {
		messages = s.dedup.Process(messages)
	}

	tools := s.agent.toolDefs()
	// Append skill tool definition if present
	if s.skillTool != nil {
		tools = append(tools, ToolDef{
			Name:        s.skillTool.Name(),
			Description: s.skillTool.Description(),
			InputSchema: s.skillTool.InputSchema(),
		})
	}

	params := ChatParams{
		Model:     s.agent.model,
		System:    system,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: s.agent.maxTokens,
	}

	if s.agent.retry == nil {
		return s.agent.provider.Chat(ctx, params)
	}

	var resp *ChatResponse
	err := retry(ctx, *s.agent.retry, func() error {
		var callErr error
		resp, callErr = s.agent.provider.Chat(ctx, params)
		return callErr
	})
	return resp, err
}

// executeTools runs all tool calls concurrently.
func (s *Session) executeTools(ctx context.Context, toolUses []ToolUseContent) []ToolResultContent {
	results := make([]ToolResultContent, len(toolUses))

	g, ctx := errgroup.WithContext(ctx)
	if s.agent.maxConcurrency > 0 {
		g.SetLimit(s.agent.maxConcurrency)
	}

	for i, tu := range toolUses {
		g.Go(func() error {
			results[i] = s.executeSingleTool(ctx, tu)
			return nil
		})
	}
	_ = g.Wait()
	return results
}

// executeSingleTool runs a single tool call.
func (s *Session) executeSingleTool(ctx context.Context, tu ToolUseContent) ToolResultContent {
	tool, ok := s.agent.tools[tu.Name]
	if !ok {
		// Check session-level skill tool
		if s.skillTool != nil && tu.Name == "use_skill" {
			tool = s.skillTool
		} else {
			return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("tool %q not found", tu.Name), IsError: true}
		}
	}

	// Approval check
	if s.agent.approver != nil {
		approved, err := s.agent.approver.Approve(ctx, tu.Name, tu.Input)
		if err != nil {
			return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("approval error: %s", err.Error()), IsError: true}
		}
		if !approved {
			return ToolResultContent{ToolUseID: tu.ID, Content: "tool call denied by user", IsError: true}
		}
	}

	// Sandbox check
	if s.agent.sandbox != nil {
		if err := s.agent.sandbox.CheckToolCall(tu.Name, tu.Input); err != nil {
			return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("sandbox blocked: %s", err.Error()), IsError: true}
		}
	}

	if s.agent.hooks.BeforeToolCall != nil {
		if err := s.agent.hooks.BeforeToolCall(ctx, tu.Name, tu.Input); err != nil {
			return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("tool call blocked: %s", err.Error()), IsError: true}
		}
	}

	// Propagate OS sandbox via context
	if s.agent.osSandbox != nil {
		ctx = WithOSSandbox(ctx, s.agent.osSandbox)
	}

	ctx = WithOutputBuffer(ctx, s.outputBuffer)

	output, err := tool.Execute(ctx, tu.Input)

	// Apply output compression if configured
	if s.agent.toolOutputCompressor != nil && err == nil {
		output = s.agent.toolOutputCompressor.Compress(tu.Name, output)
	}

	// Extract facts before summarization
	if s.factCache != nil && err == nil {
		s.factCache.Extract(tu.Name, output)
	}

	// Summarize tool output for the model
	if s.summarizer != nil && err == nil {
		output = s.summarizer.Summarize(tu.Name, output)
	}

	if s.agent.hooks.AfterToolCall != nil {
		s.agent.hooks.AfterToolCall(ctx, tu.Name, output, err)
	}

	if err != nil {
		return ToolResultContent{ToolUseID: tu.ID, Content: fmt.Sprintf("error: %s", err.Error()), IsError: true}
	}
	return ToolResultContent{ToolUseID: tu.ID, Content: output}
}

// Messages returns the session's conversation history.
func (s *Session) Messages() []Message {
	return s.memory.Messages()
}

// ClearMemory resets the session's conversation history.
func (s *Session) ClearMemory() {
	s.memory.Clear()
}

// activateSkill adds a skill to the session's active list.
// Implements LRU eviction if maxActiveSkills is exceeded.
func (s *Session) activateSkill(name string) {
	// Check if already active
	for _, n := range s.activeSkills {
		if n == name {
			return // idempotent
		}
	}

	// Evict oldest if at capacity
	if len(s.activeSkills) >= s.maxActiveSkills {
		s.activeSkills = s.activeSkills[1:]
	}

	s.activeSkills = append(s.activeSkills, name)
}

// deactivateSkill removes a skill from the session's active list.
func (s *Session) deactivateSkill(name string) {
	for i, n := range s.activeSkills {
		if n == name {
			s.activeSkills = append(s.activeSkills[:i], s.activeSkills[i+1:]...)
			return
		}
	}
}

// RunStream executes the agent loop with streaming output.
// If the provider implements StreamProvider, tokens are streamed as they arrive.
// Otherwise, falls back to Chat() and emits the complete response as events.
func (s *Session) RunStream(ctx context.Context, input string) (<-chan StreamEvent, error) {
	if input == "" {
		return nil, ErrEmptyInput
	}
	if s.agent.provider == nil {
		return nil, ErrNoProvider
	}

	out := make(chan StreamEvent, 64)

	go func() {
		defer close(out)
		s.memory.Add(NewUserMessage(input))

		for turn := range s.agent.maxTurns {
			resp, err := s.streamStep(ctx, out)
			if err != nil {
				out <- StreamEvent{Type: "error", Error: fmt.Errorf("turn %d: %w", turn+1, err)}
				return
			}

			if s.agent.hooks.OnModelResponse != nil {
				s.agent.hooks.OnModelResponse(ctx, resp)
			}

			s.memory.Add(Message{Role: RoleAssistant, Content: resp.Content})

			toolUses := resp.ToolUses()
			if len(toolUses) == 0 {
				out <- StreamEvent{Type: "message_stop", Usage: resp.Usage}
				return
			}

			results := s.executeTools(ctx, toolUses)
			s.memory.Add(NewToolResultMessage(results...))
		}

		out <- StreamEvent{Type: "error", Error: ErrMaxTurns}
	}()

	return out, nil
}

// streamStep attempts to stream from the provider, falling back to Chat().
func (s *Session) streamStep(ctx context.Context, out chan<- StreamEvent) (*ChatResponse, error) {
	system := s.agent.system
	if s.systemOverride != "" {
		system = s.systemOverride
	}
	// Inject session facts into system prompt
	if s.factCache != nil {
		if facts := s.factCache.Render(); facts != "" {
			system += "\n" + facts
		}
	}
	// Inject skill summary + active instructions
	if s.skillRegistry != nil {
		if summary := s.skillRegistry.Summary(); summary != "" {
			system += "\n" + summary
		}
		if inst := s.skillRegistry.GetInstructions(s.activeSkills); inst != "" {
			system += "\n" + inst
		}
	}

	messages := s.memory.Messages()
	if s.dedup != nil {
		messages = s.dedup.Process(messages)
	}

	tools := s.agent.toolDefs()
	// Append skill tool definition if present
	if s.skillTool != nil {
		tools = append(tools, ToolDef{
			Name:        s.skillTool.Name(),
			Description: s.skillTool.Description(),
			InputSchema: s.skillTool.InputSchema(),
		})
	}

	params := ChatParams{
		Model:     s.agent.model,
		System:    system,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: s.agent.maxTokens,
	}

	if sp, ok := s.agent.provider.(StreamProvider); ok {
		reader, err := sp.Stream(ctx, params)
		if err != nil {
			return nil, err
		}

		var contents []Content
		var usage Usage
		var hasToolUse bool
		for {
			ev, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			out <- ev

			switch ev.Type {
			case "text_delta":
				if len(contents) == 0 {
					contents = append(contents, TextContent{})
				}
				if tc, ok := contents[len(contents)-1].(TextContent); ok {
					contents[len(contents)-1] = TextContent{Text: tc.Text + ev.Text}
				}
			case "tool_use":
				if ev.ToolUse != nil {
					hasToolUse = true
					contents = append(contents, *ev.ToolUse)
				}
			case "message_stop":
				usage = ev.Usage
			}
		}

		stopReason := "end_turn"
		if hasToolUse {
			stopReason = "tool_use"
		}
		return &ChatResponse{Content: contents, StopReason: stopReason, Usage: usage}, nil
	}

	// Fallback: use Chat() and emit as single event
	resp, err := s.agent.provider.Chat(ctx, params)
	if err != nil {
		return nil, err
	}

	text := resp.Text()
	if text != "" {
		out <- StreamEvent{Type: "text_delta", Text: text}
	}
	for _, tu := range resp.ToolUses() {
		out <- StreamEvent{Type: "tool_use", ToolUse: &tu}
	}

	return resp, nil
}
