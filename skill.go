package cc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SkillMeta contains the lightweight metadata parsed from SKILL.md frontmatter.
type SkillMeta struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	AutoMatch   bool     `yaml:"auto_match"`
	Keywords    []string `yaml:"keywords"`
	Priority    int      `yaml:"priority"` // higher priority matched first (default 0)
}

// Skill represents a reusable capability or workflow that can be activated on demand.
type Skill struct {
	Meta         SkillMeta
	Instructions string // full SKILL.md body (lazy loaded for file-based skills)
	Tools        []Tool // optional tools provided by this skill
	filePath     string // SKILL.md path (empty for code-registered skills)
	loaded       bool   // whether Instructions has been loaded from file
}

// SkillRegistry manages skill discovery and metadata.
// Active state is managed per-session, not globally.
type SkillRegistry struct {
	mu           sync.RWMutex
	skills       map[string]*Skill
	orderedNames []string // deterministic iteration order for Match
}

// NewSkillRegistry creates a registry.
func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{
		skills:       make(map[string]*Skill),
		orderedNames: []string{},
	}
}

// Register adds a code-defined skill. Overwrites any existing skill with the same name.
func (r *SkillRegistry) Register(skill *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	skill.loaded = true // code-registered skills are always fully loaded

	// Add to ordered list if new
	if _, exists := r.skills[skill.Meta.Name]; !exists {
		r.orderedNames = append(r.orderedNames, skill.Meta.Name)
	}
	r.skills[skill.Meta.Name] = skill
}

// LoadDir scans a directory for SKILL.md files and registers them.
// Only parses frontmatter; instructions are lazy-loaded on first access.
func (r *SkillRegistry) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // silent skip
		}
		return fmt.Errorf("read skill dir %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			continue
		}

		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}

		meta, _, err := parseSkillMD(string(data))
		if err != nil {
			continue // skip malformed skills
		}

		// Don't overwrite code-registered skills
		r.mu.Lock()
		if _, exists := r.skills[meta.Name]; !exists {
			r.skills[meta.Name] = &Skill{
				Meta:         meta,
				Instructions: "", // lazy load
				filePath:     skillFile,
				loaded:       false,
			}
			r.orderedNames = append(r.orderedNames, meta.Name)
		}
		r.mu.Unlock()
	}
	return nil
}

// GetSkill returns a skill by name with thread-safe access.
func (r *SkillRegistry) GetSkill(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skill, ok := r.skills[name]
	return skill, ok
}

// loadInstructions loads the full instructions from file if not already loaded.
func (r *SkillRegistry) loadInstructions(skill *Skill) error {
	if skill.loaded || skill.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(skill.filePath)
	if err != nil {
		return fmt.Errorf("read skill file %s: %w", skill.filePath, err)
	}

	_, body, err := parseSkillMD(string(data))
	if err != nil {
		return fmt.Errorf("parse skill file %s: %w", skill.filePath, err)
	}

	skill.Instructions = body
	skill.loaded = true
	return nil
}

// GetInstructions returns the combined instructions for the given skill names.
// Lazy-loads file-based skills on first access.
func (r *SkillRegistry) GetInstructions(names []string) string {
	if len(names) == 0 {
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var b strings.Builder
	for _, name := range names {
		skill, ok := r.skills[name]
		if !ok {
			continue
		}
		// Lazy load if needed
		if err := r.loadInstructions(skill); err != nil {
			continue
		}
		fmt.Fprintf(&b, "[Active skill: %s]\n%s\n\n", name, skill.Instructions)
	}
	return b.String()
}

// Match checks if any auto-match skill's keywords appear in the text.
// Returns the highest-priority matching skill, or nil.
// Uses word-boundary matching to avoid false positives.
func (r *SkillRegistry) Match(text string, activeSkills []string) *Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(text)

	// Build list of matching skills with priorities
	type match struct {
		skill    *Skill
		priority int
	}
	var matches []match

	for _, name := range r.orderedNames {
		skill := r.skills[name]
		if !skill.Meta.AutoMatch {
			continue
		}

		// Skip already active
		isActive := false
		for _, activeName := range activeSkills {
			if activeName == name {
				isActive = true
				break
			}
		}
		if isActive {
			continue
		}

		// Check keywords with word boundaries
		for _, kw := range skill.Meta.Keywords {
			if matchKeyword(lower, strings.ToLower(kw)) {
				matches = append(matches, match{skill: skill, priority: skill.Meta.Priority})
				break
			}
		}
	}

	if len(matches) == 0 {
		return nil
	}

	// Return highest priority (or first if tied)
	best := matches[0]
	for _, m := range matches[1:] {
		if m.priority > best.priority {
			best = m
		}
	}
	return best.skill
}

// Summary returns a concise listing of all skills for system prompt injection.
func (r *SkillRegistry) Summary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Available skills]\n")
	for _, name := range r.orderedNames {
		skill := r.skills[name]
		fmt.Fprintf(&b, "- %s: %s\n", skill.Meta.Name, skill.Meta.Description)
	}
	b.WriteString("Use the use_skill tool to activate a skill.\n")
	return b.String()
}

// ListSkills returns all registered skill names.
func (r *SkillRegistry) ListSkills() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, len(r.orderedNames))
	copy(names, r.orderedNames)
	return names
}

// matchKeyword checks if keyword appears as a whole word in text.
// Both text and keyword should be lowercase.
func matchKeyword(text, keyword string) bool {
	idx := 0
	for {
		pos := strings.Index(text[idx:], keyword)
		if pos < 0 {
			return false
		}
		pos += idx

		// Check left boundary
		if pos > 0 && isWordChar(text[pos-1]) {
			idx = pos + 1
			continue
		}

		// Check right boundary
		end := pos + len(keyword)
		if end < len(text) && isWordChar(text[end]) {
			idx = pos + 1
			continue
		}

		return true
	}
}

func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// parseSkillMD parses a SKILL.md file into metadata and body.
// Expects YAML frontmatter between --- delimiters.
func parseSkillMD(content string) (SkillMeta, string, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return SkillMeta{}, "", fmt.Errorf("missing frontmatter delimiter")
	}

	// Find closing ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return SkillMeta{}, "", fmt.Errorf("unclosed frontmatter")
	}

	frontmatter := strings.TrimSpace(rest[:idx])
	body := strings.TrimSpace(rest[idx+4:])

	meta := SkillMeta{}
	// Simple YAML parser (avoid external dependency)
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, "\"'")

		switch key {
		case "name":
			meta.Name = val
		case "description":
			meta.Description = val
		case "auto_match":
			meta.AutoMatch = val == "true"
		case "priority":
			fmt.Sscanf(val, "%d", &meta.Priority)
		case "keywords":
			// Parse simple YAML list: [a, b, c]
			val = strings.Trim(val, "[]")
			for _, kw := range strings.Split(val, ",") {
				kw = strings.TrimSpace(kw)
				kw = strings.Trim(kw, "\"'")
				if kw != "" {
					meta.Keywords = append(meta.Keywords, kw)
				}
			}
		}
	}

	if meta.Name == "" {
		return SkillMeta{}, "", fmt.Errorf("skill name is required")
	}

	return meta, body, nil
}
