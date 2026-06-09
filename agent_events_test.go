package cc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestEventSinkReceivesRunAndToolEvents(t *testing.T) {
	var events []cc.AgentEvent
	sink := cc.EventSinkFunc(func(_ context.Context, event cc.AgentEvent) {
		events = append(events, event)
	})
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{
				Content:    []cc.Content{cc.ToolUseContent{ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)}},
				StopReason: "tool_use",
				Usage:      cc.Usage{InputTokens: 10, OutputTokens: 2},
			},
			{
				Content:    []cc.Content{cc.TextContent{Text: "done"}},
				StopReason: "end_turn",
				Usage:      cc.Usage{InputTokens: 5, OutputTokens: 1},
			},
		},
	}
	readTool := cc.NewFuncTool("read_file", "Read", func(_ context.Context, _ struct {
		Path string `json:"path"`
	}) (string, error) {
		return "content", nil
	})

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(readTool),
		cc.WithEventSink(sink),
		cc.WithRunID("test-run"),
	)
	result, err := agent.Run(context.Background(), "inspect")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.StopReason != cc.StopReasonSuccessText {
		t.Fatalf("unexpected stop reason: %s", result.StopReason)
	}
	want := []string{
		cc.EventRunStarted,
		cc.EventModelResponse,
		cc.EventToolCallStarted,
		cc.EventToolCallFinished,
		cc.EventModelResponse,
		cc.EventRunFinished,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d: %#v", len(want), len(events), events)
	}
	for i, event := range events {
		if event.Event != want[i] {
			t.Fatalf("event %d: expected %s, got %s", i, want[i], event.Event)
		}
		if event.RunID != "test-run" {
			t.Fatalf("event %d: expected run id test-run, got %q", i, event.RunID)
		}
	}
}

func TestConvergenceGuardStopsReadOnlyLoop(t *testing.T) {
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.ToolUseContent{ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)}}, StopReason: "tool_use"},
			{Content: []cc.Content{cc.ToolUseContent{ID: "c2", Name: "grep", Input: json.RawMessage(`{"path":"."}`)}}, StopReason: "tool_use"},
		},
	}
	readTool := cc.NewFuncTool("read_file", "Read", func(_ context.Context, _ struct {
		Path string `json:"path"`
	}) (string, error) {
		return "content", nil
	})
	grepTool := cc.NewFuncTool("grep", "Grep", func(_ context.Context, _ struct {
		Path string `json:"path"`
	}) (string, error) {
		return "match", nil
	})

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(readTool, grepTool),
		cc.WithConvergenceGuard(cc.ConvergenceGuardConfig{MaxConsecutiveReadOnlyTurns: 2}),
		cc.WithMaxTurns(5),
	)
	result, err := agent.Run(context.Background(), "inspect")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.StopReason != cc.StopReasonStoppedByGuard {
		t.Fatalf("expected stopped_by_guard, got %s", result.StopReason)
	}
	if result.Metrics.GuardTriggers != 1 || result.Metrics.ToolCallCount != 2 || result.Metrics.ReadOnlyCount != 2 {
		t.Fatalf("unexpected metrics: %#v", result.Metrics)
	}
}

func TestConvergenceGuardStopsRepeatedRead(t *testing.T) {
	responses := make([]*cc.ChatResponse, 4)
	for i := range responses {
		responses[i] = &cc.ChatResponse{
			Content:    []cc.Content{cc.ToolUseContent{ID: string(rune('a' + i)), Name: "read_file", Input: json.RawMessage(`{"path":"a.go","start_line":1,"end_line":20}`)}},
			StopReason: "tool_use",
		}
	}
	provider := &mockProvider{responses: responses}
	readTool := cc.NewFuncTool("read_file", "Read", func(_ context.Context, _ struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}) (string, error) {
		return "content", nil
	})

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(readTool),
		cc.WithConvergenceGuard(cc.ConvergenceGuardConfig{
			MaxConsecutiveReadOnlyTurns: 10,
			StopOnRepeatedRead:          true,
		}),
		cc.WithMaxTurns(5),
	)
	result, err := agent.Run(context.Background(), "inspect")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.StopReason != cc.StopReasonStoppedByGuard || result.GuardReason != "repeated_read" {
		t.Fatalf("expected repeated read guard, got stop=%s guard=%s", result.StopReason, result.GuardReason)
	}
}

