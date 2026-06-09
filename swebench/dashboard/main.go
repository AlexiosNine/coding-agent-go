// Package main serves a local HTML dashboard for SWE-bench agent runs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
	"github.com/alexioschen/cc-connect/goagent/provider/anthropic"
	"github.com/alexioschen/cc-connect/goagent/provider/openai"
	"github.com/alexioschen/cc-connect/goagent/tool"
)

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

type runStatus struct {
	Dir          string      `json:"dir"`
	Name         string      `json:"name"`
	InstanceID   string      `json:"instance_id"`
	State        string      `json:"state"`
	UpdatedAt    time.Time   `json:"updated_at"`
	UpdatedAgo   string      `json:"updated_ago"`
	HasMetrics   bool        `json:"has_metrics"`
	HasEvents    bool        `json:"has_events"`
	HasPatch     bool        `json:"has_patch"`
	EventCount   int         `json:"event_count"`
	ArtifactList []string    `json:"artifacts"`
	Metrics      caseMetrics `json:"metrics"`
}

type server struct {
	roots       []string
	chat        chatConfig
	agentMu     sync.Mutex
	agent       *cc.Agent
	session     *cc.Session
	chatRunning bool
	chatStarted time.Time
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "HTTP listen address")
	runsRoot := flag.String("runs", "swebench/runs", "Primary SWE-bench runs directory")
	suiteRunsRoot := flag.String("suite-runs", "swebench/suite_runs", "SWE-bench suite runs directory")
	workspace := flag.String("workspace", ".", "Workspace directory the coding agent may read and write")
	providerName := flag.String("provider", "openai", "LLM provider for chat: openai or anthropic")
	model := flag.String("model", "", "Model name for chat; defaults to LLM_MODEL, AGENT_MODEL, or provider default")
	maxTurns := flag.Int("max-turns", 20, "Maximum turns for one chat task")
	localKeyExtract := flag.Bool("local-key-extract", true, "Read local e2e_test.go fallback key/base URL when env vars are absent")
	flag.Parse()

	roots, err := normalizeRoots([]string{*runsRoot, *suiteRunsRoot})
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard: %v\n", err)
		os.Exit(2)
	}
	workspaceAbs, err := filepath.Abs(*workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dashboard workspace: %v\n", err)
		os.Exit(2)
	}

	srv := &server{
		roots: roots,
		chat: chatConfig{
			Workspace:       workspaceAbs,
			Provider:        strings.TrimSpace(*providerName),
			Model:           firstNonEmpty(*model, os.Getenv("LLM_MODEL"), os.Getenv("AGENT_MODEL")),
			MaxTurns:        *maxTurns,
			LocalKeyExtract: *localKeyExtract,
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/eval", srv.handleEval)
	mux.HandleFunc("/api/runs", srv.handleRuns)
	mux.HandleFunc("/api/chat", srv.handleChat)
	mux.HandleFunc("/api/chat/status", srv.handleChatStatus)
	mux.HandleFunc("/artifact", srv.handleArtifact)

	fmt.Printf("Coding agent UI: http://%s\n", *addr)
	fmt.Printf("Agent workspace: %s\n", workspaceAbs)
	fmt.Printf("Scanning roots:\n")
	for _, root := range roots {
		fmt.Printf("  %s\n", root)
	}
	if err := http.ListenAndServe(*addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "dashboard server: %v\n", err)
		os.Exit(1)
	}
}

type chatConfig struct {
	Workspace       string
	Provider        string
	Model           string
	MaxTurns        int
	LocalKeyExtract bool
}

type chatRequest struct {
	Message string `json:"message"`
}

type chatResponse struct {
	Output     string   `json:"output"`
	Error      string   `json:"error,omitempty"`
	Turns      int      `json:"turns"`
	Input      int      `json:"input_tokens"`
	OutputTok  int      `json:"output_tokens"`
	DurationMS int64    `json:"duration_ms"`
	Model      string   `json:"model"`
	Workspace  string   `json:"workspace"`
	Artifacts  []string `json:"artifacts,omitempty"`
}

