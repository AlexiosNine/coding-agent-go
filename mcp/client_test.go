package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/alexioschen/cc-connect/goagent/mcp"
)

// mockTransport is a test transport that returns pre-configured responses.
type mockTransport struct {
	mu        sync.Mutex
	sent      [][]byte
	responses [][]byte
	recvIdx   int
}

func (m *mockTransport) Send(_ context.Context, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, append([]byte(nil), data...))
	return nil
}

func (m *mockTransport) Receive(_ context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recvIdx >= len(m.responses) {
		return nil, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.recvIdx]
	m.recvIdx++
	return resp, nil
}

func (m *mockTransport) Close() error { return nil }

func TestClient_Initialize(t *testing.T) {
	transport := &mockTransport{
		responses: [][]byte{
			[]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","serverInfo":{"name":"test-server","version":"0.1.0"},"capabilities":{}}}`),
		},
	}

	client := mcp.NewClient(transport, mcp.ClientInfo{Name: "test", Version: "1.0"})
	_, err := client.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify initialize request was sent
	if len(transport.sent) < 1 {
		t.Fatal("expected at least 1 sent message")
	}

	var req mcp.Request
	if err := json.Unmarshal(transport.sent[0], &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Method != "initialize" {
		t.Errorf("expected method 'initialize', got %q", req.Method)
	}
}

func TestClient_ListTools(t *testing.T) {
	transport := &mockTransport{
		responses: [][]byte{
			[]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","description":"Echo input","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}}]}}`),
		},
	}

	client := mcp.NewClient(transport, mcp.ClientInfo{Name: "test", Version: "1.0"})
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("expected tool name 'echo', got %q", tools[0].Name)
	}
}

func TestClient_CallTool(t *testing.T) {
	transport := &mockTransport{
		responses: [][]byte{
			[]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"hello world"}]}}`),
		},
	}

	client := mcp.NewClient(transport, mcp.ClientInfo{Name: "test", Version: "1.0"})
	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hello world"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %q", result)
	}

	// Verify the request format
	var req mcp.Request
	if err := json.Unmarshal(transport.sent[0], &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Method != "tools/call" {
		t.Errorf("expected method 'tools/call', got %q", req.Method)
	}
}

func TestClient_CallTool_Error(t *testing.T) {
	transport := &mockTransport{
		responses: [][]byte{
			[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid request"}}`),
		},
	}

	client := mcp.NewClient(transport, mcp.ClientInfo{Name: "test", Version: "1.0"})
	_, err := client.CallTool(context.Background(), "bad", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMCPTool_ImplementsInterface(t *testing.T) {
	transport := &mockTransport{
		responses: [][]byte{
			[]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"42"}]}}`),
		},
	}

	client := mcp.NewClient(transport, mcp.ClientInfo{Name: "test", Version: "1.0"})
	tool := mcp.NewMCPTool(client, mcp.ToolInfo{
		Name:        "calc",
		Description: "Calculator",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	})

	if tool.Name() != "calc" {
		t.Errorf("expected name 'calc', got %q", tool.Name())
	}
	if tool.Description() != "Calculator" {
		t.Errorf("expected description 'Calculator', got %q", tool.Description())
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"expr":"6*7"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "42" {
		t.Errorf("expected '42', got %q", result)
	}
}
