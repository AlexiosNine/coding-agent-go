package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{
			input: "authMiddleware",
			want:  []string{"auth", "middleware"},
		},
		{
			input: "getUserByID",
			want:  []string{"get", "user", "id"},
		},
		{
			input: "parse_json_data",
			want:  []string{"parse", "json", "data"},
		},
		{
			input: "The quick brown fox",
			want:  []string{"quick", "brown", "fox"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := tokenize(tt.input)
			for _, w := range tt.want {
				if got[w] == 0 {
					t.Errorf("tokenize(%q) missing expected term %q", tt.input, w)
				}
			}
		})
	}
}

func TestSearchBasic(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	files := map[string]string{
		"auth.go": `package main
func AuthMiddleware(next Handler) Handler {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token == "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		next(w, r)
	}
}`,
		"user.go": `package main
type User struct {
	ID    int
	Name  string
	Email string
}

func GetUserByID(id int) (*User, error) {
	// database lookup
	return nil, nil
}`,
		"config.json": `{"port": 8080}`,
	}

	for name, content := range files {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tool := Search()
	ctx := context.Background()

	// Test 1: Search for "authentication"
	result, err := tool.Execute(ctx, []byte(`{"query":"authentication middleware","path":"`+tmpDir+`"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(result, "auth.go") {
		t.Errorf("Expected auth.go in results, got: %s", result)
	}

	// Test 2: Search with pattern filter
	result, err = tool.Execute(ctx, []byte(`{"query":"user","path":"`+tmpDir+`","pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(result, "user.go") {
		t.Errorf("Expected user.go in results, got: %s", result)
	}

	if strings.Contains(result, "config.json") {
		t.Errorf("Should not include config.json with *.go pattern, got: %s", result)
	}
}

func TestSearchNoResults(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file with unrelated content
	path := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := Search()
	ctx := context.Background()

	result, err := tool.Execute(ctx, []byte(`{"query":"nonexistent term xyz","path":"`+tmpDir+`"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !strings.Contains(result, "No relevant files found") {
		t.Errorf("Expected 'No relevant files found', got: %s", result)
	}
}

func TestIsBinaryFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Text file
	textFile := filepath.Join(tmpDir, "text.txt")
	if err := os.WriteFile(textFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	// Binary file (with null byte)
	binFile := filepath.Join(tmpDir, "binary.bin")
	if err := os.WriteFile(binFile, []byte{0x00, 0x01, 0x02}, 0644); err != nil {
		t.Fatal(err)
	}

	if isBinaryFile(textFile) {
		t.Errorf("text.txt should not be detected as binary")
	}

	if !isBinaryFile(binFile) {
		t.Errorf("binary.bin should be detected as binary")
	}
}