func normalizeRoots(inputs []string) ([]string, error) {
	seen := map[string]bool{}
	var roots []string
	for _, input := range inputs {
		if strings.TrimSpace(input) == "" {
			continue
		}
		abs, err := filepath.Abs(input)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if !seen[abs] {
			seen[abs] = true
			roots = append(roots, abs)
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no existing run roots found")
	}
	return roots, nil
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = chatTemplate.Execute(w, map[string]any{
		"Workspace": s.chat.Workspace,
		"Provider":  s.chat.Provider,
		"Model":     s.effectiveModel(),
		"MaxTurns":  s.chat.MaxTurns,
	})
}

func (s *server) handleEval(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/eval" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = indexTemplate.Execute(w, map[string]any{
		"Roots": s.roots,
	})
}

func (s *server) handleChatStatus(w http.ResponseWriter, _ *http.Request) {
	s.agentMu.Lock()
	running := s.chatRunning
	started := s.chatStarted
	s.agentMu.Unlock()

	payload := map[string]any{
		"running":   running,
		"workspace": s.chat.Workspace,
		"provider":  s.chat.Provider,
		"model":     s.effectiveModel(),
	}
	if running {
		payload["started_at"] = started
		payload["elapsed_ms"] = time.Since(started).Milliseconds()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	session, err := s.ensureSession()
	if err != nil {
		writeChatError(w, err, s.chat)
		return
	}

	s.agentMu.Lock()
	if s.chatRunning {
		s.agentMu.Unlock()
		writeChatError(w, fmt.Errorf("another task is already running"), s.chat)
		return
	}
	s.chatRunning = true
	s.chatStarted = time.Now()
	s.agentMu.Unlock()

	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()
	result, runErr := session.Run(ctx, req.Message)

	s.agentMu.Lock()
	s.chatRunning = false
	s.agentMu.Unlock()

	resp := chatResponse{
		DurationMS: time.Since(started).Milliseconds(),
		Model:      s.effectiveModel(),
		Workspace:  s.chat.Workspace,
	}
	if result != nil {
		resp.Output = result.Output
		resp.Turns = result.Turns
		resp.Input = result.Usage.InputTokens
		resp.OutputTok = result.Usage.OutputTokens
	}
	if runErr != nil {
		resp.Error = runErr.Error()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if runErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func writeChatError(w http.ResponseWriter, err error, cfg chatConfig) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(chatResponse{
		Error:     err.Error(),
		Model:     effectiveModelFor(cfg),
		Workspace: cfg.Workspace,
	})
}

func (s *server) ensureSession() (*cc.Session, error) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	if s.session != nil {
		return s.session, nil
	}
	provider, err := buildChatProvider(s.chat)
	if err != nil {
		return nil, err
	}
	model := s.effectiveModel()
	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel(model),
		cc.WithSystem("You are a local coding agent. Work inside the configured workspace. Prefer small, verifiable edits. Use tools to inspect files before editing. When you finish, summarize changed files and verification results."),
		cc.WithMaxTurns(s.chat.MaxTurns),
		cc.WithAutoApprove(),
		cc.WithStrictSandbox(s.chat.Workspace),
		cc.WithTools(
			tool.Shell(),
			tool.ReadFile(),
			tool.WriteFile(),
			tool.EditFile(),
			tool.ListFiles(),
			tool.Grep(),
			tool.LSP(),
		),
	)
	s.agent = agent
	s.session = agent.NewSession()
	return s.session, nil
}

func buildChatProvider(cfg chatConfig) (cc.Provider, error) {
	switch cfg.Provider {
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		base := os.Getenv("OPENAI_BASE_URL")
		if cfg.LocalKeyExtract {
			key = firstNonEmpty(key, extractLocalConfig("apiKey"))
			base = firstNonEmpty(base, extractLocalConfig("baseURL"))
		}
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is not set")
		}
		var opts []openai.Option
		if base != "" {
			opts = append(opts, openai.WithBaseURL(base))
		}
		return openai.New(key, opts...), nil
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
		}
		return anthropic.New(key), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
}

