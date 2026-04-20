package cc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillRegistry_Register(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "test", Description: "A test skill"},
		Instructions: "Do the thing",
	})

	names := r.ListSkills()
	if len(names) != 1 || names[0] != "test" {
		t.Errorf("expected [test], got %v", names)
	}
}

func TestSkillRegistry_GetSkill(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "test", Description: "A test skill"},
		Instructions: "Do the thing",
	})

	skill, ok := r.GetSkill("test")
	if !ok || skill.Meta.Name != "test" {
		t.Error("expected to get skill 'test'")
	}

	_, ok = r.GetSkill("nonexistent")
	if ok {
		t.Error("expected false for nonexistent skill")
	}
}

func TestSkillRegistry_GetInstructions(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "s1", Description: "Skill 1"},
		Instructions: "Instructions for s1",
	})
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "s2", Description: "Skill 2"},
		Instructions: "Instructions for s2",
	})

	// No active skills
	if r.GetInstructions([]string{}) != "" {
		t.Error("expected empty when no skills requested")
	}

	// Single skill
	inst := r.GetInstructions([]string{"s1"})
	if !containsStr(inst, "Instructions for s1") {
		t.Errorf("expected s1 instructions, got: %s", inst)
	}

	// Multiple skills
	inst = r.GetInstructions([]string{"s1", "s2"})
	if !containsStr(inst, "Instructions for s1") || !containsStr(inst, "Instructions for s2") {
		t.Errorf("expected both instructions, got: %s", inst)
	}
}

func TestSkillRegistry_Match(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta: SkillMeta{
			Name:        "db-migrate",
			Description: "Database migrations",
			AutoMatch:   true,
			Keywords:    []string{"migration", "schema"},
		},
		Instructions: "Run migrations",
	})
	r.Register(&Skill{
		Meta: SkillMeta{
			Name:        "deploy",
			Description: "Deploy to production",
			AutoMatch:   false, // disabled
			Keywords:    []string{"deploy"},
		},
		Instructions: "Deploy steps",
	})

	// Should match db-migrate
	if s := r.Match("I need to run a migration", []string{}); s == nil || s.Meta.Name != "db-migrate" {
		t.Errorf("expected db-migrate match, got %v", s)
	}

	// Should not match deploy (auto_match=false)
	if s := r.Match("deploy to production", []string{}); s != nil {
		t.Errorf("expected no match for deploy, got %s", s.Meta.Name)
	}

	// No match
	if s := r.Match("hello world", []string{}); s != nil {
		t.Errorf("expected no match, got %s", s.Meta.Name)
	}

	// Already active skill should not re-match
	if s := r.Match("run migration again", []string{"db-migrate"}); s != nil {
		t.Errorf("expected no match for already active skill, got %v", s)
	}
}

func TestSkillRegistry_Match_WordBoundary(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta: SkillMeta{
			Name:        "fix-bug",
			Description: "Fix bugs",
			AutoMatch:   true,
			Keywords:    []string{"fix"},
		},
		Instructions: "Fix the bug",
	})

	// Should match "fix" as whole word
	if s := r.Match("I need to fix this bug", []string{}); s == nil {
		t.Error("expected match for 'fix this'")
	}

	// Should NOT match "fix" inside "prefix"
	if s := r.Match("add a prefix to the variable", []string{}); s != nil {
		t.Errorf("expected no match for 'prefix', got %s", s.Meta.Name)
	}

	// Should match at start
	if s := r.Match("fix: update config", []string{}); s == nil {
		t.Error("expected match for 'fix:' at start")
	}

	// Should match at end
	if s := r.Match("apply the fix", []string{}); s == nil {
		t.Error("expected match for 'fix' at end")
	}
}

func TestSkillRegistry_Match_Priority(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta: SkillMeta{
			Name:        "low-priority",
			Description: "Low",
			AutoMatch:   true,
			Keywords:    []string{"test"},
			Priority:    1,
		},
		Instructions: "Low priority",
	})
	r.Register(&Skill{
		Meta: SkillMeta{
			Name:        "high-priority",
			Description: "High",
			AutoMatch:   true,
			Keywords:    []string{"test"},
			Priority:    10,
		},
		Instructions: "High priority",
	})

	// Should match high-priority (higher priority value)
	s := r.Match("run test", []string{})
	if s == nil || s.Meta.Name != "high-priority" {
		t.Errorf("expected high-priority match, got %v", s)
	}
}

