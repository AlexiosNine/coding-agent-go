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
}

// Skill represents a reusable capability or workflow that can be activated on demand.
type Skill struct {
	Meta         SkillMeta
	Instructions string // full SKILL.md body (lazy loaded for file-based skills)
	Tools        []Tool // optional tools provided by this skill
	filePath     string // SKILL.md path (empty for code-registered skills)
	loaded       bool   // whether Instructions has been loaded from file
}

// SkillRegistry manages skill discovery, matching, and activation lifecycle.
type SkillRegistry struct {
	mu        sync.RWMutex
	skills    map[string]*Skill
	active    []string // ordered list of active skill names
	maxActive int
}

// NewSkillRegistry creates a registry with default settings.
func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{
		skills:    make(map[string]*Skill),
		maxActive: 3,
	}
}

// Register adds a code-defined skill. Overwrites any existing skill with the same name.
func (r *SkillRegistry) Register(skill *Skill) {
	r.mu.Lock()
	defer r.mu.Unlock()
	skill.loaded = true // code-registered skills are always fully loaded
	r.skills[skill.Meta.Name] = skill
}

// LoadDir scans a directory for SKILL.md files and registers them.
// Only parses frontmatter at this stage; instructions are lazy-loaded on activation.
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

		meta, body, err := parseSkillMD(string(data))
		if err != nil {
			continue // skip malformed skills
		}

		// Don't overwrite code-registered skills
		r.mu.Lock()
		if _, exists := r.skills[meta.Name]; !exists {
			r.skills[meta.Name] = &Skill{
				Meta:         meta,
				Instructions: body,
				filePath:     skillFile,
				loaded:       true, // we already have the body
			}
		}
		r.mu.Unlock()
	}
	return nil
}

// Activate loads full instructions and registers skill tools.
// If maxActive is exceeded, the oldest active skill is deactivated.
func (r *SkillRegistry) Activate(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	skill, ok := r.skills[name]
	if !ok {
		return fmt.Errorf("skill %q not found", name)
	}

	// Already active — idempotent
	for _, n := range r.active {
		if n == name {
			return nil
		}
	}

	// Evict oldest if at capacity
	if len(r.active) >= r.maxActive {
		r.deactivateLocked(r.active[0])
	}

	r.active = append(r.active, name)
	_ = skill // instructions already loaded
	return nil
}

// Deactivate removes a skill from active set.
func (r *SkillRegistry) Deactivate(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deactivateLocked(name)
}

func (r *SkillRegistry) deactivateLocked(name string) {
	for i, n := range r.active {
		if n == name {
			r.active = append(r.active[:i], r.active[i+1:]...)
			return
		}
	}
}

// Match checks if any auto-match skill's keywords appear in the text.
// Returns the first matching skill, or nil.
func (r *SkillRegistry) Match(text string) *Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(text)
	for _, skill := range r.skills {
		if !skill.Meta.AutoMatch {
			continue
		}
		// Skip already active
		for _, n := range r.active {
			if n == skill.Meta.Name {
				goto next
			}
		}
		for _, kw := range skill.Meta.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return skill
			}
		}
	next:
	}
	return nil
}

// Summary returns a concise listing of all skills for system prompt injection.
// Only includes name + description (~50 tokens per skill).
func (r *SkillRegistry) Summary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Available skills]\n")
	for _, skill := range r.skills {
		fmt.Fprintf(&b, "- %s: %s\n", skill.Meta.Name, skill.Meta.Description)
	}
	b.WriteString("Use the use_skill tool to activate a skill.\n")
	return b.String()
}

// ActiveInstructions returns the combined instructions of all active skills.
func (r *SkillRegistry) ActiveInstructions() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.active) == 0 {
		return ""
	}

	var b strings.Builder
	for _, name := range r.active {
		skill := r.skills[name]
		fmt.Fprintf(&b, "[Active skill: %s]\n%s\n\n", name, skill.Instructions)
	}
	return b.String()
}

// ActiveSkills returns the names of currently active skills.
func (r *SkillRegistry) ActiveSkills() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]string, len(r.active))
	copy(result, r.active)
	return result
}

// ListSkills returns all registered skill names.
func (r *SkillRegistry) ListSkills() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	return names
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
