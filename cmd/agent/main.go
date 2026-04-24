// CLI for the cc-connect agent runtime.
//
// Usage:
//
//	agent "What is 2+2?"          # Single-shot mode
//	agent                         # Interactive REPL mode
//
// Environment variables:
//
//	ANTHROPIC_API_KEY  - Anthropic API key (uses Claude by default)
//	OPENAI_API_KEY     - OpenAI API key (use with -provider openai)
//	OPENAI_BASE_URL    - Custom OpenAI-compatible base URL
//	AGENT_MODEL        - Model name override
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	cc "github.com/alexioschen/cc-connect/goagent"
	"github.com/alexioschen/cc-connect/goagent/channel"
	"github.com/alexioschen/cc-connect/goagent/channel/feishu"
	"github.com/alexioschen/cc-connect/goagent/provider/anthropic"
	"github.com/alexioschen/cc-connect/goagent/provider/openai"
	"github.com/alexioschen/cc-connect/goagent/tool"
)

func main() {
	mode := flag.String("mode", "cli", "Run mode: cli, repl, or bot")
	providerName := flag.String("provider", "anthropic", "LLM provider: anthropic or openai")
	model := flag.String("model", "", "Model name (default: provider-specific)")
	system := flag.String("system", "You are a helpful assistant with access to tools.", "System prompt")
	maxTurns := flag.Int("max-turns", 10, "Maximum agent loop turns")
	noTools := flag.Bool("no-tools", false, "Disable built-in tools")
	streamMode := flag.Bool("stream", false, "Enable streaming output")
	approvalMode := flag.String("approval", "auto", "Tool approval mode: auto, interactive, or pattern")
	flag.Parse()

	provider, err := buildProvider(*providerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	if *model == "" {
		*model = defaultModel(*providerName)
	}

	opts := []cc.Option{
		cc.WithProvider(provider),
		cc.WithModel(*model),
		cc.WithSystem(*system),
		cc.WithMaxTurns(*maxTurns),
	}

	// Set approval mode
	switch *approvalMode {
	case "auto":
		opts = append(opts, cc.WithAutoApprove())
	case "interactive":
		opts = append(opts, cc.WithInteractiveApprove())
	case "pattern":
		// Auto-approve read-only tools, prompt for others
		opts = append(opts, cc.WithPatternApprove([]string{"list_files", "grep", "read_file", "http_request"}))
	default:
		fmt.Fprintf(os.Stderr, "Unknown approval mode: %s\n", *approvalMode)
		os.Exit(1)
	}

	if !*noTools {
		opts = append(opts, cc.WithTools(
			tool.Shell(),
			tool.ReadFile(),
			tool.WriteFile(),
			tool.ListFiles(),
			tool.Grep(),
			tool.Search(),
			tool.LSP(),
			tool.HTTPRequest(),
		))
	}

	agent := cc.New(opts...)

	// Bot mode: run HTTP webhook server
	if *mode == "bot" {
		runBot(agent)
		return
	}

	// Single-shot mode: pass query as argument
	if args := flag.Args(); len(args) > 0 {
		query := strings.Join(args, " ")
		runOnce(agent, query)
		return
	}

	// Interactive REPL mode
	runREPL(agent, *streamMode)
}

func runBot(agent *cc.Agent) {
	cfg := loadConfig()

	// Validate Feishu configuration
	if cfg.Feishu.AppID == "" || cfg.Feishu.AppSecret == "" {
		fmt.Fprintln(os.Stderr, "Error: FEISHU_APP_ID and FEISHU_APP_SECRET must be set for bot mode")
		os.Exit(1)
	}

	// Create Feishu channel
	feishuCh := feishu.New(cfg.Feishu)

	// Create session manager
	mgr := channel.NewSessionManager(agent, cfg.Session)

	// Create and configure webhook server
	srv := channel.NewChannelServer(mgr)
	srv.Register(feishuCh)

	// Start HTTP server
	log.Printf("Starting bot server on %s", cfg.Addr)
	log.Printf("Webhook endpoint: http://localhost%s/webhook/feishu", cfg.Addr)
	log.Printf("Health check: http://localhost%s/health", cfg.Addr)

	if err := http.ListenAndServe(cfg.Addr, srv); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %s\n", err)
		os.Exit(1)
	}
}

func runOnce(agent *cc.Agent, query string) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	result, err := agent.Run(ctx, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
	fmt.Println(result.Output)
}

func runREPL(agent *cc.Agent, stream bool) {
	// Double Ctrl+C pattern: first cancels current operation, second exits
	var interruptCount atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range sigCh {
			count := interruptCount.Add(1)
			if count == 1 {
				fmt.Fprintln(os.Stderr, "\n^C (interrupt: cancelling current operation, press again to exit)")
				cancel()
			} else {
				fmt.Fprintln(os.Stderr, "\n^C (exiting)")
				os.Exit(0)
			}
		}
	}()

	session := agent.NewSession()
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("cc-connect agent (type 'exit' to quit, Ctrl+C twice to force exit)")

	for {
		// Reset interrupt count and context for each new input
		interruptCount.Store(0)
		ctx, cancel = context.WithCancel(context.Background())

		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}
		if input == "/clear" {
			session.ClearMemory()
			fmt.Println("Memory cleared.")
			continue
		}

		if stream {
			ch, err := session.RunStream(ctx, input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", err)
				continue
			}
			fmt.Println()
			for ev := range ch {
				switch ev.Type {
				case "text_delta":
					fmt.Print(ev.Text)
				case "error":
					if ev.Error == context.Canceled {
						fmt.Fprintln(os.Stderr, "\n[cancelled]")
					} else {
						fmt.Fprintf(os.Stderr, "\nError: %s\n", ev.Error)
					}
				case "message_stop":
					fmt.Printf("\n[tokens: %d in / %d out]\n", ev.Usage.InputTokens, ev.Usage.OutputTokens)
				}
			}
		} else {
			result, err := session.Run(ctx, input)
			if err != nil {
				if err == context.Canceled {
					fmt.Fprintln(os.Stderr, "[cancelled]")
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s\n", err)
				}
				continue
			}
			fmt.Printf("\n%s\n", result.Output)
			fmt.Printf("[turns: %d | tokens: %d in / %d out]\n", result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
		}
	}
	cancel()
}

func buildProvider(name string) (cc.Provider, error) {
	switch name {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		return anthropic.New(key), nil
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY not set")
		}
		var opts []openai.Option
		if base := os.Getenv("OPENAI_BASE_URL"); base != "" {
			opts = append(opts, openai.WithBaseURL(base))
		}
		return openai.New(key, opts...), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s (use 'anthropic' or 'openai')", name)
	}
}

func defaultModel(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-sonnet-4-20250514"
	case "openai":
		return "gpt-4o"
	default:
		return ""
	}
}