func TestSkillRegistry_Summary(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "commit", Description: "Git commit workflow"},
		Instructions: "...",
	})

	summary := r.Summary()
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !containsStr(summary, "commit") || !containsStr(summary, "Git commit workflow") {
		t.Errorf("summary missing skill info: %s", summary)
	}
}

func TestSkillRegistry_LoadDir(t *testing.T) {
	dir := t.TempDir()

	// Create a skill directory
	skillDir := filepath.Join(dir, "my-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: my-skill
description: "A test skill from file"
auto_match: true
keywords: ["test", "demo"]
---

## Instructions

1. Do the thing
2. Verify it worked
`), 0644)

	r := NewSkillRegistry()
	if err := r.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	names := r.ListSkills()
	if len(names) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(names))
	}

	// Check lazy loading: instructions should be empty initially
	skill, _ := r.GetSkill("my-skill")
	if skill.Instructions != "" {
		t.Error("expected empty instructions before lazy load")
	}

	// Get instructions triggers lazy load
	inst := r.GetInstructions([]string{"my-skill"})
	if !containsStr(inst, "Do the thing") {
		t.Errorf("expected instructions loaded, got: %s", inst)
	}

	// Now instructions should be loaded
	skill, _ = r.GetSkill("my-skill")
	if !skill.loaded {
		t.Error("expected skill to be marked as loaded")
	}
}

func TestSkillRegistry_LoadDir_CodeOverridesFile(t *testing.T) {
	dir := t.TempDir()

	skillDir := filepath.Join(dir, "override")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: override
description: "File version"
---
File instructions
`), 0644)

	r := NewSkillRegistry()
	// Code-register first
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "override", Description: "Code version"},
		Instructions: "Code instructions",
	})
	// Then load dir — should NOT overwrite
	r.LoadDir(dir)

	inst := r.GetInstructions([]string{"override"})
	if !containsStr(inst, "Code instructions") {
		t.Errorf("expected code version to win, got: %s", inst)
	}
}

func TestSkillRegistry_ConcurrentAccess(t *testing.T) {
	r := NewSkillRegistry()

	// Concurrent Register + GetSkill
	done := make(chan bool, 2)
	go func() {
		for i := 0; i < 100; i++ {
			r.Register(&Skill{
				Meta:         SkillMeta{Name: "test", Description: "Test"},
				Instructions: "Test instructions",
			})
		}
		done <- true
	}()
	go func() {
		for i := 0; i < 100; i++ {
			r.GetSkill("test")
		}
		done <- true
	}()

	<-done
	<-done
}

func TestParseSkillMD(t *testing.T) {
	content := `---
name: test-skill
description: "A test"
auto_match: true
keywords: ["foo", "bar"]
priority: 5
---

## Steps

1. First step
2. Second step
`
	meta, body, err := parseSkillMD(content)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if meta.Name != "test-skill" {
		t.Errorf("name = %q", meta.Name)
	}
	if meta.Description != "A test" {
		t.Errorf("description = %q", meta.Description)
	}
	if !meta.AutoMatch {
		t.Error("expected auto_match = true")
	}
	if len(meta.Keywords) != 2 || meta.Keywords[0] != "foo" || meta.Keywords[1] != "bar" {
		t.Errorf("keywords = %v", meta.Keywords)
	}
	if meta.Priority != 5 {
		t.Errorf("priority = %d", meta.Priority)
	}
	if !containsStr(body, "First step") {
		t.Errorf("body missing content: %s", body)
	}
}

func TestParseSkillMD_MissingName(t *testing.T) {
	content := `---
description: "No name"
---
Body
`
	_, _, err := parseSkillMD(content)
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		len(s) > len(substr)+1 && containsSubstr(s, substr)))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
