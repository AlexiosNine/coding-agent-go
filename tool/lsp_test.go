package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"main.go", "go"},
		{"app.py", "python"},
		{"index.ts", "typescript"},
		{"component.tsx", "typescript"},
		{"script.js", "typescript"},
		{"main.rs", "rust"},
		{"App.java", "java"},
		{"unknown.txt", ""},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := detectLanguage(tt.file)
			if got != tt.want {
				t.Errorf("detectLanguage(%q) = %q, want %q", tt.file, got, tt.want)
			}
		})
	}
}

func TestFindProjectRoot(t *testing.T) {
	tmpDir := t.TempDir()

	// Create nested structure: root/subdir/file.go
	root := tmpDir
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create go.mod at root
	gomod := filepath.Join(root, "go.mod")
	if err := os.WriteFile(gomod, []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create file in subdir
	file := filepath.Join(subdir, "main.go")
	if err := os.WriteFile(file, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	// Test: should find root from subdir/main.go
	foundRoot := findProjectRoot(file, []string{"go.mod"})
	if foundRoot != root {
		t.Errorf("findProjectRoot(%q) = %q, want %q", file, foundRoot, root)
	}
}

func TestGetServerConfig(t *testing.T) {
	tests := []struct {
		lang string
		want string // binary name
	}{
		{"go", "gopls"},
		{"python", "pylsp"},
		{"typescript", "typescript-language-server"},
		{"rust", "rust-analyzer"},
		{"java", "jdtls"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			cfg := getServerConfig(tt.lang)
			if tt.want == "" {
				if cfg != nil {
					t.Errorf("getServerConfig(%q) = %v, want nil", tt.lang, cfg)
				}
			} else {
				if cfg == nil {
					t.Errorf("getServerConfig(%q) = nil, want config", tt.lang)
				} else if cfg.Binary != tt.want {
					t.Errorf("getServerConfig(%q).Binary = %q, want %q", tt.lang, cfg.Binary, tt.want)
				}
			}
		})
	}
}
