package cc

import (
	"strings"
	"testing"
)

func TestToolOutputCompressor_NoOp(t *testing.T) {
	c := NewToolOutputCompressor(1000)
	output := "short output"
	result := c.Compress("read_file", output)
	if result != output {
		t.Errorf("expected no compression for short output")
	}
}

func TestToolOutputCompressor_Disabled(t *testing.T) {
	c := NewToolOutputCompressor(0)
	output := strings.Repeat("x", 10000)
	result := c.Compress("read_file", output)
	if result != output {
		t.Errorf("expected no compression when maxSize=0")
	}
}

func TestToolOutputCompressor_EditFileNeverCompressed(t *testing.T) {
	c := NewToolOutputCompressor(100)
	output := strings.Repeat("x", 200)
	result := c.Compress("edit_file", output)
	if result != output {
		t.Errorf("edit_file should never be compressed")
	}
}

func TestToolOutputCompressor_ReadFile(t *testing.T) {
	c := NewToolOutputCompressor(100)
	output := strings.Repeat("line\n", 50) // 250 chars
	result := c.Compress("read_file", output)

	if len(result) > 150 {
		t.Errorf("result too long: %d chars", len(result))
	}
	if !strings.Contains(result, "...") {
		t.Errorf("expected gap marker in truncated output")
	}
	if !strings.Contains(result, "[truncated") {
		t.Errorf("expected truncation suffix")
	}
	// Should contain both head and tail
	if !strings.HasPrefix(result, "line\n") {
		t.Errorf("expected head preserved")
	}
}

func TestToolOutputCompressor_Shell(t *testing.T) {
	c := NewToolOutputCompressor(100)
	output := strings.Repeat("line\n", 50) // 250 chars
	result := c.Compress("shell", output)

	if len(result) > 200 {
		t.Errorf("result too long: %d chars", len(result))
	}
	if !strings.Contains(result, "[truncated") {
		t.Errorf("expected truncation suffix")
	}
	// Shell keeps tail (errors at end), so result should end with "line\n" before suffix
	lines := strings.Split(result, "\n")
	hasLineSuffix := false
	for _, line := range lines {
		if strings.Contains(line, "line") && !strings.Contains(line, "[truncated") {
			hasLineSuffix = true
			break
		}
	}
	if !hasLineSuffix {
		t.Errorf("expected tail content preserved, got: %s", result)
	}
}

func TestToolOutputCompressor_Grep(t *testing.T) {
	c := NewToolOutputCompressor(100)
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "match line\n"
	}
	output := strings.Join(lines, "")
	result := c.Compress("grep", output)

	if !strings.Contains(result, "[truncated") {
		t.Errorf("expected truncation suffix")
	}
	// Grep keeps first N lines
	if !strings.HasPrefix(result, "match line\n") {
		t.Errorf("expected first lines preserved")
	}
}

func TestToolOutputCompressor_Generic(t *testing.T) {
	c := NewToolOutputCompressor(100)
	output := strings.Repeat("x", 500)
	result := c.Compress("unknown_tool", output)

	if len(result) > 150 {
		t.Errorf("result too long: %d chars", len(result))
	}
	if !strings.Contains(result, "[truncated") {
		t.Errorf("expected truncation suffix")
	}
	// Generic keeps head
	if !strings.HasPrefix(result, "xxx") {
		t.Errorf("expected head preserved")
	}
}
