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

func TestSkillRegistry_ActivateDeactivate(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "s1", Description: "Skill 1"},
		Instructions: "Instructions for s1",
	})

	if err := r.Activate("s1"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if active := r.ActiveSkills(); len(active) != 1 || active[0] != "s1" {
		t.Errorf("expected [s1] active, got %v", active)
	}

	// Idempotent
	if err := r.Activate("s1"); err != nil {
		t.Fatalf("re-activate: %v", err)
	}
	if len(r.ActiveSkills()) != 1 {
		t.Error("expected idempotent activation")
	}

	r.Deactivate("s1")
	if len(r.ActiveSkills()) != 0 {
		t.Error("expected empty after deactivate")
	}
}

func TestSkillRegistry_ActivateNotFound(t *testing.T) {
	r := NewSkillRegistry()
	if err := r.Activate("nonexistent"); err == nil {
		t.Error("expected error for nonexistent skill")
	}
}

func TestSkillRegistry_MaxActive(t *testing.T) {
	r := NewSkillRegistry()
	r.maxActive = 2

	for _, name := range []string{"a", "b", "c"} {
		r.Register(&Skill{
			Meta:         SkillMeta{Name: name, Description: name},
			Instructions: "inst " + name,
		})
	}

	r.Activate("a")
	r.Activate("b")
	r.Activate("c") // should evict "a"

	active := r.ActiveSkills()
	if len(active) != 2 {
		t.Fatalf("expected 2 active, got %d: %v", len(active), active)
	}
	// "a" should be evicted
	for _, n := range active {
		if n == "a" {
			t.Error("expected 'a' to be evicted")
		}
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
	if s := r.Match("I need to run a migration"); s == nil || s.Meta.Name != "db-migrate" {
		t.Errorf("expected db-migrate match, got %v", s)
	}

	// Should not match deploy (auto_match=false)
	if s := r.Match("deploy to production"); s != nil {
		t.Errorf("expected no match for deploy, got %s", s.Meta.Name)
	}

	// No match
	if s := r.Match("hello world"); s != nil {
		t.Errorf("expected no match, got %s", s.Meta.Name)
	}

	// Already active skill should not re-match
	r.Activate("db-migrate")
	if s := r.Match("run migration again"); s != nil {
		t.Errorf("expected no match for already active skill, got %v", s)
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

func TestSkillRegistry_ActiveInstructions(t *testing.T) {
	r := NewSkillRegistry()
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "s1", Description: "S1"},
		Instructions: "Do step 1\nDo step 2",
	})
	r.Register(&Skill{
		Meta:         SkillMeta{Name: "s2", Description: "S2"},
		Instructions: "Do step A",
	})

	// No active skills
	if r.ActiveInstructions() != "" {
		t.Error("expected empty when no skills active")
	}

	r.Activate("s1")
	r.Activate("s2")

	inst := r.ActiveInstructions()
	if !containsStr(inst, "Do step 1") || !containsStr(inst, "Do step A") {
		t.Errorf("expected both skill instructions, got: %s", inst)
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

	// Activate and check instructions
	r.Activate("my-skill")
	inst := r.ActiveInstructions()
	if !containsStr(inst, "Do the thing") {
		t.Errorf("expected instructions loaded, got: %s", inst)
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

	r.Activate("override")
	inst := r.ActiveInstructions()
	if !containsStr(inst, "Code instructions") {
		t.Errorf("expected code version to win, got: %s", inst)
	}
}

func TestParseSkillMD(t *testing.T) {
	content := `---
name: test-skill
description: "A test"
auto_match: true
keywords: ["foo", "bar"]
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
		t.Error("expected auto_match=true")
	}
	if len(meta.Keywords) != 2 || meta.Keywords[0] != "foo" || meta.Keywords[1] != "bar" {
		t.Errorf("keywords = %v", meta.Keywords)
	}
	if !containsStr(body, "First step") {
		t.Errorf("body = %q", body)
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

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && findStr(s, sub)
}

func findStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
