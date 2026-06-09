// Package swebench provides an adapter to run goagent on SWE-bench tasks.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
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

type caseMetrics struct {
	InstanceID              string `json:"instance_id"`
	Model                   string `json:"model"`
	Toolset                 string `json:"toolset"`
	RunResult               string `json:"run_result"`
	StopReason              string `json:"stop_reason"`
	Turns                   int    `json:"turns"`
	InputTokens             int    `json:"input_tokens"`
	OutputTokens            int    `json:"output_tokens"`
	ToolCalls               int    `json:"tool_calls"`
	GrepCalls               int    `json:"grep_calls"`
	ReadFileCalls           int    `json:"read_file_calls"`
	EditFileCalls           int    `json:"edit_file_calls"`
	ReadOnlyAfterEditBlocks int    `json:"read_only_after_edit_blocks"`
	GuardTriggers           int    `json:"guard_triggers"`
	FirstEditTurn           *int   `json:"first_edit_turn"`
	PatchBytes              int    `json:"patch_bytes"`
	PatchLines              int    `json:"patch_lines"`
	VerifyStatus            string `json:"verify_status"`
	VerifyErrorMessage      string `json:"verify_error_message,omitempty"`
	DurationMS              int64  `json:"duration_ms"`
}

type jsonlEventSink struct {
	mu         sync.Mutex
	instanceID string
	enc        *json.Encoder
}

