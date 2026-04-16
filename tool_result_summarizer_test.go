package cc

import (
	"strings"
	"testing"
)

func TestToolResultSummarizer_NoOpWhenShort(t *testing.T) {
	s := NewToolResultSummarizer(500)
	output := "short output"
	result := s.Summarize("read_file", output)
	if result != output {
		t.Errorf("expected no summarization for short output")
	}
}

func TestToolResultSummarizer_Disabled(t *testing.T) {
	s := NewToolResultSummarizer(0)
	output := strings.Repeat("x", 1000)
	result := s.Summarize("read_file", output)
	if result != output {
		t.Errorf("expected no summarization when disabled")
	}
}

func TestToolResultSummarizer_EditFileNeverSummarized(t *testing.T) {
	s := NewToolResultSummarizer(100)
	output := strings.Repeat("x", 200)
	if s.Summarize("edit_file", output) != output {
		t.Error("edit_file should never be summarized")
	}
	if s.Summarize("write_file", output) != output {
		t.Error("write_file should never be summarized")
	}
}

func TestToolResultSummarizer_GrepSummary(t *testing.T) {
	s := NewToolResultSummarizer(200)
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "file.py:100: match line content here"
	}
	output := strings.Join(lines, "\n")
	result := s.Summarize("grep", output)

	if len(result) > 300 {
		t.Errorf("result too long: %d", len(result))
	}
	// Should keep first matches
	if !strings.Contains(result, "file.py:100") {
		t.Error("expected first matches preserved")
	}
}

func TestToolResultSummarizer_ReadFileSummary(t *testing.T) {
	s := NewToolResultSummarizer(300)
	output := `import os
import sys

class MyClass:
    def __init__(self):
        pass

    def method_one(self):
        return 1

    def method_two(self):
        return 2

def standalone_func():
    pass
` + strings.Repeat("# padding line\n", 50)

	result := s.Summarize("read_file", output)

	// Should contain symbol extraction
	if !strings.Contains(result, "class MyClass") {
		t.Errorf("expected class MyClass in summary, got: %s", result)
	}
	if !strings.Contains(result, "def __init__") || !strings.Contains(result, "def method_one") {
		t.Errorf("expected method signatures in summary, got: %s", result)
	}
	if !strings.Contains(result, "lines read") {
		t.Errorf("expected line count header, got: %s", result)
	}
}

func TestToolResultSummarizer_ShellSummary(t *testing.T) {
	s := NewToolResultSummarizer(200)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "build output line"
	}
	lines[99] = "ERROR: compilation failed"
	output := strings.Join(lines, "\n")
	result := s.Summarize("shell", output)

	// Should keep tail (errors at end)
	if !strings.Contains(result, "ERROR: compilation failed") {
		t.Errorf("expected last line preserved, got: %s", result)
	}
	if !strings.Contains(result, "lines total") {
		t.Errorf("expected line count header, got: %s", result)
	}
}

func TestToolResultSummarizer_ListFilesSummary(t *testing.T) {
	s := NewToolResultSummarizer(200)
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "src/module/file_" + string(rune('a'+i%26)) + ".go"
	}
	output := strings.Join(lines, "\n")
	result := s.Summarize("list_files", output)

	if !strings.Contains(result, "50 entries") {
		t.Errorf("expected entry count, got: %s", result)
	}
}
