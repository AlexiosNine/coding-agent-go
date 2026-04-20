package tool

import (
	"os"
	"path/filepath"
	"strings"
)

// ServerMode defines how to communicate with a language server.
type ServerMode int

const (
	ModeCLI     ServerMode = iota // CLI mode (gopls-style)
	ModeJSONRPC                   // JSON-RPC over stdio
)

// ServerConfig describes a language server configuration.
type ServerConfig struct {
	Language    string
	Binary      string
	Args        []string
	Mode        ServerMode
	Extensions  []string
	RootMarkers []string
	InstallHint string
}

var defaultRegistry = []ServerConfig{
	{
		Language:    "go",
		Binary:      "gopls",
		Mode:        ModeCLI,
		Extensions:  []string{".go"},
		RootMarkers: []string{"go.mod", "go.sum"},
		InstallHint: "go install golang.org/x/tools/gopls@latest",
	},
	{
		Language:    "python",
		Binary:      "pylsp",
		Args:        []string{},
		Mode:        ModeJSONRPC,
		Extensions:  []string{".py"},
		RootMarkers: []string{"pyproject.toml", "setup.py", "requirements.txt"},
		InstallHint: "pip install python-lsp-server",
	},
	{
		Language:    "typescript",
		Binary:      "typescript-language-server",
		Args:        []string{"--stdio"},
		Mode:        ModeJSONRPC,
		Extensions:  []string{".ts", ".tsx", ".js", ".jsx"},
		RootMarkers: []string{"package.json", "tsconfig.json"},
		InstallHint: "npm install -g typescript-language-server",
	},
	{
		Language:    "rust",
		Binary:      "rust-analyzer",
		Args:        []string{},
		Mode:        ModeJSONRPC,
		Extensions:  []string{".rs"},
		RootMarkers: []string{"Cargo.toml"},
		InstallHint: "rustup component add rust-analyzer",
	},
	{
		Language:    "java",
		Binary:      "jdtls",
		Args:        []string{},
		Mode:        ModeJSONRPC,
		Extensions:  []string{".java"},
		RootMarkers: []string{"pom.xml", "build.gradle"},
		InstallHint: "Install Eclipse JDT Language Server",
	},
}

// detectLanguage returns the language ID for a file based on extension.
func detectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	for _, cfg := range defaultRegistry {
		for _, e := range cfg.Extensions {
			if e == ext {
				return cfg.Language
			}
		}
	}
	return ""
}

// getServerConfig returns the server configuration for a language.
func getServerConfig(lang string) *ServerConfig {
	for i := range defaultRegistry {
		if defaultRegistry[i].Language == lang {
			return &defaultRegistry[i]
		}
	}
	return nil
}

// checkBinary checks if a binary exists in PATH.
func checkBinary(name string) error {
	_, err := os.Stat(name)
	return err
}
