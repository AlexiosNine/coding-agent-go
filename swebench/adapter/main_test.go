package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

func TestSweToolsLeanByDefault(t *testing.T) {
	tools := sweTools("")
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name()
	}

	want := []string{"grep", "read_file", "edit_file"}
	if len(names) != len(want) {
		t.Fatalf("expected %d lean tools, got %d: %v", len(want), len(names), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tool %d mismatch: got %q want %q", i, names[i], want[i])
		}
	}
}

func TestSweToolsFullIncludesFileToolsWithoutShell(t *testing.T) {
	tools := sweTools("full")
	seen := make(map[string]bool)
	for _, tool := range tools {
		seen[tool.Name()] = true
	}
	for _, name := range []string{"write_file", "list_files", "grep", "read_file", "edit_file"} {
		if !seen[name] {
			t.Fatalf("expected full toolset to include %s", name)
		}
	}
	if seen["shell"] {
		t.Fatal("SWE-bench adapter must not expose shell because file access must stay inside workspace")
	}
}

func TestDefaultWorkspaceRootUsesEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SWE_WORKSPACE_ROOT", root)

	got, err := defaultWorkspaceRoot()
	if err != nil {
		t.Fatalf("defaultWorkspaceRoot: %v", err)
	}
	want, _ := filepath.Abs(root)
	if got != want {
		t.Fatalf("workspace root mismatch: got %q want %q", got, want)
	}
}

func TestDefaultTemplatesRootUsesEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SWE_TEMPLATES_DIR", root)

	got, err := defaultTemplatesRoot()
	if err != nil {
		t.Fatalf("defaultTemplatesRoot: %v", err)
	}
	want, _ := filepath.Abs(root)
	if got != want {
		t.Fatalf("templates root mismatch: got %q want %q", got, want)
	}
}

func TestBuildPromptUsesProgressiveTemplates(t *testing.T) {
	prompt, err := buildPrompt(Instance{
		Repo:             "sympy/sympy",
		BaseCommit:       "abc123",
		ProblemStatement: "ccode(sinc(x)) doesn't work",
		HintsText:        "Use Piecewise",
	}, "lean")
	if err != nil {
		t.Fatalf("buildPrompt: %v", err)
	}

	for _, want := range []string{
		"Fix this SWE-bench issue",
		"Repo: sympy/sympy",
		"Base commit: abc123",
		"ccode(sinc(x)) doesn't work",
		"Hints:\nUse Piecewise",
		"grep:",
		"read_file:",
		"edit_file:",
		"Rules:",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "write_file:") || strings.Contains(prompt, "list_files:") {
		t.Fatalf("lean prompt should not include full-only tool guidance:\n%s", prompt)
	}
}

func TestBuildPromptFullToolsetAddsFullToolGuidance(t *testing.T) {
	prompt, err := buildPrompt(Instance{Repo: "repo", BaseCommit: "sha", ProblemStatement: "issue"}, "full")
	if err != nil {
		t.Fatalf("buildPrompt: %v", err)
	}
	for _, want := range []string{"write_file:", "list_files:"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("full prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSweConvergenceGuardOverridesReadOnlyThreshold(t *testing.T) {
	cfg := sweConvergenceGuard(10)
	if !cfg.Enabled {
		t.Fatal("expected guard enabled")
	}
	if cfg.MaxConsecutiveReadOnlyTurns != 10 {
		t.Fatalf("expected max read-only threshold 10, got %d", cfg.MaxConsecutiveReadOnlyTurns)
	}
	if !cfg.StopOnRepeatedRead || !cfg.StopOnBudgetExhausted || !cfg.StopReadOnlyAfterEdit {
		t.Fatalf("expected default hard-stop policies preserved: %#v", cfg)
	}
}

func TestSafePathNameRemovesSeparators(t *testing.T) {
	got := safePathName("../bad/repo:case")
	if got == "../bad/repo:case" || filepath.IsAbs(got) {
		t.Fatalf("expected sanitized relative name, got %q", got)
	}
	for _, bad := range []string{"/", "\\", ":", ".."} {
		if strings.Contains(got, bad) {
			t.Fatalf("path name %q still contains %q", got, bad)
		}
	}
}

func TestBuildMetricsVerificationFailureIsCaseFailed(t *testing.T) {
	firstEdit := 3
	result := &cc.RunResult{
		StopReason: cc.StopReasonSuccessText,
		Turns:      5,
		Usage:      cc.Usage{InputTokens: 10, OutputTokens: 4},
		Metrics: cc.RunMetrics{
			ToolCallCount: 3,
			GrepCalls:     1,
			ReadFileCalls: 1,
			EditFileCalls: 1,
			FirstEditTurn: firstEdit,
		},
	}

	metrics := buildMetrics(Instance{InstanceID: "sympy__sympy-11400"}, "model", "lean", result, "diff\n", "failed", "exit status 1", time.Now())
	if metrics.RunResult != "case_failed" {
		t.Fatalf("expected case_failed, got %s", metrics.RunResult)
	}
	if metrics.FirstEditTurn == nil || *metrics.FirstEditTurn != firstEdit {
		t.Fatalf("unexpected first edit turn: %#v", metrics.FirstEditTurn)
	}
}

func TestBuildMetricsGuardStop(t *testing.T) {
	result := &cc.RunResult{
		StopReason:  cc.StopReasonStoppedByGuard,
		GuardReason: "repeated_read",
		Metrics: cc.RunMetrics{
			GuardTriggers: 1,
			GuardReason:   "repeated_read",
		},
	}

	metrics := buildMetrics(Instance{InstanceID: "case"}, "model", "lean", result, "", "skipped", "", time.Now())
	if metrics.RunResult != "stopped_by_guard" {
		t.Fatalf("expected stopped_by_guard, got %s", metrics.RunResult)
	}
	if metrics.GuardTriggers != 1 {
		t.Fatalf("expected guard trigger count, got %#v", metrics)
	}
}

func TestRenderSummaryIncludesCoreFields(t *testing.T) {
	metrics := caseMetrics{
		InstanceID:   "case",
		RunResult:    "case_success",
		StopReason:   cc.StopReasonSuccessText,
		VerifyStatus: "passed",
		Turns:        3,
		ToolCalls:    2,
		PatchBytes:   10,
		PatchLines:   1,
		InputTokens:  5,
		OutputTokens: 6,
	}
	summary := renderSummary(metrics)
	for _, want := range []string{"case_success", "passed", "Tool calls: 2", "Patch: 10 bytes"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestJSONLEventSinkInjectsInstanceID(t *testing.T) {
	var buf bytes.Buffer
	sink := &jsonlEventSink{instanceID: "sympy__sympy-11400", enc: json.NewEncoder(&buf)}

	sink.EmitAgentEvent(context.Background(), cc.AgentEvent{Event: cc.EventRunStarted, RunID: "run"})

	var event cc.AgentEvent
	if err := json.Unmarshal(buf.Bytes(), &event); err != nil {
		t.Fatalf("parse event: %v", err)
	}
	if event.InstanceID != "sympy__sympy-11400" {
		t.Fatalf("expected instance id injection, got %#v", event)
	}
}
