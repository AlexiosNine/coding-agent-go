package cc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Approver decides whether a tool call should be executed.
type Approver interface {
	Approve(ctx context.Context, name string, input json.RawMessage) (bool, error)
}

// AutoApprover approves all tool calls without prompting.
type AutoApprover struct{}

func (AutoApprover) Approve(_ context.Context, _ string, _ json.RawMessage) (bool, error) {
	return true, nil
}

// DenyApprover denies all tool calls.
type DenyApprover struct{}

func (DenyApprover) Approve(_ context.Context, _ string, _ json.RawMessage) (bool, error) {
	return false, nil
}

// PromptApprover asks the user interactively before each tool call.
// Supports: y (yes), n (no), a (approve all remaining).
type PromptApprover struct {
	approveAll bool
	reader     *bufio.Reader
}

// NewPromptApprover creates an interactive approver that reads from stdin.
func NewPromptApprover() *PromptApprover {
	return &PromptApprover{reader: bufio.NewReader(os.Stdin)}
}

func (p *PromptApprover) Approve(_ context.Context, name string, input json.RawMessage) (bool, error) {
	if p.approveAll {
		return true, nil
	}

	fmt.Printf("\n[Tool call] %s\n  Input: %s\n  Approve? [y/n/a(all)] ", name, string(input))

	line, err := p.reader.ReadString('\n')
	if err != nil {
		return false, err
	}

	switch strings.TrimSpace(strings.ToLower(line)) {
	case "y", "yes":
		return true, nil
	case "a", "all":
		p.approveAll = true
		return true, nil
	default:
		return false, nil
	}
}

// PatternApprover auto-approves tools matching allowed names, denies others.
type PatternApprover struct {
	allowed map[string]bool
	fallback Approver
}

// NewPatternApprover creates an approver that auto-approves listed tools.
// Tools not in the list are delegated to fallback (default: DenyApprover).
func NewPatternApprover(allowedTools []string, fallback Approver) *PatternApprover {
	if fallback == nil {
		fallback = DenyApprover{}
	}
	allowed := make(map[string]bool, len(allowedTools))
	for _, t := range allowedTools {
		allowed[t] = true
	}
	return &PatternApprover{allowed: allowed, fallback: fallback}
}

func (p *PatternApprover) Approve(ctx context.Context, name string, input json.RawMessage) (bool, error) {
	if p.allowed[name] {
		return true, nil
	}
	return p.fallback.Approve(ctx, name, input)
}