func extractLocalConfig(name string) string {
	data, err := os.ReadFile("e2e_test.go")
	if err != nil {
		return ""
	}
	prefix := name + " = \""
	for _, line := range strings.Split(string(data), "\n") {
		idx := strings.Index(line, prefix)
		if idx < 0 {
			continue
		}
		rest := line[idx+len(prefix):]
		end := strings.Index(rest, "\"")
		if end >= 0 {
			return rest[:end]
		}
	}
	return ""
}

func (s *server) effectiveModel() string {
	return effectiveModelFor(s.chat)
}

func effectiveModelFor(cfg chatConfig) string {
	if cfg.Model != "" {
		return cfg.Model
	}
	switch cfg.Provider {
	case "anthropic":
		return "claude-sonnet-4-20250514"
	default:
		return "gpt-4o"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *server) handleRuns(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	runs, err := scanRuns(s.roots)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(runs) > limit {
		runs = runs[:limit]
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"runs":       runs,
		"updated_at": time.Now(),
	})
}

func (s *server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	runDir := r.URL.Query().Get("run")
	name := r.URL.Query().Get("file")
	path, err := s.safeArtifactPath(runDir, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, path)
}

func (s *server) safeArtifactPath(runDir, name string) (string, error) {
	if runDir == "" || name == "" {
		return "", fmt.Errorf("run and file are required")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid artifact name")
	}
	absRun, err := filepath.Abs(runDir)
	if err != nil {
		return "", err
	}
	if !isUnderAnyRoot(absRun, s.roots) {
		return "", fmt.Errorf("run is outside configured roots")
	}
	path := filepath.Join(absRun, name)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if filepath.Dir(absPath) != absRun {
		return "", fmt.Errorf("artifact escaped run directory")
	}
	if _, err := os.Stat(absPath); err != nil {
		return "", err
	}
	return absPath, nil
}

func scanRuns(roots []string) ([]runStatus, error) {
	runDirs := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if isRunDir(path) {
				runDirs[path] = true
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	runs := make([]runStatus, 0, len(runDirs))
	for dir := range runDirs {
		run, err := readRunStatus(dir)
		if err == nil {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].UpdatedAt.After(runs[j].UpdatedAt)
	})
	return runs, nil
}

func isRunDir(path string) bool {
	for _, marker := range []string{"metrics.json", "status.txt", "env_summary.txt", "runner.stdout.log", "adapter.log"} {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

func readRunStatus(dir string) (runStatus, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return runStatus{}, err
	}
	run := runStatus{
		Dir:       dir,
		Name:      filepath.Base(dir),
		State:     "running",
		UpdatedAt: info.ModTime(),
	}

	if data, err := os.ReadFile(filepath.Join(dir, "metrics.json")); err == nil {
		run.HasMetrics = true
		_ = json.Unmarshal(data, &run.Metrics)
		run.InstanceID = run.Metrics.InstanceID
		run.State = run.Metrics.RunResult
		if run.State == "" {
			run.State = "unknown"
		}
	}
	if run.InstanceID == "" {
		run.InstanceID = instanceFromRunName(run.Name)
	}
	if status := readKeyValue(filepath.Join(dir, "status.txt"), "run_result"); status != "" && !run.HasMetrics {
		run.State = status
	}

	artifacts := []string{
		"metrics.json",
		"events.jsonl",
		"summary.md",
		"prediction_patch.diff",
		"repo.diff",
		"adapter.log",
		"runner.stdout.log",
		"runner.stderr.log",
		"runner_status.jsonl",
		"env_summary.txt",
		"command.txt",
	}
	for _, artifact := range artifacts {
		path := filepath.Join(dir, artifact)
		if artifactInfo, err := os.Stat(path); err == nil && !artifactInfo.IsDir() {
			run.ArtifactList = append(run.ArtifactList, artifact)
			if artifactInfo.ModTime().After(run.UpdatedAt) {
				run.UpdatedAt = artifactInfo.ModTime()
			}
			switch artifact {
			case "events.jsonl":
				run.HasEvents = true
				run.EventCount = countLines(path)
			case "prediction_patch.diff", "repo.diff":
				if artifactInfo.Size() > 0 {
					run.HasPatch = true
				}
			}
		}
	}
	run.UpdatedAgo = formatAgo(time.Since(run.UpdatedAt))
	return run, nil
}

func instanceFromRunName(name string) string {
	parts := strings.SplitN(name, "_", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return name
}

func readKeyValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0
	}
	count := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		count++
	}
	return count
}

