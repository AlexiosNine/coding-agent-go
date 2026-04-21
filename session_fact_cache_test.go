package cc

import (
	"strings"
	"testing"
)

func TestSessionFactCache_ExtractGrep(t *testing.T) {
	c := NewSessionFactCache(20)
	output := `Found 2 match(es) for "def _print_sinc" (showing 1-2):

sympy/printing/octave.py:395:     def _print_sinc(self, expr):
sympy/printing/fcode.py:200:     def _print_sinc(self, expr):
`
	c.Extract("grep", output)

	facts := c.Facts()
	// Now expects 4 facts: 2 file_structure + 2 reference
	if len(facts) != 4 {
		t.Fatalf("expected 4 facts (2 file_structure + 2 reference), got %d", len(facts))
	}

	// Order: file_structure(octave), reference(octave), file_structure(fcode), reference(fcode)
	if facts[0].Category != "file_structure" {
		t.Errorf("expected category=file_structure, got %s", facts[0].Category)
	}
	if !strings.Contains(facts[0].Content, "octave.py") {
		t.Errorf("expected octave.py in fact, got: %s", facts[0].Content)
	}

	// Check reference facts
	if facts[1].Category != "reference" {
		t.Errorf("expected category=reference, got %s", facts[1].Category)
	}
	if !strings.Contains(facts[1].Content, "octave.py:395") {
		t.Errorf("expected octave.py:395 in fact, got: %s", facts[1].Content)
	}
	if !strings.Contains(facts[3].Content, "fcode.py:200") {
		t.Errorf("expected fcode.py:200 in fact, got: %s", facts[3].Content)
	}
}

func TestSessionFactCache_ExtractReadFile(t *testing.T) {
	c := NewSessionFactCache(20)
	output := `class CCodePrinter(CodePrinter):
    def __init__(self, settings={}):
        pass

    def _print_Pow(self, expr):
        return "pow"

    def _print_sign(self, func):
        return "sign"
`
	c.Extract("read_file", output)

	facts := c.Facts()
	if len(facts) != 4 {
		t.Fatalf("expected 4 facts (1 class + 3 def), got %d: %v", len(facts), facts)
	}
	if facts[0].Category != "definition" || !strings.Contains(facts[0].Content, "class CCodePrinter") {
		t.Errorf("expected class CCodePrinter fact, got: %+v", facts[0])
	}
}

func TestSessionFactCache_ExtractEditFile(t *testing.T) {
	c := NewSessionFactCache(20)
	output := "Replaced in /tmp/swebench/sympy/printing/ccode.py (14048 bytes → 14319 bytes). No need to re-read."
	c.Extract("edit_file", output)

	facts := c.Facts()
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Category != "insertion_point" {
		t.Errorf("expected category=insertion_point, got %s", facts[0].Category)
	}
	if !strings.Contains(facts[0].Content, "ccode.py") {
		t.Errorf("expected ccode.py in fact, got: %s", facts[0].Content)
	}
}

func TestSessionFactCache_Dedup(t *testing.T) {
	c := NewSessionFactCache(20)
	c.Extract("grep", "file.py:10: def foo")
	c.Extract("grep", "file.py:10: def foo")

	// Now expects 2 facts: 1 file_structure + 1 reference (deduped)
	if len(c.Facts()) != 2 {
		t.Errorf("expected 2 facts (1 file_structure + 1 reference), got %d", len(c.Facts()))
	}
}

func TestSessionFactCache_MaxFacts(t *testing.T) {
	c := NewSessionFactCache(3)
	c.Extract("grep", "a.py:1: match1")
	c.Extract("grep", "b.py:2: match2")
	c.Extract("grep", "c.py:3: match3")
	c.Extract("grep", "d.py:4: match4")

	facts := c.Facts()
	if len(facts) != 3 {
		t.Fatalf("expected max 3 facts, got %d", len(facts))
	}
	// Oldest (a.py) should be evicted
	if strings.Contains(facts[0].Content, "a.py:1") {
		t.Error("expected oldest fact evicted")
	}
}

func TestSessionFactCache_Render(t *testing.T) {
	c := NewSessionFactCache(20)
	c.Extract("grep", "octave.py:395: def _print_sinc(self, expr):")
	c.Extract("edit_file", "Replaced in /tmp/ccode.py (100 bytes → 200 bytes).")

	rendered := c.Render()
	if !strings.Contains(rendered, "[Session facts]") {
		t.Error("expected [Session facts] header")
	}
	if !strings.Contains(rendered, "reference:") {
		t.Error("expected reference fact")
	}
	if !strings.Contains(rendered, "insertion_point:") {
		t.Error("expected insertion_point fact")
	}
}

func TestSessionFactCache_RenderEmpty(t *testing.T) {
	c := NewSessionFactCache(20)
	if c.Render() != "" {
		t.Error("expected empty render for empty cache")
	}
}
