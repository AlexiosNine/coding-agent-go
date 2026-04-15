package cc

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Sandbox defines file system access restrictions for tools.
type Sandbox struct {
	// AllowedPaths: whitelist of allowed directories (absolute paths).
	// Empty means no restrictions.
	AllowedPaths []string

	// BlockedPatterns: regex patterns that block execution.
	// Checked against shell commands and file paths.
	BlockedPatterns []*regexp.Regexp
}

// DefaultSandbox returns a sandbox with common dangerous patterns blocked.
func DefaultSandbox() *Sandbox {
	return &Sandbox{
		BlockedPatterns: []*regexp.Regexp{
			// Dangerous rm patterns
			regexp.MustCompile(`rm\s+(-[rf]+\s+)?[/*]`),                    // rm -rf /, rm /*
			regexp.MustCompile(`rm\s+(-[rf]+\s+)?\.\*`),                    // rm -rf .*
			regexp.MustCompile(`rm\s+(-[rf]+\s+)?~`),                       // rm -rf ~
			regexp.MustCompile(`rm\s+(-[rf]+\s+)?/[a-z]+`),                 // rm -rf /usr, /etc, etc.

			// Dangerous system operations
			regexp.MustCompile(`mkfs`),                                      // format filesystem
			regexp.MustCompile(`dd\s+.*of=/dev/`),                          // overwrite disk
			regexp.MustCompile(`:\(\)\{.*:\|:.*\};:`),                      // fork bomb
			regexp.MustCompile(`chmod\s+777`),                              // dangerous permissions
			regexp.MustCompile(`curl.*\|\s*(bash|sh)`),                     // pipe to shell
			regexp.MustCompile(`wget.*\|\s*(bash|sh)`),                     // pipe to shell

			// Privilege escalation
			regexp.MustCompile(`sudo\s+rm`),
			regexp.MustCompile(`sudo\s+chmod`),
		},
	}
}

// CheckPath validates if a file path is allowed.
// Returns error if path is outside allowed directories.
func (s *Sandbox) CheckPath(path string) error {
	if len(s.AllowedPaths) == 0 {
		return nil // no restrictions
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	for _, allowed := range s.AllowedPaths {
		allowedAbs, err := filepath.Abs(allowed)
		if err != nil {
			continue
		}
		// Check if path is within allowed directory
		rel, err := filepath.Rel(allowedAbs, absPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return nil // path is within allowed directory
		}
	}

	return fmt.Errorf("access denied: %s is outside allowed paths", absPath)
}

// CheckCommand validates if a shell command is allowed.
// Returns error if command matches blocked patterns.
func (s *Sandbox) CheckCommand(cmd string) error {
	normalized := strings.ToLower(strings.TrimSpace(cmd))

	for _, pattern := range s.BlockedPatterns {
		if pattern.MatchString(normalized) {
			return fmt.Errorf("blocked command: matches dangerous pattern %q", pattern.String())
		}
	}

	return nil
}

// StrictSandbox returns a sandbox with both path whitelist and pattern blocking.
func StrictSandbox(allowedPaths []string) *Sandbox {
	s := DefaultSandbox()
	s.AllowedPaths = allowedPaths
	return s
}

// CheckToolCall inspects tool input JSON and validates against sandbox rules.
// It extracts paths and commands from known tool input schemas.
func (s *Sandbox) CheckToolCall(toolName string, input json.RawMessage) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(input, &raw); err != nil {
		return nil // can't parse, let tool handle validation
	}

	// Check "command" field (shell tool)
	if cmd, ok := raw["command"]; ok {
		var command string
		if json.Unmarshal(cmd, &command) == nil {
			if err := s.CheckCommand(command); err != nil {
				return err
			}
		}
	}

	// Check "path" field (read_file, write_file, list_files)
	if p, ok := raw["path"]; ok {
		var path string
		if json.Unmarshal(p, &path) == nil && path != "" {
			if err := s.CheckPath(path); err != nil {
				return err
			}
		}
	}

	// Check "content" for write_file — block if writing shell scripts with dangerous content
	if toolName == "write_file" {
		if c, ok := raw["content"]; ok {
			var content string
			if json.Unmarshal(c, &content) == nil {
				if err := s.CheckCommand(content); err != nil {
					return fmt.Errorf("file content blocked: %w", err)
				}
			}
		}
	}

	return nil
}