func isUnderAnyRoot(path string, roots []string) bool {
	path = filepath.Clean(path)
	for _, root := range roots {
		root = filepath.Clean(root)
		if path == root {
			return true
		}
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
			return true
		}
	}
	return false
}

func formatAgo(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

var chatTemplate = template.Must(template.New("chat").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Coding Agent</title>
  <style>
    :root {
      color-scheme: light dark;
      --bg: #0f1115;
      --panel: #171b22;
      --text: #e6edf3;
      --muted: #9aa4ad;
      --line: #30363d;
      --accent: #58a6ff;
      --ok: #3fb950;
      --bad: #f85149;
      --input: #0d1117;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      background: var(--bg);
      color: var(--text);
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 18px;
      padding: 18px 24px;
      border-bottom: 1px solid var(--line);
      background: var(--panel);
    }
    h1 { margin: 0; font-size: 20px; }
    nav { display: flex; gap: 10px; align-items: center; }
    nav a {
      color: var(--text);
      text-decoration: none;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 7px 10px;
      font-size: 13px;
    }
    nav a.active { border-color: var(--accent); color: var(--accent); }
    main {
      display: grid;
      grid-template-columns: minmax(0, 1fr) 320px;
      gap: 16px;
      padding: 16px 24px 24px;
      height: calc(100vh - 68px);
    }
    .terminal, .side {
      border: 1px solid var(--line);
      background: var(--panel);
      border-radius: 8px;
      min-height: 0;
    }
    .terminal {
      display: grid;
      grid-template-rows: minmax(0, 1fr) auto;
      overflow: hidden;
    }
    #log {
      overflow: auto;
      padding: 16px;
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 13px;
      line-height: 1.55;
      white-space: pre-wrap;
    }
    .entry { margin-bottom: 16px; }
    .role { color: var(--muted); margin-bottom: 4px; }
    .user .role { color: var(--accent); }
    .agent .role { color: var(--ok); }
    .error .role { color: var(--bad); }
    form {
      display: grid;
      grid-template-columns: minmax(0, 1fr) auto;
      gap: 10px;
      padding: 12px;
      border-top: 1px solid var(--line);
      background: var(--input);
    }
    textarea {
      resize: none;
      min-height: 58px;
      max-height: 180px;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 10px;
      background: #080b10;
      color: var(--text);
      font: inherit;
      font-size: 14px;
    }
    button {
      border: 1px solid var(--accent);
      background: var(--accent);
      color: #08111f;
      border-radius: 6px;
      padding: 0 18px;
      font-weight: 700;
      cursor: pointer;
    }
    button:disabled { opacity: .5; cursor: not-allowed; }
    .side { padding: 14px; overflow: auto; }
    .side h2 { margin: 0 0 12px; font-size: 14px; color: var(--muted); }
    .kv { display: grid; gap: 10px; }
    .kv div {
      border-bottom: 1px solid var(--line);
      padding-bottom: 8px;
      overflow-wrap: anywhere;
    }
    .label { color: var(--muted); font-size: 12px; margin-bottom: 3px; }
    .value { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
    .status {
      display: inline-flex;
      gap: 6px;
      align-items: center;
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 4px 8px;
      font-size: 12px;
    }
    .dot {
      width: 8px;
      height: 8px;
      border-radius: 999px;
      background: var(--ok);
    }
    .running .dot { background: var(--accent); }
    .hint { margin-top: 14px; color: var(--muted); font-size: 12px; line-height: 1.5; }
    @media (max-width: 900px) {
      main { grid-template-columns: 1fr; height: auto; }
      .terminal { min-height: 70vh; }
    }
  </style>
</head>
<body>
  <header>
    <div>
      <h1>Coding Agent</h1>
    </div>
    <nav>
      <a class="active" href="/">Chat</a>
      <a href="/eval">Evaluation</a>
    </nav>
  </header>
  <main>
    <section class="terminal">
      <div id="log">
        <div class="entry agent">
          <div class="role">agent</div>
          <div>Ready. Send a coding task. I will operate inside the configured workspace.</div>
        </div>
      </div>
      <form id="chat-form">
        <textarea id="message" placeholder="e.g. Summarize the repo, fix a failing test, or inspect the SWE-bench dashboard code." autofocus></textarea>
        <button id="send" type="submit">Run</button>
      </form>
    </section>
    <aside class="side">
      <h2>Runtime</h2>
      <div class="kv">
        <div><div class="label">status</div><div class="value"><span id="status" class="status"><span class="dot"></span><span>idle</span></span></div></div>
        <div><div class="label">workspace</div><div class="value">{{.Workspace}}</div></div>
        <div><div class="label">provider</div><div class="value">{{.Provider}}</div></div>
        <div><div class="label">model</div><div class="value">{{.Model}}</div></div>
        <div><div class="label">max turns</div><div class="value">{{.MaxTurns}}</div></div>
      </div>
      <div class="hint">The agent uses a strict filesystem sandbox for the workspace. Evaluation run details are available on the Evaluation page.</div>
    </aside>
  </main>
  <script>
    const log = document.getElementById("log");
    const form = document.getElementById("chat-form");
    const input = document.getElementById("message");
    const send = document.getElementById("send");
    const status = document.getElementById("status");

    function addEntry(role, text, meta) {
      const entry = document.createElement("div");
      entry.className = "entry " + role;
      const roleEl = document.createElement("div");
      roleEl.className = "role";
      roleEl.textContent = role + (meta ? " " + meta : "");
      const body = document.createElement("div");
      body.textContent = text;
      entry.appendChild(roleEl);
      entry.appendChild(body);
      log.appendChild(entry);
      log.scrollTop = log.scrollHeight;
      return entry;
    }

    function setRunning(running) {
      send.disabled = running;
      input.disabled = running;
      status.className = running ? "status running" : "status";
      status.querySelector("span:last-child").textContent = running ? "running" : "idle";
    }

    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const message = input.value.trim();
      if (!message) return;
      input.value = "";
      addEntry("user", message);
      const pending = addEntry("agent", "running...");
      setRunning(true);
      const started = Date.now();
      try {
        const res = await fetch("/api/chat", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ message })
        });
        const payload = await res.json();
        pending.remove();
        if (payload.error) {
          addEntry("error", payload.error);
        }
        if (payload.output) {
          const meta = "[" + (payload.turns || 0) + " turns, " + (payload.input_tokens || 0) + " in / " + (payload.output_tokens || 0) + " out, " + Math.round((payload.duration_ms || (Date.now() - started)) / 1000) + "s]";
          addEntry("agent", payload.output, meta);
        }
      } catch (err) {
        pending.remove();
        addEntry("error", String(err));
      } finally {
        setRunning(false);
        input.focus();
      }
    });

    input.addEventListener("keydown", (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
        form.requestSubmit();
      }
    });
  </script>