func TestConvergenceGuardStopsReadOnlyAfterEdit(t *testing.T) {
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.ToolUseContent{ID: "c1", Name: "edit_file", Input: json.RawMessage(`{"path":"a.go"}`)}}, StopReason: "tool_use"},
			{Content: []cc.Content{cc.ToolUseContent{ID: "c2", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)}}, StopReason: "tool_use"},
		},
	}
	editTool := cc.NewFuncTool("edit_file", "Edit", func(_ context.Context, _ struct {
		Path string `json:"path"`
	}) (string, error) {
		return "edited", nil
	})
	readTool := cc.NewFuncTool("read_file", "Read", func(_ context.Context, _ struct {
		Path string `json:"path"`
	}) (string, error) {
		return "content", nil
	})

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(editTool, readTool),
		cc.WithConvergenceGuard(cc.DefaultConvergenceGuardConfig()),
		cc.WithMaxTurns(5),
	)
	result, err := agent.Run(context.Background(), "fix")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.StopReason != cc.StopReasonStoppedByGuard || result.GuardReason != "read_only_after_successful_edit" {
		t.Fatalf("unexpected guard result: stop=%s guard=%s", result.StopReason, result.GuardReason)
	}
	if result.Metrics.FirstEditTurn != 1 || result.Metrics.ReadOnlyAfterEditBlocks != 1 {
		t.Fatalf("unexpected metrics: %#v", result.Metrics)
	}
}

func TestFailedEditAllowsOneRecoveryReadDespiteBudgetNudge(t *testing.T) {
	provider := &mockProvider{
		responses: []*cc.ChatResponse{
			{Content: []cc.Content{cc.ToolUseContent{ID: "r1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go","start_line":1,"end_line":20}`)}}, StopReason: "tool_use"},
			{Content: []cc.Content{cc.ToolUseContent{ID: "e1", Name: "edit_file", Input: json.RawMessage(`{"path":"a.go","old_string":"missing","new_string":"x"}`)}}, StopReason: "tool_use"},
			{Content: []cc.Content{cc.ToolUseContent{ID: "r2", Name: "read_file", Input: json.RawMessage(`{"path":"a.go","start_line":1,"end_line":20}`)}}, StopReason: "tool_use"},
			{Content: []cc.Content{cc.TextContent{Text: "cannot safely continue"}}, StopReason: "end_turn"},
		},
	}
	readTool := cc.NewFuncTool("read_file", "Read", func(_ context.Context, _ struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}) (string, error) {
		return "content", nil
	})
	editTool := cc.NewFuncTool("edit_file", "Edit", func(_ context.Context, _ struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}) (string, error) {
		return "", fmt.Errorf("old_string not found")
	})

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(readTool, editTool),
		cc.WithExplorationBudget(1),
		cc.WithConvergenceGuard(cc.DefaultConvergenceGuardConfig()),
		cc.WithMaxTurns(5),
	)
	result, err := agent.Run(context.Background(), "fix")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.StopReason != cc.StopReasonSuccessText {
		t.Fatalf("expected recovery read to avoid guard stop, got stop=%s guard=%s", result.StopReason, result.GuardReason)
	}
	if result.Metrics.GuardTriggers != 0 || result.Metrics.EditFileCalls != 1 || result.Metrics.ReadFileCalls != 2 {
		t.Fatalf("unexpected metrics: %#v", result.Metrics)
	}
}
