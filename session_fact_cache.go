package cc

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Fact represents a single piece of knowledge extracted from tool output.
type Fact struct {
	Category string // "definition", "reference", "import", "file_structure", "insertion_point"
	Content  string // one-line description
}

// SessionFactCache accumulates reusable facts from tool outputs within a session.
// Facts are injected into the system prompt so the model doesn't need to re-read files.
type SessionFactCache struct {
	mu       sync.RWMutex
	facts    []Fact
	maxFacts int
	seen     map[string]bool // dedup by content
}

// NewSessionFactCache creates a fact cache with the given max capacity.
func NewSessionFactCache(maxFacts int) *SessionFactCache {
	return &SessionFactCache{
		maxFacts: maxFacts,
		seen:     make(map[string]bool),
	}
}

var (
	// grepMatchRe matches grep output lines like "file.py:123: content"
	grepMatchRe = regexp.MustCompile(`^(.+?):(\d+):\s*(.+)$`)
	// defRe matches Python/Go/etc function/class definitions
	defRe = regexp.MustCompile(`(?m)^\s*(def |class |func )\w+`)
	// defLineRe extracts def/class with name
	defLineRe = regexp.MustCompile(`(?:def |class |func )(\w+)`)
	// importRe matches import statements in Python/Go
	importRe = regexp.MustCompile(`(?m)^\s*(?:import\s+|from\s+.+\s+import\s+)`)
)

// Extract parses tool output and adds relevant facts to the cache.
func (c *SessionFactCache) Extract(toolName, output string) {
	switch toolName {
	case "grep":
		c.extractGrep(output)
	case "read_file":
		c.extractReadFile(output)
	case "edit_file":
		c.extractEditFile(output)
	}
}

func (c *SessionFactCache) extractGrep(output string) {
	lines := strings.Split(output, "\n")
	seenFiles := make(map[string]bool)

	for _, line := range lines {
		m := grepMatchRe.FindStringSubmatch(line)
		if m != nil {
			file, lineNum, content := m[1], m[2], m[3]

			// Record file path index
			if !seenFiles[file] {
				c.addFact(Fact{
					Category: "file_structure",
					Content:  file,
				})
				seenFiles[file] = true
			}

			content = strings.TrimSpace(content)
			if len(content) > 80 {
				content = content[:80] + "..."
			}
			c.addFact(Fact{
				Category: "reference",
				Content:  fmt.Sprintf("%s:%s %s", file, lineNum, content),
			})
		}
	}
}

func (c *SessionFactCache) extractReadFile(output string) {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		// Extract import statements
		if importRe.MatchString(line) {
			importStmt := strings.TrimSpace(line)
			if len(importStmt) > 80 {
				importStmt = importStmt[:80] + "..."
			}
			c.addFact(Fact{
				Category: "import",
				Content:  importStmt,
			})
		}

		// Extract definitions
		if defRe.MatchString(line) {
			m := defLineRe.FindStringSubmatch(line)
			if m != nil {
				name := m[1]
				keyword := strings.TrimSpace(line)
				if strings.HasPrefix(keyword, "class ") {
					c.addFact(Fact{
						Category: "definition",
						Content:  fmt.Sprintf("class %s at line %d", name, i+1),
					})
				} else {
					c.addFact(Fact{
						Category: "definition",
						Content:  fmt.Sprintf("func %s at line %d", name, i+1),
					})
				}
			}
		}
	}
}

func (c *SessionFactCache) extractEditFile(output string) {
	if strings.Contains(output, "Replaced in") {
		// Extract file path from "Replaced in /path/to/file (...)"
		parts := strings.SplitN(output, " (", 2)
		path := strings.TrimPrefix(parts[0], "Replaced in ")
		c.addFact(Fact{
			Category: "insertion_point",
			Content:  fmt.Sprintf("edited %s", path),
		})
	}
}

// categoryLimits defines the maximum number of facts per category.
var categoryLimits = map[string]int{
	"import":         30,
	"file_structure": 50,
	"definition":     40,
	"reference":      80,
}

func (c *SessionFactCache) addFact(f Fact) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.seen[f.Content] {
		return
	}

	// Per-category eviction
	limit := categoryLimits[f.Category]
	if limit == 0 {
		limit = c.maxFacts
	}

	count := 0
	for _, fact := range c.facts {
		if fact.Category == f.Category {
			count++
		}
	}

	if count >= limit {
		// Evict oldest fact in this category
		for i, fact := range c.facts {
			if fact.Category == f.Category {
				delete(c.seen, fact.Content)
				c.facts = append(c.facts[:i], c.facts[i+1:]...)
				break
			}
		}
	}

	// Global cap still applies
	if len(c.facts) >= c.maxFacts {
		oldest := c.facts[0]
		delete(c.seen, oldest.Content)
		c.facts = c.facts[1:]
	}

	c.facts = append(c.facts, f)
	c.seen[f.Content] = true
}

// Render formats all facts as a string suitable for system prompt injection.
func (c *SessionFactCache) Render() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.facts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Session facts]\n")
	for _, f := range c.facts {
		fmt.Fprintf(&b, "- %s: %s\n", f.Category, f.Content)
	}
	return b.String()
}

// Facts returns the current facts (for testing).
func (c *SessionFactCache) Facts() []Fact {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]Fact, len(c.facts))
	copy(result, c.facts)
	return result
}
