package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGrep_DefaultRecursive(t *testing.T) {
	// Create nested directory structure
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0755)
	os.WriteFile(filepath.Join(dir, "top.py"), []byte("class TopLevel:\n    pass\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "mid.py"), []byte("class MidLevel:\n    pass\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "deep.py"), []byte("class DeepLevel:\n    pass\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "vdeep.py"), []byte("class VeryDeepLevel:\n    pass\n"), 0644)

	tool := Grep()

	// Default: recursive=true, should find all levels
	input, _ := json.Marshal(map[string]interface{}{
		"pattern": "class.*Level",
		"path":    dir,
	})
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(out, "TopLevel") {
		t.Errorf("expected TopLevel in results, got: %s", out)
	}
	if !contains(out, "MidLevel") {
		t.Errorf("expected MidLevel in results, got: %s", out)
	}
	if !contains(out, "DeepLevel") {
		t.Errorf("expected DeepLevel in results, got: %s", out)
	}
	if !contains(out, "VeryDeepLevel") {
		t.Errorf("expected VeryDeep in results, got: %s", out)
	}
}

func TestGrep_NonRecursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "top.py"), []byte("class Top:\n"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "deep.py"), []byte("class Deep:\n"), 0644)

	tool := Grep()

	f := false
	input, _ := json.Marshal(map[string]interface{}{
		"pattern":   "class",
		"path":      dir,
		"recursive": f,
	})
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(out, "Top") {
		t.Errorf("expected Top in results, got: %s", out)
	}
	if contains(out, "Deep") {
		t.Errorf("should NOT find Deep in non-recursive mode, got: %s", out)
	}
}

func TestGrep_MaxDepth(t *testing.T) {
	dir := t.TempDir()
	// depth 0: dir/
	// depth 1: dir/a/
	// depth 2: dir/a/b/
	// depth 3: dir/a/b/c/
	os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0755)
	os.WriteFile(filepath.Join(dir, "top.py"), []byte("MATCH_TOP\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "d1.py"), []byte("MATCH_D1\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "d2.py"), []byte("MATCH_D2\n"), 0644)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "d3.py"), []byte("MATCH_D3\n"), 0644)

	tool := Grep()

	// max_depth=2: should find top, d1, d2 but NOT d3
	input, _ := json.Marshal(map[string]interface{}{
		"pattern":   "MATCH",
		"path":      dir,
		"max_depth": 2,
	})
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(out, "MATCH_TOP") {
		t.Errorf("expected MATCH_TOP, got: %s", out)
	}
	if !contains(out, "MATCH_D1") {
		t.Errorf("expected MATCH_D1, got: %s", out)
	}
	if !contains(out, "MATCH_D2") {
		t.Errorf("expected MATCH_D2, got: %s", out)
	}
	if contains(out, "MATCH_D3") {
		t.Errorf("should NOT find MATCH_D3 at depth 3, got: %s", out)
	}
}

func TestGrep_SkipsNoisyDirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	os.MkdirAll(filepath.Join(dir, "__pycache__"), 0755)
	os.MkdirAll(filepath.Join(dir, "src"), 0755)

	os.WriteFile(filepath.Join(dir, ".git", "objects", "git.txt"), []byte("SECRET\n"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "nm.txt"), []byte("SECRET\n"), 0644)
	os.WriteFile(filepath.Join(dir, "__pycache__", "cache.txt"), []byte("SECRET\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "main.py"), []byte("SECRET\n"), 0644)

	tool := Grep()

	input, _ := json.Marshal(map[string]interface{}{
		"pattern": "SECRET",
		"path":    dir,
	})
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should only find in src/main.py
	if !contains(out, "main.py") {
		t.Errorf("expected main.py in results, got: %s", out)
	}
	if contains(out, "git.txt") || contains(out, "nm.txt") || contains(out, "cache.txt") {
		t.Errorf("should skip noisy dirs, got: %s", out)
	}
}

func TestGrep_SingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.py")
	os.WriteFile(f, []byte("line1\nmatch_here\nline3\n"), 0644)

	tool := Grep()
	input, _ := json.Marshal(map[string]interface{}{
		"pattern": "match_here",
		"path":    f,
	})
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(out, "match_here") {
		t.Errorf("expected match, got: %s", out)
	}
	if !contains(out, ":2:") {
		t.Errorf("expected line number 2, got: %s", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && findSub(s, sub)
}

func findSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
