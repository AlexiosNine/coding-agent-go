//go:build integration

package mcp_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	cc "github.com/alexioschen/cc-connect/goagent"
	"github.com/alexioschen/cc-connect/goagent/mcp"
)

// buildEchoServer compiles the echo server binary into a temp directory.
func buildEchoServer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "echoserver")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/echoserver")
	cmd.Dir = filepath.Join(".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build echo server: %v\n%s", err, out)
	}
	return bin
}

// newClient starts the echo server and returns an initialized MCP client.
func newClient(t *testing.T, bin string) *mcp.Client {
	t.Helper()
	transport, err := mcp.NewStdioTransport(bin)
	if err != nil {
		t.Fatalf("new stdio transport: %v", err)
	}
	client := mcp.NewClient(transport, mcp.ClientInfo{Name: "test", Version: "0.1.0"})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestMCPLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bin := buildEchoServer(t)
	client := newClient(t, bin)

	info, err := client.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if info.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocol version = %q, want %q", info.ProtocolVersion, "2025-11-25")
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["echo"] || !names["add"] {
		t.Errorf("expected echo and add tools, got %v", names)
	}
}

func TestMCPCallToolEcho(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bin := buildEchoServer(t)
	client := newClient(t, bin)

	if _, err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"hello mcp"}`))
	if err != nil {
		t.Fatalf("call echo: %v", err)
	}
	if result != "hello mcp" {
		t.Errorf("echo result = %q, want %q", result, "hello mcp")
	}
}

func TestMCPCallToolAdd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bin := buildEchoServer(t)
	client := newClient(t, bin)

	if _, err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result, err := client.CallTool(ctx, "add", json.RawMessage(`{"a":15,"b":27}`))
	if err != nil {
		t.Fatalf("call add: %v", err)
	}
	if result != "42" {
		t.Errorf("add result = %q, want %q", result, "42")
	}
}

func TestMCPToolAdapter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bin := buildEchoServer(t)
	client := newClient(t, bin)

	if _, err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	var echoInfo mcp.ToolInfo
	for _, ti := range tools {
		if ti.Name == "echo" {
			echoInfo = ti
			break
		}
	}

	mcpTool := mcp.NewMCPTool(client, echoInfo)
	if mcpTool.Name() != "echo" {
		t.Errorf("tool name = %q, want %q", mcpTool.Name(), "echo")
	}

	result, err := mcpTool.Execute(ctx, json.RawMessage(`{"text":"adapter test"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result != "adapter test" {
		t.Errorf("result = %q, want %q", result, "adapter test")
	}
}

// mockProvider is a test LLM provider that returns a tool_use on the first call
// and a text response on the second call (after receiving the tool result).
type mockProvider struct {
	calls int
}

func (m *mockProvider) Chat(_ context.Context, params cc.ChatParams) (*cc.ChatResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &cc.ChatResponse{
			Content: []cc.Content{
				cc.ToolUseContent{
					ID:    "call_1",
					Name:  "add",
					Input: json.RawMessage(`{"a":15,"b":27}`),
				},
			},
			StopReason: "tool_use",
		}, nil
	}
	// Second call: the tool result should be in messages; return final text.
	return &cc.ChatResponse{
		Content:    []cc.Content{cc.TextContent{Text: "The answer is 42"}},
		StopReason: "end_turn",
	}, nil
}

func TestMCPAgentIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bin := buildEchoServer(t)
	transport, err := mcp.NewStdioTransport(bin)
	if err != nil {
		t.Fatalf("new stdio transport: %v", err)
	}
	client := mcp.NewClient(transport, mcp.ClientInfo{Name: "test", Version: "0.1.0"})
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	// Build cc.Tool slice from discovered MCP tools.
	var ccTools []cc.Tool
	for _, ti := range tools {
		ccTools = append(ccTools, mcp.NewMCPTool(client, ti))
	}

	provider := &mockProvider{}
	agent := cc.New(
		cc.WithProvider(provider),
		cc.WithTools(ccTools...),
		cc.WithMaxTurns(5),
	)

	result, err := agent.Run(ctx, "What is 15 + 27?")
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	if result.Output != "The answer is 42" {
		t.Errorf("output = %q, want %q", result.Output, "The answer is 42")
	}
	if provider.calls != 2 {
		t.Errorf("provider calls = %d, want 2", provider.calls)
	}
	if result.Turns != 2 {
		t.Errorf("turns = %d, want 2", result.Turns)
	}
}