</body>
</html>`))

var indexTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SWE-bench Agent Runs</title>
  <style>
    :root {
      color-scheme: light dark;
      --bg: #f7f7f4;
      --panel: #ffffff;
      --text: #1f2328;
      --muted: #687076;
      --line: #d8d8d2;
      --ok: #116329;
      --bad: #b42318;
      --warn: #9a6700;
      --run: #0969da;
      --chip: #eef1f4;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #111315;
        --panel: #181b1f;
        --text: #e6edf3;
        --muted: #9aa4ad;
        --line: #30363d;
        --chip: #24292f;
      }
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    header {
      display: flex;
      justify-content: space-between;
      align-items: flex-end;
      gap: 24px;
      padding: 24px 28px 16px;
      border-bottom: 1px solid var(--line);
      background: var(--panel);
    }
    h1 { margin: 0; font-size: 22px; line-height: 1.2; }
    .sub { margin-top: 6px; color: var(--muted); font-size: 13px; }
    .controls { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
    input, select, button {
      border: 1px solid var(--line);
      background: var(--panel);
      color: var(--text);
      border-radius: 6px;
      padding: 8px 10px;
      font: inherit;
      font-size: 13px;
    }
    button { cursor: pointer; }
    main { padding: 18px 28px 28px; }
    .summary {
      display: grid;
      grid-template-columns: repeat(4, minmax(140px, 1fr));
      gap: 12px;
      margin-bottom: 16px;
    }
    .metric {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      padding: 12px;
    }
    .metric .label { color: var(--muted); font-size: 12px; }
    .metric .value { font-size: 24px; margin-top: 4px; font-weight: 650; }
    table {
      width: 100%;
      border-collapse: collapse;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      overflow: hidden;
    }
    th, td {
      text-align: left;
      padding: 10px 12px;
      border-bottom: 1px solid var(--line);
      font-size: 13px;
      vertical-align: top;
    }
    th {
      position: sticky;
      top: 0;
      background: var(--panel);
      color: var(--muted);
      font-weight: 600;
      z-index: 1;
    }
    tr:last-child td { border-bottom: 0; }
    .mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
    .state {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      border-radius: 999px;
      padding: 3px 8px;
      background: var(--chip);
      font-weight: 600;
      white-space: nowrap;
    }
    .state.case_success { color: var(--ok); }
    .state.case_failed, .state.command_failed { color: var(--bad); }
    .state.stopped_by_guard { color: var(--warn); }
    .state.running { color: var(--run); }
    .links { display: flex; gap: 7px; flex-wrap: wrap; max-width: 460px; }
    a { color: #0969da; text-decoration: none; }
    a:hover { text-decoration: underline; }
    .muted { color: var(--muted); }
    .empty { padding: 30px; text-align: center; color: var(--muted); }
    @media (max-width: 900px) {
      header { display: block; }
      .controls { margin-top: 14px; }
      .summary { grid-template-columns: repeat(2, 1fr); }
      table, thead, tbody, th, td, tr { display: block; }
      thead { display: none; }
      tr { border-bottom: 1px solid var(--line); }
      td { border-bottom: 0; padding: 7px 12px; }
      td::before { content: attr(data-label); display: block; color: var(--muted); font-size: 11px; margin-bottom: 2px; }
    }
  </style>
</head>
<body>
  <header>
    <div>
      <h1>SWE-bench Agent Runs</h1>
      <div class="sub">Live local view over metrics, events, patches, and logs.</div>
    </div>
    <div class="controls">
      <a href="/">Chat</a>
      <input id="filter" placeholder="Filter instance or state" autocomplete="off">
      <select id="limit">
        <option value="50">50 runs</option>
        <option value="100" selected>100 runs</option>
        <option value="200">200 runs</option>
      </select>
      <button id="refresh">Refresh</button>
      <span class="muted" id="updated"></span>
    </div>
  </header>
  <main>
    <section class="summary">
      <div class="metric"><div class="label">Visible runs</div><div class="value" id="total">0</div></div>
      <div class="metric"><div class="label">Success</div><div class="value" id="success">0</div></div>
      <div class="metric"><div class="label">Failed</div><div class="value" id="failed">0</div></div>
      <div class="metric"><div class="label">Guard stopped</div><div class="value" id="guard">0</div></div>
    </section>
    <table>
      <thead>
        <tr>
          <th>Run</th>
          <th>Status</th>
          <th>Trajectory</th>
          <th>Patch</th>
          <th>Updated</th>
          <th>Artifacts</th>
        </tr>
      </thead>
      <tbody id="runs">
        <tr><td colspan="6" class="empty">Loading...</td></tr>
      </tbody>
    </table>
  </main>
  <script>
    let allRuns = [];

    function stateClass(state) {
      return (state || "unknown").replace(/[^a-zA-Z0-9_-]/g, "_");
    }

    function artifactLink(run, file) {
      const params = new URLSearchParams({ run: run.dir, file });
      return '<a href="/artifact?' + params.toString() + '" target="_blank">' + file + '</a>';
    }

    function render() {
      const q = document.getElementById("filter").value.toLowerCase().trim();
      const runs = allRuns.filter(run => {
        if (!q) return true;
        return [run.name, run.instance_id, run.state, run.metrics.verify_status, run.metrics.stop_reason]
          .filter(Boolean)
          .some(value => String(value).toLowerCase().includes(q));
      });

      document.getElementById("total").textContent = runs.length;
      document.getElementById("success").textContent = runs.filter(r => r.state === "case_success").length;
      document.getElementById("failed").textContent = runs.filter(r => r.state === "case_failed" || r.state === "command_failed").length;
      document.getElementById("guard").textContent = runs.filter(r => r.state === "stopped_by_guard" || r.metrics.stop_reason === "stopped_by_guard").length;

      const body = document.getElementById("runs");
      if (runs.length === 0) {
        body.innerHTML = '<tr><td colspan="6" class="empty">No runs match the filter.</td></tr>';
        return;
      }
      body.innerHTML = runs.map(run => {
        const m = run.metrics || {};
        const links = (run.artifacts || []).map(file => artifactLink(run, file)).join("");
        const firstEdit = m.first_edit_turn == null ? "-" : m.first_edit_turn;
        return '<tr>' +
          '<td data-label="Run">' +
            '<div class="mono">' + (run.instance_id || run.name) + '</div>' +
            '<div class="muted mono">' + run.name + '</div>' +
          '</td>' +
          '<td data-label="Status">' +
            '<span class="state ' + stateClass(run.state) + '">' + (run.state || "unknown") + '</span>' +
            '<div class="muted">verify: ' + (m.verify_status || "-") + '</div>' +
            '<div class="muted">stop: ' + (m.stop_reason || "-") + '</div>' +
          '</td>' +
          '<td data-label="Trajectory">' +
            '<div>turns ' + (m.turns || 0) + ', tools ' + (m.tool_calls || 0) + ', events ' + (run.event_count || 0) + '</div>' +
            '<div class="muted">grep ' + (m.grep_calls || 0) + ', read ' + (m.read_file_calls || 0) + ', edit ' + (m.edit_file_calls || 0) + '</div>' +
            '<div class="muted">first edit ' + firstEdit + ', guards ' + (m.guard_triggers || 0) + '</div>' +
          '</td>' +
          '<td data-label="Patch">' +
            '<div>' + (run.has_patch ? "present" : "none") + '</div>' +
            '<div class="muted">' + (m.patch_lines || 0) + ' lines, ' + (m.patch_bytes || 0) + ' bytes</div>' +
          '</td>' +
          '<td data-label="Updated">' +
            '<div>' + (run.updated_ago || "-") + '</div>' +
            '<div class="muted">' + new Date(run.updated_at).toLocaleString() + '</div>' +
          '</td>' +
          '<td data-label="Artifacts"><div class="links">' + links + '</div></td>' +
        '</tr>';
      }).join("");
    }

    async function loadRuns() {
      const limit = document.getElementById("limit").value;
      const response = await fetch('/api/runs?limit=' + encodeURIComponent(limit));
      const payload = await response.json();
      allRuns = payload.runs || [];
      document.getElementById("updated").textContent = 'updated ' + new Date(payload.updated_at).toLocaleTimeString();
      render();
    }

    document.getElementById("filter").addEventListener("input", render);
    document.getElementById("limit").addEventListener("change", loadRuns);
    document.getElementById("refresh").addEventListener("click", loadRuns);
    loadRuns();
    setInterval(loadRuns, 5000);
  </script>
</body>
</html>`))
