package main

import (
	"context"
	"fmt"
	"log"
	"os"

	cc "github.com/alexioschen/cc-connect/goagent"
	"github.com/alexioschen/cc-connect/goagent/provider/anthropic"
	"github.com/alexioschen/cc-connect/goagent/tool"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY not set")
	}

	provider := anthropic.New(apiKey)

	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithModel("claude-sonnet-4-20250514"),
		cc.WithSystem("You are a helpful assistant."),
		cc.WithTools(tool.Shell()),
	)

	ctx := context.Background()
	result, err := agent.Run(ctx, "What is the current date? Use the shell tool to find out.")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Agent: %s\n", result.Output)
	fmt.Printf("Turns: %d\n", result.Turns)
	fmt.Printf("Tokens: %d in / %d out\n", result.Usage.InputTokens, result.Usage.OutputTokens)
}
