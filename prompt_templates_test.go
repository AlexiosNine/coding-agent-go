package cc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptTemplateRegistryRender(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "swebench"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "swebench", "case.md"), []byte("Repo: {{repo}}\nIssue: {{issue}}"), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	registry := NewPromptTemplateRegistry(root)
	got, err := registry.Render("swebench/case.md", map[string]string{
		"repo":  "sympy/sympy",
		"issue": "ccode(sinc(x))",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, "Repo: sympy/sympy") || !strings.Contains(got, "Issue: ccode(sinc(x))") {
		t.Fatalf("unexpected render:\n%s", got)
	}
}

func TestPromptTemplateRegistryRenderManyKeepsOrder(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"base.md":  "base {{value}}",
		"extra.md": "extra {{value}}",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got, err := NewPromptTemplateRegistry(root).RenderMany([]string{"base.md", "extra.md"}, map[string]string{"value": "ok"})
	if err != nil {
		t.Fatalf("render many: %v", err)
	}
	if got != "base ok\n\nextra ok" {
		t.Fatalf("unexpected render order: %q", got)
	}
}

func TestPromptTemplateRegistryRejectsUnsafeName(t *testing.T) {
	_, err := NewPromptTemplateRegistry(t.TempDir()).Render("../secret.md", nil)
	if err == nil {
		t.Fatal("expected unsafe template name error")
	}
}

func TestPromptTemplateRegistryMissingTemplate(t *testing.T) {
	_, err := NewPromptTemplateRegistry(t.TempDir()).Render("missing.md", nil)
	if err == nil {
		t.Fatal("expected missing template error")
	}
}