func (s *jsonlEventSink) EmitAgentEvent(_ context.Context, event cc.AgentEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.InstanceID == "" {
		event.InstanceID = s.instanceID
	}
	_ = s.enc.Encode(event)
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
	startedAt := time.Now()
	workspaceRoot, err := defaultWorkspaceRoot()
	if err != nil {
		return "", err
	}
	workDir, err := prepareWorkspace(instance, workspaceRoot)
	if err != nil {
		return "", err
	}

	// Run tools from the checked-out repository so the model can use short relative paths.
	origDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get cwd: %w", err)
	}
	if err := os.Chdir(workDir); err != nil {
		return "", fmt.Errorf("chdir workspace: %w", err)
	}
	defer os.Chdir(origDir)

	toolset := getEnv("SWE_TOOLSET", "lean")
	prompt, err := buildPrompt(instance, toolset)
	if err != nil {
		return "", err
	}

	// Create agent with tools
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "xopkimik25"
	}

	var providerOpts []openai.Option
	if baseURL != "" {
		providerOpts = append(providerOpts, openai.WithBaseURL(baseURL))
	}

	provider := openai.New(apiKey, providerOpts...)

	// Setup logging
	logDir := filepath.Join(workspaceRoot, "logs")
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
	runID := getEnv("SWE_RUN_ID", fmt.Sprintf("%s_%s", time.Now().Format("20060102_150405"), safePathName(instance.InstanceID)))
	runOutputDir := getEnv("SWE_RUN_DIR", filepath.Join(workspaceRoot, "runs", runID))
	if err := os.MkdirAll(runOutputDir, 0755); err != nil {
		return "", fmt.Errorf("create run output dir: %w", err)
	}
	eventsFile, err := os.Create(filepath.Join(runOutputDir, "events.jsonl"))
	if err != nil {
		return "", fmt.Errorf("create events file: %w", err)
	}
	defer eventsFile.Close()
	eventSink := &jsonlEventSink{instanceID: instance.InstanceID, enc: json.NewEncoder(eventsFile)}

	turnDelay := envDuration("TURN_DELAY", 15*time.Second) // default for xf-yun rate limits
	maxTokens := envInt("SWE_MAX_TOKENS", 4096)
	contextWindow := envInt("SWE_CONTEXT_WINDOW", 12000)
	recentWindow := envInt("SWE_RECENT_WINDOW", 4)
	toolOutputMax := envInt("SWE_TOOL_OUTPUT_CHARS", 5000)
	toolSummaryMax := envInt("SWE_TOOL_SUMMARY_CHARS", 2400)
	explorationBudget := envInt("SWE_EXPLORATION_BUDGET", 12)
	maxReadOnlyTurns := envInt("SWE_MAX_READ_ONLY_TURNS", 10)
	maxTurns := envInt("SWE_MAX_TURNS", 18)
	editLocked := false

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel(model),
		cc.WithMaxTokens(maxTokens),
		cc.WithTurnDelay(turnDelay),
		cc.WithTokenAwareCompressMemory(contextWindow, recentWindow),
		cc.WithToolOutputMaxSize(toolOutputMax),
		cc.WithToolResultSummary(toolSummaryMax),
		cc.WithSessionFactCache(20),
		cc.WithExplorationBudget(explorationBudget),
		cc.WithTools(sweTools(toolset)...),
		cc.WithStrictSandbox(workDir),
		cc.WithRunID(runID),
		cc.WithEventSink(eventSink),
		cc.WithConvergenceGuard(sweConvergenceGuard(maxReadOnlyTurns)),
		cc.WithMaxTurns(maxTurns),
		cc.WithMaxExplorationTurns(0),
		cc.WithHooks(cc.Hooks{
			BeforeToolCall: func(_ context.Context, name string, input json.RawMessage) error {
				if editLocked && isReadOnlySweTool(name) {
					return fmt.Errorf("a successful edit_file has already completed; stop reading and respond with final text")
				}

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
					if name == "edit_file" {
						editLocked = true
					}
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
	log("Config: toolset=%s max_turns=%d max_tokens=%d context=%d recent=%d output=%d summary=%d budget=%d max_read_only=%d delay=%s",
		toolset, maxTurns, maxTokens, contextWindow, recentWindow, toolOutputMax, toolSummaryMax, explorationBudget, maxReadOnlyTurns, turnDelay)
	log("Run artifacts: %s", runOutputDir)
	log("Problem: %s", truncateStr(instance.ProblemStatement, 200))

	// Run agent
	ctx := context.Background()
	result, err := agent.Run(ctx, prompt)
	if err != nil && err.Error() != "agent: max turns exceeded" {
		log("Agent error: %v", err)
		writeCaseArtifacts(runOutputDir, buildMetrics(instance, model, toolset, result, "", "skipped", err.Error(), startedAt))
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
		writeCaseArtifacts(runOutputDir, buildMetrics(instance, model, toolset, result, patch, "skipped", "", startedAt))
		return "", fmt.Errorf("no changes made by agent")
	}

	// Run verification if verify script exists for this instance
	verifyStatus := "skipped"
	verifyError := ""
	execPath, _ := os.Executable()
	if verifyScript, ok := verificationScriptForInstance(instance.InstanceID, execPath); ok {
		log("=== Running patch verification ===")
		patchDir := filepath.Join(workspaceRoot, "patches")
		if err := os.MkdirAll(patchDir, 0755); err != nil {
			return "", fmt.Errorf("create patch dir: %w", err)
		}
		patchFile := filepath.Join(patchDir, instance.InstanceID+".diff")
		os.WriteFile(patchFile, diffOutput, 0644)

		verifyCmd := exec.Command("bash", verifyScript, patchFile)
		verifyCmd.Dir = workDir
		verifyCmd.Env = append(os.Environ(), "SWE_REPO_PATH="+workDir, "SWE_INSTANCE_ID="+instance.InstanceID)
		verifyOutput, verifyErr := verifyCmd.CombinedOutput()
		log("Verify output:\n%s", string(verifyOutput))
		if verifyErr != nil {
			verifyStatus = "failed"
			verifyError = verifyErr.Error()
			log("WARNING: Patch verification FAILED: %v", verifyErr)
		} else {
			verifyStatus = "passed"
			log("Patch verification PASSED")
		}
	} else {
		verifyError = "no verifier configured for instance"
		log("Patch verification SKIPPED: %s", verifyError)
	}
	writeCaseArtifacts(runOutputDir, buildMetrics(instance, model, toolset, result, patch, verifyStatus, verifyError, startedAt))

	return patch, nil
}

func verificationScriptForInstance(instanceID, execPath string) (string, bool) {
	switch instanceID {
	case "sympy__sympy-11400", "django__django-11179", "pytest-dev__pytest-11143":
	default:
		return "", false
	}
	verifyScript := filepath.Join(filepath.Dir(execPath), "..", "verify_patch.sh")
	if _, err := os.Stat(verifyScript); err != nil {
		return "", false
	}
	return verifyScript, true
}

func formatHints(hints string) string {
	if hints == "" {
		return ""
	}
	return fmt.Sprintf("\nHints:\n%s", hints)
}

func buildMetrics(instance Instance, model, toolset string, result *cc.RunResult, patch, verifyStatus, verifyError string, startedAt time.Time) caseMetrics {
	metrics := caseMetrics{
		InstanceID:         instance.InstanceID,
		Model:              model,
		Toolset:            toolset,
		RunResult:          "case_failed",
		VerifyStatus:       verifyStatus,
		VerifyErrorMessage: verifyError,
		PatchBytes:         len(patch),
		PatchLines:         countLines(patch),
		DurationMS:         time.Since(startedAt).Milliseconds(),
	}
	if result != nil {
		metrics.StopReason = result.StopReason
		metrics.Turns = result.Turns
		metrics.InputTokens = result.Usage.InputTokens
		metrics.OutputTokens = result.Usage.OutputTokens
		metrics.ToolCalls = result.Metrics.ToolCallCount
		metrics.GrepCalls = result.Metrics.GrepCalls
		metrics.ReadFileCalls = result.Metrics.ReadFileCalls
		metrics.EditFileCalls = result.Metrics.EditFileCalls
		metrics.ReadOnlyAfterEditBlocks = result.Metrics.ReadOnlyAfterEditBlocks
		metrics.GuardTriggers = result.Metrics.GuardTriggers
		if result.Metrics.FirstEditTurn > 0 {
			firstEditTurn := result.Metrics.FirstEditTurn
			metrics.FirstEditTurn = &firstEditTurn
		}
		if result.StopReason == cc.StopReasonStoppedByGuard {
			metrics.RunResult = "stopped_by_guard"
		}
	}
	if patch != "" && verifyStatus == "passed" {
		metrics.RunResult = "case_success"
	}
	if verifyStatus == "failed" {
		metrics.RunResult = "case_failed"
	}
	return metrics
}

func writeCaseArtifacts(runOutputDir string, metrics caseMetrics) {
	data, _ := json.MarshalIndent(metrics, "", "  ")
	_ = os.WriteFile(filepath.Join(runOutputDir, "metrics.json"), append(data, '\n'), 0644)
	_ = os.WriteFile(filepath.Join(runOutputDir, "summary.md"), []byte(renderSummary(metrics)), 0644)
}

func renderSummary(metrics caseMetrics) string {
	firstEdit := "null"
	if metrics.FirstEditTurn != nil {
		firstEdit = strconv.Itoa(*metrics.FirstEditTurn)
	}
	return fmt.Sprintf(`# SWE-bench Case Summary

- Instance: %s
- Run result: %s
- Stop reason: %s
- Verify status: %s
- Turns: %d
- Tool calls: %d
- grep/read_file/edit_file: %d/%d/%d
- First edit turn: %s
- Patch: %d bytes, %d lines
- Tokens: input=%d output=%d
- Duration: %d ms
`,
		metrics.InstanceID,
		metrics.RunResult,
		metrics.StopReason,
		metrics.VerifyStatus,
		metrics.Turns,
		metrics.ToolCalls,
		metrics.GrepCalls,
		metrics.ReadFileCalls,
		metrics.EditFileCalls,
		firstEdit,
		metrics.PatchBytes,
		metrics.PatchLines,
		metrics.InputTokens,
		metrics.OutputTokens,
		metrics.DurationMS,
	)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

func defaultWorkspaceRoot() (string, error) {
	root := os.Getenv("SWE_WORKSPACE_ROOT")
	if root == "" {
		root = filepath.Join("..", "workspace")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return "", fmt.Errorf("create workspace root %s: %w", abs, err)
	}
	return abs, nil
}

func prepareWorkspace(instance Instance, workspaceRoot string) (string, error) {
	caseDir := filepath.Join(workspaceRoot, safePathName(instance.InstanceID))
	repoURL := fmt.Sprintf("https://github.com/%s.git", instance.Repo)

	if _, err := os.Stat(filepath.Join(caseDir, ".git")); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect workspace git dir: %w", err)
		}
		if err := os.RemoveAll(caseDir); err != nil {
			return "", fmt.Errorf("remove incomplete workspace %s: %w", caseDir, err)
		}
		fmt.Fprintf(os.Stderr, "Cloning %s into %s...\n", instance.Repo, caseDir)
		if err := runGit("", "clone", repoURL, caseDir); err != nil {
			return "", fmt.Errorf("failed to clone repo: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "Preparing %s at %s...\n", instance.BaseCommit[:12], caseDir)
	if err := runGit(caseDir, "checkout", instance.BaseCommit); err != nil {
		_ = runGit(caseDir, "fetch", "origin", instance.BaseCommit)
		if err := runGit(caseDir, "checkout", instance.BaseCommit); err != nil {
			return "", fmt.Errorf("failed to checkout base commit: %w", err)
		}
	}
	if err := runGit(caseDir, "reset", "--hard", instance.BaseCommit); err != nil {
		return "", fmt.Errorf("failed to reset workspace: %w", err)
	}
	if err := runGit(caseDir, "clean", "-fd"); err != nil {
		return "", fmt.Errorf("failed to clean workspace: %w", err)
	}
	return caseDir, nil
}

func runGit(dir string, args ...string) error {
	cmdArgs := args
	if dir != "" {
		cmdArgs = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", cmdArgs...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func safePathName(s string) string {
	replacer := strings.NewReplacer("/", "__", "\\", "__", ":", "_", "..", "_")
	return replacer.Replace(s)
}

func buildPrompt(instance Instance, toolset string) (string, error) {
	root, err := defaultTemplatesRoot()
	if err != nil {
		return "", err
	}
	registry := cc.NewPromptTemplateRegistry(root)
	names := []string{
		"swebench/case.md",
		"core/tool_calling.md",
	}
	names = append(names, toolTemplateNames(toolset)...)
	names = append(names, repoTemplateNames(instance.Repo)...)
	names = append(names, "swebench/rules.md")

	return registry.RenderMany(names, map[string]string{
		"repo":              instance.Repo,
		"base_commit":       instance.BaseCommit,
		"problem_statement": strings.TrimSpace(instance.ProblemStatement),
		"hints_block":       formatHints(instance.HintsText),
	})
}

func repoTemplateNames(repo string) []string {
	switch strings.ToLower(strings.TrimSpace(repo)) {
	case "pytest-dev/pytest":
		return []string{"swebench/repos/pytest.md"}
	case "django/django":
		return []string{"swebench/repos/django.md"}
	default:
		return nil
	}
}

func defaultTemplatesRoot() (string, error) {
	for _, key := range []string{"SWE_TEMPLATES_DIR", "GOAGENT_TEMPLATES_DIR"} {
		if root := os.Getenv(key); root != "" {
			abs, err := filepath.Abs(root)
			if err != nil {
				return "", fmt.Errorf("resolve %s: %w", key, err)
			}
			return abs, nil
		}
	}

	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "..", "..", "templates"))
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "..", "..", "templates"))
	}
	candidates = append(candidates, "templates", filepath.Join("..", "..", "templates"))

	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			return abs, nil
		}
	}
	return "", fmt.Errorf("templates directory not found; set SWE_TEMPLATES_DIR")
}

func toolTemplateNames(toolset string) []string {
	switch strings.ToLower(toolset) {
	case "full":
		return []string{
			"tools/read_file.md",
			"tools/write_file.md",
			"tools/edit_file.md",
			"tools/list_files.md",
			"tools/grep.md",
		}
	default:
		return []string{
			"tools/grep.md",
			"tools/read_file.md",
			"tools/edit_file.md",
		}
	}
}

func sweTools(toolset string) []cc.Tool {
	switch strings.ToLower(toolset) {
	case "full":
		return []cc.Tool{
			tool.ReadFile(),
			tool.WriteFile(),
			tool.EditFile(),
			tool.ListFiles(),
			tool.Grep(),
		}
	default:
		return []cc.Tool{
			tool.Grep(),
			tool.ReadFile(),
			tool.EditFile(),
		}
	}
}

func sweConvergenceGuard(maxReadOnlyTurns int) cc.ConvergenceGuardConfig {
	cfg := cc.DefaultConvergenceGuardConfig()
	cfg.MaxConsecutiveReadOnlyTurns = maxReadOnlyTurns
	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err == nil {
		return value
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func isReadOnlySweTool(name string) bool {
	switch name {
	case "grep", "read_file", "list_files":
		return true
	default:
		return false
	}
}
