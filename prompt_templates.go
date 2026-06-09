package cc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PromptTemplateRegistry loads and renders prompt templates from a directory.
// Template names are slash-separated paths relative to root, for example
// "swebench/case.md".
type PromptTemplateRegistry struct {
	root string
}

// NewPromptTemplateRegistry creates a file-backed prompt template registry.
func NewPromptTemplateRegistry(root string) *PromptTemplateRegistry {
	return &PromptTemplateRegistry{root: root}
}

// RenderPromptTemplateText replaces {{name}} placeholders with data values.
func RenderPromptTemplateText(text string, data map[string]string) string {
	if len(data) == 0 {
		return text
	}
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j])
	})

	replacements := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		replacements = append(replacements, "{{"+key+"}}", data[key])
	}
	return strings.NewReplacer(replacements...).Replace(text)
}

// Render loads a template by name and replaces {{name}} placeholders.
func (r *PromptTemplateRegistry) Render(name string, data map[string]string) (string, error) {
	path, err := r.path(name)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt template %q: %w", name, err)
	}
	return RenderPromptTemplateText(string(raw), data), nil
}

// RenderMany renders templates in order and joins non-empty blocks with blank lines.
func (r *PromptTemplateRegistry) RenderMany(names []string, data map[string]string) (string, error) {
	var blocks []string
	for _, name := range names {
		block, err := r.Render(name, data)
		if err != nil {
			return "", err
		}
		block = strings.TrimSpace(block)
		if block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n\n"), nil
}

func (r *PromptTemplateRegistry) path(name string) (string, error) {
	if r == nil || r.root == "" {
		return "", fmt.Errorf("prompt template root is empty")
	}
	if filepath.IsAbs(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid prompt template name %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || strings.HasPrefix(clean, string(filepath.Separator)) {
		return "", fmt.Errorf("invalid prompt template name %q", name)
	}
	return filepath.Join(r.root, clean), nil
}
