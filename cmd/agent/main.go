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
	"os"
	"os/signal"
	"strings"

	cc "github.com/alexioschen/cc-connect/goagent"
	"github.com/alexioschen/cc-connect/goagent/provider/anthropic"
	"github.com/alexioschen/cc-connect/goagent/provider/openai"
	"github.com/alexioschen/cc-connect/goagent/tool"
)

func main() {
	providerName := flag.String("provider", "anthropic", "LLM provider: anthropic or openai")
	model := flag.String("model", "", "Model name (default: provider-specific)")
	system := flag.String("system", "You are a helpful assistant with access to tools.", "System prompt")
	maxTurns := flag.Int("max-turns", 10, "Maximum agent loop turns")
	noTools := flag.Bool("no-tools", false, "Disable built-in tools")
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

	if !*noTools {
		opts = append(opts, cc.WithTools(
			tool.Shell(),
			tool.ReadFile(),
			tool.WriteFile(),
			tool.HTTPRequest(),
		))
	}

	agent := cc.New(opts...)

	// Single-shot mode: pass query as argument
	if args := flag.Args(); len(args) > 0 {
		query := strings.Join(args, " ")
		runOnce(agent, query)
		return
	}

	// Interactive REPL mode
	runREPL(agent)
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

func runREPL(agent *cc.Agent) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("cc-connect agent (type 'exit' to quit)")

	for {
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
			agent.ClearMemory()
			fmt.Println("Memory cleared.")
			continue
		}

		result, err := agent.Run(ctx, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			continue
		}
		fmt.Printf("\n%s\n", result.Output)
		fmt.Printf("[turns: %d | tokens: %d in / %d out]\n", result.Turns, result.Usage.InputTokens, result.Usage.OutputTokens)
	}
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
