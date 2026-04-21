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
	prompt := fmt.Sprintf(`You are a software engineer fixing a GitHub issue.

Repository: %s | Base Commit: %s | Checkout: %s

Issue:
%s

%s

WORKFLOW (explore → edit → done):
1. grep to find relevant files/line numbers
2. read_file with line ranges (start_line, end_line) to examine code
3. edit_file to make targeted changes (provide exact old_string and new_string)
4. Respond with text explaining your fix (signals completion)

CONSTRAINTS:
- Do NOT run tests or execute code - environment not configured
- Focus ONLY on the specific issue - no extra features
- Read each file max 2 times - after 3-5 files, start editing
- edit_file returns success - do NOT re-read to verify
- Use parallel tool calls when possible
- Respond with text (no tools) when done

TOOLS:
- grep: search codebase
- read_file: examine code (supports line ranges)
- edit_file: replace exact old_string with new_string (preferred over write_file)

PATTERN: explore (5 turns) → edit (1-2 turns) → done (1 turn)`,
		instance.Repo,
		instance.BaseCommit,
		workDir,
		instance.ProblemStatement,
		formatHints(instance.HintsText),
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

	// Parse turn delay from environment
	turnDelay := 15 * time.Second // default 15s for xf-yun rate limits
	if d := os.Getenv("TURN_DELAY"); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil {
			turnDelay = parsed
		}
	}

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel(model),
		cc.WithMaxTokens(102400),
		cc.WithTurnDelay(turnDelay),
		cc.WithTokenAwareCompressMemory(20000, 3),
		cc.WithToolOutputMaxSize(8000),
		cc.WithToolResultSummary(800),
		cc.WithSessionFactCache(20),
		cc.WithExplorationBudget(15),
		cc.WithTools(
			tool.Shell(),
			tool.ReadFile(),
			tool.WriteFile(),
			tool.EditFile(),
			tool.ListFiles(),
			tool.Grep(),
		),
		cc.WithMaxTurns(25),
		cc.WithMaxExplorationTurns(0),
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

	// Run verification if verify script exists for this instance
	execPath, _ := os.Executable()
	verifyScript := filepath.Join(filepath.Dir(execPath), "..", "verify_patch.sh")
	if _, err := os.Stat(verifyScript); err == nil {
		log("=== Running patch verification ===")
		patchFile := filepath.Join("/tmp", "swebench_patch_"+instance.InstanceID+".diff")
		os.WriteFile(patchFile, diffOutput, 0644)

		verifyCmd := exec.Command("bash", verifyScript, patchFile)
		verifyCmd.Dir = workDir
		verifyOutput, verifyErr := verifyCmd.CombinedOutput()
		log("Verify output:\n%s", string(verifyOutput))
		if verifyErr != nil {
			log("WARNING: Patch verification FAILED: %v", verifyErr)
		} else {
			log("Patch verification PASSED")
		}
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
