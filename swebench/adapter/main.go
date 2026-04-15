// Package swebench provides an adapter to run goagent on SWE-bench tasks.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
	"github.com/alexioschen/cc-connect/goagent/provider/openai"
	"github.com/alexioschen/cc-connect/goagent/tool"
)

// Instance represents a SWE-bench task instance.
type Instance struct {
	InstanceID       string `json:"instance_id"`
	Repo             string `json:"repo"`
	BaseCommit       string `json:"base_commit"`
	ProblemStatement string `json:"problem_statement"`
	HintsText        string `json:"hints_text"`
	PatchText        string `json:"patch"` // ground truth (not used during inference)
}

// Prediction is the output format required by SWE-bench.
type Prediction struct {
	InstanceID      string `json:"instance_id"`
	ModelNameOrPath string `json:"model_name_or_path"`
	ModelPatch      string `json:"model_patch"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <instance_json_file>\n", os.Args[0])
		os.Exit(1)
	}

	instanceFile := os.Args[1]
	data, err := os.ReadFile(instanceFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading instance file: %v\n", err)
		os.Exit(1)
	}

	var instance Instance
	if err := json.Unmarshal(data, &instance); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing instance JSON: %v\n", err)
		os.Exit(1)
	}

	// Run the agent
	patch, err := runAgent(instance)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		os.Exit(1)
	}

	// Output prediction
	pred := Prediction{
		InstanceID:      instance.InstanceID,
		ModelNameOrPath: "goagent",
		ModelPatch:      patch,
	}

	output, _ := json.Marshal(pred)
	fmt.Println(string(output))
}

func runAgent(instance Instance) (string, error) {
	// Setup workspace
	workDir := filepath.Join("/tmp", "swebench", instance.InstanceID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create workspace: %w", err)
	}

	// Clone repository and checkout base commit
	repoURL := fmt.Sprintf("https://github.com/%s.git", instance.Repo)

	// Check if already cloned
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		fmt.Fprintf(os.Stderr, "Cloning %s...\n", instance.Repo)
		cloneCmd := exec.Command("git", "clone", repoURL, workDir)
		cloneCmd.Stderr = os.Stderr
		if err := cloneCmd.Run(); err != nil {
			return "", fmt.Errorf("failed to clone repo: %w", err)
		}
	}

	// Checkout base commit
	fmt.Fprintf(os.Stderr, "Checking out %s...\n", instance.BaseCommit[:12])
	checkoutCmd := exec.Command("git", "-C", workDir, "checkout", instance.BaseCommit)
	if err := checkoutCmd.Run(); err != nil {
		// Try fetching the commit first
		fetchCmd := exec.Command("git", "-C", workDir, "fetch", "origin", instance.BaseCommit)
		fetchCmd.Run()
		checkoutCmd = exec.Command("git", "-C", workDir, "checkout", instance.BaseCommit)
		if err := checkoutCmd.Run(); err != nil {
			return "", fmt.Errorf("failed to checkout base commit: %w", err)
		}
	}

	// Reset any previous changes
	exec.Command("git", "-C", workDir, "checkout", ".").Run()

	// Construct prompt
	prompt := fmt.Sprintf(`You are a software engineer tasked with fixing a GitHub issue.

Repository: %s
Base Commit: %s

Issue Description:
%s

%s

Your task:
1. Analyze the issue and understand what needs to be fixed
2. Locate the relevant files in the repository
3. Make the necessary code changes to fix the issue
4. Ensure your changes are minimal and focused

The repository is checked out at: %s

IMPORTANT:
- Do NOT try to run tests or execute Python code - the environment is not set up for that
- Focus ONLY on fixing the specific issue described - do not add extra features or improvements
- Use grep to search, read_file to examine code (supports line ranges: start_line, end_line)
- Use edit_file to make targeted changes (preferred over write_file for existing files)
- edit_file replaces a specific string in a file - provide exact old_string and new_string
- When you've made the fix, respond with text explaining what you did - this signals completion
- Be efficient: use parallel tool calls when possible, read only necessary lines

Workflow suggestion:
1. grep to find relevant files and line numbers
2. read_file with line ranges to examine specific sections
3. edit_file to make the fix
4. Explain what you did (this ends the task)

Use the available tools to read files, search code, and make edits. When you're done, I will generate a patch from your changes.`,
		instance.Repo,
		instance.BaseCommit,
		instance.ProblemStatement,
		formatHints(instance.HintsText),
		workDir,
	)

	// Create agent with tools
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "xop3qwen32b"
	}

	var providerOpts []openai.Option
	if baseURL != "" {
		providerOpts = append(providerOpts, openai.WithBaseURL(baseURL))
	}

	provider := openai.New(apiKey, providerOpts...)

	// Setup logging
	logDir := filepath.Join("/tmp", "swebench", "logs")
	os.MkdirAll(logDir, 0755)
	logFile := filepath.Join(logDir, instance.InstanceID+".log")
	logF, err := os.Create(logFile)
	if err != nil {
		return "", fmt.Errorf("failed to create log file: %w", err)
	}
	defer logF.Close()

	log := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		fmt.Fprintf(os.Stderr, "%s\n", msg)
		fmt.Fprintf(logF, "[%s] %s\n", time.Now().Format("15:04:05"), msg)
	}

	turnCount := 0

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel(model),
		cc.WithMaxTokens(102400),
		cc.WithTokenAwareCompressMemory(200000, 10),
		cc.WithTools(
			tool.Shell(),
			tool.ReadFile(),
			tool.WriteFile(),
			tool.EditFile(),
			tool.ListFiles(),
			tool.Grep(),
		),
		cc.WithMaxTurns(25),
		cc.WithMaxExplorationTurns(8),
		cc.WithHooks(cc.Hooks{
			BeforeToolCall: func(_ context.Context, name string, input json.RawMessage) error {
				// Truncate long inputs for logging
				inputStr := string(input)
				if len(inputStr) > 200 {
					inputStr = inputStr[:200] + "..."
				}
				log("  [TOOL] %s → %s", name, inputStr)
				return nil
			},
			AfterToolCall: func(_ context.Context, name string, output string, err error) {
				if err != nil {
					log("  [TOOL] %s ← ERROR: %s", name, err)
				} else {
					outStr := output
					if len(outStr) > 200 {
						outStr = outStr[:200] + "..."
					}
					log("  [TOOL] %s ← %s", name, outStr)
				}
			},
			OnModelResponse: func(_ context.Context, resp *cc.ChatResponse) {
				turnCount++
				text := resp.Text()
				if len(text) > 300 {
					text = text[:300] + "..."
				}
				toolUses := resp.ToolUses()
				if len(toolUses) > 0 {
					names := make([]string, len(toolUses))
					for i, tu := range toolUses {
						names[i] = tu.Name
					}
					log("[Turn %d] LLM → tool_use: [%s]", turnCount, strings.Join(names, ", "))
				} else {
					log("[Turn %d] LLM → text: %s", turnCount, text)
				}
			},
		}),
	)

	log("=== SWE-bench Run: %s ===", instance.InstanceID)
	log("Repo: %s | Commit: %s", instance.Repo, instance.BaseCommit[:12])
	log("Problem: %s", truncateStr(instance.ProblemStatement, 200))

	// Run agent
	ctx := context.Background()
	result, err := agent.Run(ctx, prompt)
	if err != nil && err.Error() != "agent: max turns exceeded" {
		log("Agent error: %v", err)
		return "", fmt.Errorf("agent execution failed: %w", err)
	}

	log("=== Agent finished: %d turns ===", result.Turns)
	log("Log saved to: %s", logFile)

	// Generate diff
	diffCmd := exec.Command("git", "-C", workDir, "diff", "HEAD")
	diffOutput, err := diffCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to generate diff: %w", err)
	}

	patch := string(diffOutput)
	if patch == "" {
		return "", fmt.Errorf("no changes made by agent")
	}

	return patch, nil
}

func formatHints(hints string) string {
	if hints == "" {
		return ""
	}
	return fmt.Sprintf("\nHints:\n%s", hints)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
