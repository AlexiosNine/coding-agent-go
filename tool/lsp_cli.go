package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// executeCLI runs a gopls CLI command and returns formatted output.
func executeCLI(ctx context.Context, cfg *ServerConfig, root, operation, file string, line, col int, query string) (string, error) {
	var args []string

	switch operation {
	case "definition":
		if line <= 0 || col <= 0 {
			return "", fmt.Errorf("line and column are required for definition")
		}
		args = []string{"definition", fmt.Sprintf("%s:%d:%d", file, line, col)}

	case "references":
		if line <= 0 || col <= 0 {
			return "", fmt.Errorf("line and column are required for references")
		}
		args = []string{"references", fmt.Sprintf("%s:%d:%d", file, line, col)}

	case "hover":
		if line <= 0 || col <= 0 {
			return "", fmt.Errorf("line and column are required for hover")
		}
		args = []string{"hover", fmt.Sprintf("%s:%d:%d", file, line, col)}

	case "symbols":
		args = []string{"symbols", file}

	case "workspace_symbol":
		if query == "" {
			return "", fmt.Errorf("query is required for workspace_symbol")
		}
		args = []string{"workspace_symbol", query}

	default:
		return "", fmt.Errorf("unsupported operation: %s (supported: definition, references, hover, symbols, workspace_symbol)", operation)
	}

	cmd := exec.CommandContext(ctx, cfg.Binary, args...)
	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		if output != "" {
			return "", fmt.Errorf("%s %s failed: %s", cfg.Binary, operation, output)
		}
		return "", fmt.Errorf("%s %s failed: %w", cfg.Binary, operation, err)
	}

	if output == "" {
		return fmt.Sprintf("No results for %s at %s:%d:%d", operation, file, line, col), nil
	}

	return formatCLIOutput(operation, output), nil
}

// formatCLIOutput formats gopls CLI output for LLM consumption.
func formatCLIOutput(operation, raw string) string {
	lines := strings.Split(raw, "\n")

	switch operation {
	case "references":
		if len(lines) == 0 {
			return "No references found."
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("References (%d found):\n", len(lines)))
		for i, line := range lines {
			b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, strings.TrimSpace(line)))
		}
		return b.String()

	case "symbols":
		if len(lines) == 0 {
			return "No symbols found."
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Symbols (%d):\n", len(lines)))
		for _, line := range lines {
			b.WriteString(fmt.Sprintf("  %s\n", strings.TrimSpace(line)))
		}
		return b.String()

	default:
		return raw
	}
}
