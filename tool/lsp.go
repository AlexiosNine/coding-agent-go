package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
)

type lspInput struct {
	Operation string `json:"operation" desc:"LSP operation: definition, references, hover, symbols, workspace_symbol"`
	File      string `json:"file" desc:"Absolute file path"`
	Line      int    `json:"line,omitempty" desc:"Line number (1-indexed)"`
	Column    int    `json:"column,omitempty" desc:"Column number (1-indexed)"`
	Query     string `json:"query,omitempty" desc:"Symbol query for workspace_symbol"`
}

// LSP returns a multi-language LSP tool that routes to the appropriate language server.
func LSP() cc.Tool {
	return cc.NewFuncTool(
		"lsp",
		"Query language servers for code intelligence. Operations: definition, references, hover, symbols, workspace_symbol.",
		func(ctx context.Context, input lspInput) (string, error) {
			if input.Operation == "" {
				return "", fmt.Errorf("operation is required")
			}
			if input.File == "" {
				return "", fmt.Errorf("file is required")
			}

			absFile, err := filepath.Abs(input.File)
			if err != nil {
				return "", fmt.Errorf("invalid file path: %w", err)
			}
			if _, err := os.Stat(absFile); err != nil {
				return "", fmt.Errorf("file not found: %w", err)
			}

			// Detect language and get server config
			lang := detectLanguage(absFile)
			if lang == "" {
				return "", fmt.Errorf("unsupported file type: %s", filepath.Ext(absFile))
			}

			cfg := getServerConfig(lang)
			if cfg == nil {
				return "", fmt.Errorf("no language server configured for %s", lang)
			}

			// Check if binary exists
			if _, err := exec.LookPath(cfg.Binary); err != nil {
				return "", fmt.Errorf("%s not found. Install: %s", cfg.Binary, cfg.InstallHint)
			}

			// Find project root
			root := findProjectRoot(absFile, cfg.RootMarkers)

			// Dispatch to backend
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			switch cfg.Mode {
			case ModeCLI:
				return executeCLI(ctx, cfg, root, input.Operation, absFile, input.Line, input.Column, input.Query)
			case ModeJSONRPC:
				return "", fmt.Errorf("%s requires JSON-RPC mode (not yet implemented, use shell tool to call %s directly)", cfg.Binary, cfg.Binary)
			default:
				return "", fmt.Errorf("unknown server mode for %s", lang)
			}
		},
	)
}

// findProjectRoot walks up from file to find a project root marker.
func findProjectRoot(filePath string, markers []string) string {
	dir := filepath.Dir(filePath)
	for {
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Dir(filePath)
		}
		dir = parent
	}
}
