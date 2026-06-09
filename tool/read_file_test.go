package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileRangeIncludesAbsoluteLineMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.py")
	content := strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	input, _ := json.Marshal(map[string]any{
		"path":       path,
		"start_line": 2,
		"end_line":   3,
	})
	output, err := ReadFile().Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}

	for _, want := range []string{
		"File: " + path,
		"Lines: 2-3 of 4",
		"Content:\nline 2\nline 3",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
