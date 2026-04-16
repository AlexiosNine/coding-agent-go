package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", ""},
		{"no change", "hello world", "hello world"},
		{"trim leading", "  hello", "hello"},
		{"trim trailing", "hello  ", "hello"},
		{"collapse tabs", "hello\t\tworld", "hello world"},
		{"mixed whitespace", "  hello  \t world  ", "hello world"},
		{"multiline", "  foo  bar  \n\tbaz  qux", "foo bar\nbaz qux"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeWhitespace(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeWhitespace(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFindNormalizedMatch(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		oldString string
		wantStart int
		wantEnd   int
		wantFound bool
	}{
		{
			name:      "exact match not needed",
			content:   "func foo() {\n\treturn 1\n}",
			oldString: "func foo() {\n  return 1\n}",
			wantFound: true,
		},
		{
			name:      "trailing spaces",
			content:   "hello world\nfoo bar",
			oldString: "hello world  \nfoo bar  ",
			wantFound: true,
		},
		{
			name:      "tabs vs spaces",
			content:   "\thello\tworld",
			oldString: "  hello  world",
			wantFound: true,
		},
		{
			name:      "not found",
			content:   "hello world",
			oldString: "goodbye world",
			wantFound: false,
		},
		{
			name:      "ambiguous",
			content:   "foo bar\nfoo bar",
			oldString: "foo  bar",
			wantFound: false, // matches twice
		},
		{
			name:      "empty old_string",
			content:   "hello",
			oldString: "",
			wantFound: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, found := findNormalizedMatch(tt.content, tt.oldString)
			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}
			if found {
				// Verify the matched region makes sense
				if start < 0 || end > len(tt.content) || start >= end {
					t.Errorf("invalid range [%d, %d) for content len %d", start, end, len(tt.content))
				}
			}
			_ = start
			_ = end
		})
	}
}

func TestEditFile_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("func foo() {\n\treturn 1\n}\n"), 0644)

	tool := EditFile()
	out, err := tool.Execute(nil, mustJSON(editFileInput{
		Path:      f,
		OldString: "return 1",
		NewString: "return 2",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}

	data, _ := os.ReadFile(f)
	if got := string(data); got != "func foo() {\n\treturn 2\n}\n" {
		t.Errorf("file content = %q", got)
	}
}

func TestEditFile_NormalizedMatch(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	// File uses tabs
	os.WriteFile(f, []byte("func foo() {\n\treturn 1\n}\n"), 0644)

	tool := EditFile()
	// old_string uses spaces instead of tabs
	out, err := tool.Execute(nil, mustJSON(editFileInput{
		Path:      f,
		OldString: "func foo() {\n  return 1\n}",
		NewString: "func bar() {\n\treturn 2\n}",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}

	data, _ := os.ReadFile(f)
	content := string(data)
	if content != "func bar() {\n\treturn 2\n}\n" {
		t.Errorf("file content = %q", content)
	}
}

func TestFindSimilarContent_MultiLine(t *testing.T) {
	content := "line1\nline2\nfunc foo() {\n\treturn 1\n}\nline6\nline7"
	oldString := "func foo() {\n    return 999\n}"

	hint := findSimilarContent(content, oldString)
	if hint == "" {
		t.Fatal("expected non-empty hint")
	}
	// Should find partial match near line 3
	if !strings.Contains(hint, "partial match") {
		t.Errorf("hint should mention partial match: %s", hint)
	}
}

func mustJSON(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
