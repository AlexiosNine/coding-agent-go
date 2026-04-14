# MCP Client Integration — Design Spec

**Date**: 2026-04-14
**Approach**: Lightweight self-implemented MCP Client in `mcp/` subpackage
**Estimated scope**: ~400 lines, 6 new files, 2 modified files

---

## Context

The goagent runtime has a working tool system (`cc.Tool` interface) with built-in tools and custom tool registration. To connect to external MCP Servers (filesystem, database, web search, etc.), we need an MCP Client that:

1. Connects to MCP Servers via stdio or SSE transport
2. Discovers tools via `tools/list`
3. Executes tools via `tools/call`
4. Bridges MCP tools into the existing `cc.Tool` interface seamlessly

---

## Architecture

```
Agent (goagent)
  ↓ WithMCPServer() / WithMCPClient()
MCPTool (adapter, implements cc.Tool)
  ↓ Execute()
Client (mcp package)
  ↓ CallTool() — JSON-RPC 2.0
Transport (stdio / SSE)
  ↓
MCP Server (external process or remote)
```

Agent is unaware of MCP details — it only sees standard `cc.Tool` instances. The `mcp.Client` manages the full lifecycle: connect → initialize → discover tools → call tools → close.

---

## File Structure

```
goagent/
├── mcp/
│   ├── client.go          # MCP Client: Initialize, ListTools, CallTool, Close
│   ├── transport.go        # Transport interface
│   ├── stdio.go            # StdioTransport: subprocess stdin/stdout
│   ├── sse.go              # SSETransport: HTTP POST + SSE receive
│   ├── tool.go             # MCPTool adapter: bridges mcp tool → cc.Tool
│   └── jsonrpc.go          # JSON-RPC 2.0 request/response types
├── options.go              # Add WithMCPServer(), WithMCPClient()
└── agent.go                # Add mcpClients field, Close() method
```

---

## Core Types

### JSON-RPC (`mcp/jsonrpc.go`)

```go
type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      int64           `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      int64           `json:"id"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}
```

### Transport (`mcp/transport.go`)

```go
type Transport interface {
    Send(ctx context.Context, data []byte) error
    Receive(ctx context.Context) ([]byte, error)
    Close() error
}
```

### StdioTransport (`mcp/stdio.go`)

```go
type StdioTransport struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout *bufio.Scanner
    mu     sync.Mutex
}

func NewStdioTransport(command string, args ...string) (*StdioTransport, error)
```

- Starts subprocess with `exec.Command`
- Sends JSON-RPC messages via stdin (one line per message, newline-delimited)
- Receives responses via stdout line-by-line
- `Close()` kills the subprocess

### SSETransport (`mcp/sse.go`)

```go
type SSETransport struct {
    baseURL    string
    httpClient *http.Client
    sessionURL string // obtained from initial SSE endpoint
    events     chan []byte
}

func NewSSETransport(baseURL string) (*SSETransport, error)
```

- Connects to SSE endpoint (`GET /sse`), receives `endpoint` event containing POST URL
- Sends requests via HTTP POST to the session URL from the `endpoint` event
- Receives responses as SSE `message` events on the persistent GET connection

### Client (`mcp/client.go`)

```go
type Client struct {
    transport Transport
    nextID    atomic.Int64
    info      ClientInfo
    mu        sync.Mutex
}

type ClientInfo struct {
    Name    string
    Version string
}

type ServerInfo struct {
    Name            string
    Version         string
    ProtocolVersion string
}

type ToolInfo struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"inputSchema"`
}

func NewClient(transport Transport, info ClientInfo) *Client
func (c *Client) Initialize(ctx context.Context) (*ServerInfo, error)
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error)
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error)
func (c *Client) Close() error
```

`Initialize` sends:
```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"goagent","version":"1.0.0"}}}
```
Then sends `notifications/initialized` (no id, no response expected).

`ListTools` sends `{"method":"tools/list"}` and returns `[]ToolInfo`.

`CallTool` sends `{"method":"tools/call","params":{"name":"...","arguments":{...}}}` and returns the text content from the result.

### MCPTool Adapter (`mcp/tool.go`)

```go
type MCPTool struct {
    client *Client
    info   ToolInfo
}

func (t *MCPTool) Name() string                                          { return t.info.Name }
func (t *MCPTool) Description() string                                   { return t.info.Description }
func (t *MCPTool) InputSchema() json.RawMessage                          { return t.info.InputSchema }
func (t *MCPTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
    return t.client.CallTool(ctx, t.info.Name, input)
}
```

This implements `cc.Tool` — the Agent treats MCP tools identically to built-in tools.

---

## Agent Integration

### New Options (`options.go`)

```go
// WithMCPServer connects to an MCP Server via stdio.
// It starts the subprocess, initializes the connection, discovers tools,
// and registers them with the agent.
func WithMCPServer(command string, args ...string) Option

// WithMCPClient registers a pre-configured MCP Client.
// The caller is responsible for calling Initialize() before passing it.
func WithMCPClient(client *mcp.Client) Option
```

### Agent Changes (`agent.go`)

```go
type Agent struct {
    // ... existing fields ...
    mcpClients []*mcp.Client // track for cleanup
}

// Close shuts down all MCP client connections.
func (a *Agent) Close() error
```

`WithMCPServer` flow:
1. Create `StdioTransport` with command + args
2. Create `mcp.Client`
3. Call `client.Initialize()`
4. Call `client.ListTools()`
5. Wrap each tool as `MCPTool` and register with agent
6. Append client to `mcpClients` for cleanup

---

## Protocol Details

### Message Format
- JSON-RPC 2.0 over newline-delimited JSON (stdio) or SSE (HTTP)
- Requests have `id` (integer), notifications have no `id`
- Protocol version: `2025-11-25`

### Lifecycle
1. Client → Server: `initialize` request
2. Server → Client: `initialize` response (server info + capabilities)
3. Client → Server: `notifications/initialized` notification
4. Client → Server: `tools/list` request
5. Client → Server: `tools/call` requests (during agent loop)
6. Client: `Close()` kills transport

### Error Handling
- JSON-RPC errors (code + message) → wrapped as Go errors
- Transport errors (process crash, connection lost) → wrapped with context
- Tool execution errors from MCP → returned as `ToolResultContent.IsError = true`

---

## Verification Plan

1. **Unit tests**: Mock transport, verify JSON-RPC message format for initialize/list/call
2. **Integration test**: Start a simple MCP server (e.g., echo server in Go), connect via stdio, call a tool
3. **Agent integration test**: Create agent with `WithMCPServer`, verify MCP tools appear in `toolDefs()`
4. **Build**: `go build ./...` + `go vet ./...` passes
