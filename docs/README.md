# GoAgent - Go Agent Runtime

A Go-idiomatic agent runtime with tool-calling, multi-provider LLM support, MCP integration, multi-agent collaboration, and SWE-bench evaluation.

## Architecture

```
User Input
    |
    v
+----------+     +----------+     +---------+
|  Agent   |---->| Provider |---->| Claude/ |
|  Loop    |<----|(interface)|<----| OpenAI  |
+----+-----+     +----------+     +---------+
     |
     v  tool_use?
+---------+     +---------+     +----------+
|  Tool   |---->| Sandbox |---->| Approver |
| Registry|     | (regex/ |     | (auto/   |
|         |     |  OS)    |     |  prompt) |
+---------+     +---------+     +----------+
```

Core loop (ReAct pattern):
1. Send messages + tool definitions to LLM
2. If response contains `tool_use` -> execute tools -> append results -> goto 1
3. If response is text only -> return to user
4. If max turns exceeded -> return with error

## Features

### Core
- **ReAct Agent Loop** - think -> act -> observe -> loop
- **Session Isolation** - each conversation has independent memory
- **Functional Options** - `cc.New(cc.WithProvider(p), cc.WithTools(t1, t2))`

### LLM Providers
- **Anthropic Claude** - Messages API with tool_use
- **OpenAI Compatible** - works with GPT-4o, DeepSeek, Qwen, xopglm5, etc.
- **Streaming** - optional `StreamProvider` interface with SSE support

### Tool System
| Tool | Description |
|------|-------------|
| `shell` | Execute shell commands with timeout |
| `read_file` | Read files (supports line ranges) |
| `write_file` | Write/create files |
| `edit_file` | Targeted string replacement in files |
| `list_files` | List directory contents (recursive) |
| `grep` | Regex search in files |
| `http_request` | HTTP GET/POST |

### MCP Integration
- MCP Client with JSON-RPC 2.0 protocol
- **stdio transport** - connect to local MCP servers via subprocess
- **SSE transport** - connect to remote MCP servers via HTTP
- `WithMCPServer("npx", "@modelcontextprotocol/server-filesystem", "/tmp")`

### Multi-Agent
- **Sub-Agent** - `cc.AsAgentTool("researcher", "desc", subAgent)` wraps Agent as Tool
- **MessageBus** - ZMQ-style in-process messaging:
  - Pub/Sub (topic broadcast)
  - Req/Rep (synchronous request-reply)
  - Push/Pull (task queue with multiple workers)
  - Pipeline (sequential agent processing)
  - FanOut (parallel execution)
- **SharedState** - thread-safe KV store via context propagation
- **Context Injection** - parent conversation context flows to sub-agents

### Security
- **Sandbox** (application-level) - regex pattern blocking + path whitelist
- **OS Sandbox** (opt-in) - macOS Seatbelt / Linux Docker isolation
- **Approver** - tool approval before execution:
  - `AutoApprover` - approve all
  - `PromptApprover` - interactive y/n/a
  - `PatternApprover` - auto-approve safe tools, prompt for others
  - `DenyApprover` - deny all

### Context Management
- **BufferMemory** - unbounded (default)
- **WindowMemory** - sliding window (last N messages)
- **CompressMemory** - auto-compress when messages exceed threshold
- **Token-Aware Compression** - compress at 70% of context window
  - Keeps first 10% + last 10% of messages
  - Simplifies (not drops) tool results in middle 80%

### Reliability
- **Structured Errors** - `ProviderError` with status code, type, retryable flag
- **Auto Retry** - exponential backoff for rate limits and server errors
- **Exploration Guard** - abort if agent spends too many turns without making changes

## Quick Start

```go
package main

import (
    cc "github.com/alexioschen/cc-connect/goagent"
    "github.com/alexioschen/cc-connect/goagent/provider/openai"
    "github.com/alexioschen/cc-connect/goagent/tool"
)

func main() {
    provider := openai.New(os.Getenv("OPENAI_API_KEY"),
        openai.WithBaseURL(os.Getenv("OPENAI_BASE_URL")),
    )

    agent := cc.New(
        cc.WithProvider(provider),
        cc.WithModel("gpt-4o"),
        cc.WithSystem("You are a helpful assistant."),
        cc.WithTools(tool.Shell(), tool.ReadFile(), tool.EditFile(), tool.Grep()),
        cc.WithDefaultSandbox(),
        cc.WithAutoApprove(),
        cc.WithTokenAwareCompressMemory(200000, 10),
        cc.WithMaxRetries(3),
    )
    defer agent.Close()

    result, _ := agent.Run(context.Background(), "What files are in the current directory?")
    fmt.Println(result.Output)
}
```

## CLI

```bash
# Interactive REPL
go run ./cmd/agent -provider openai

# Single-shot
go run ./cmd/agent -provider openai "What is 2+2?"

# With options
go run ./cmd/agent -provider openai -approval interactive -stream -max-turns 20
```

## SWE-bench Evaluation

Adapter for testing goagent on real GitHub issue fixing tasks.

```bash
# Run on a single instance
cd swebench/adapter
OPENAI_API_KEY=... OPENAI_BASE_URL=... LLM_MODEL=xopglm5 \
  ./adapter /path/to/instance.json

# Random sample from dataset
cd swebench/runner
./runner --dataset /path/to/swebench_lite.json --n 5
```

### Results (sympy__sympy-11400)

| Config | Turns | Result | Patch Quality |
|--------|-------|--------|---------------|
| No limit (25 turns) | 25 | Pass | Extra unnecessary changes |
| maxExplorationTurns=15 | 15 | Pass | Clean, minimal patch |
| maxExplorationTurns=12 | 12 | Fail | Aborted too early |
| maxExplorationTurns=8 | 8 | Fail | Aborted too early |

## Project Structure

```
goagent/
+-- agent.go              # Agent struct, New(), Run(), NewSession()
+-- session.go            # Session: agent loop, tool execution
+-- message.go            # Message, Content types
+-- provider.go           # Provider interface, ChatParams
+-- tool.go               # Tool interface, FuncTool generic helper
+-- options.go            # All functional options
+-- memory.go             # Memory interface, BufferMemory, WindowMemory
+-- memory_compress.go    # CompressMemory, token-aware compression
+-- subagent.go           # AgentTool adapter, SharedState
+-- bus.go                # MessageBus (Pub/Sub, Req/Rep, Push/Pull, Pipeline)
+-- approval.go           # Approver interface and implementations
+-- sandbox.go            # Application-level sandbox (regex + path whitelist)
+-- sandbox_os.go         # OS-level sandbox (Seatbelt/Docker)
+-- stream.go             # StreamEvent, StreamReader, StreamProvider
+-- retry.go              # RetryConfig, exponential backoff
+-- errors.go             # Sentinel errors, ProviderError, ToolError
+-- hook.go               # Lifecycle hooks
+-- provider/
|   +-- anthropic/        # Claude Messages API
|   +-- openai/           # OpenAI Chat Completions API
+-- tool/
|   +-- shell.go          # Shell command execution
|   +-- read_file.go      # File reading (with line ranges)
|   +-- write_file.go     # File writing
|   +-- edit_file.go      # Targeted string replacement
|   +-- list_files.go     # Directory listing
|   +-- grep.go           # Regex file search
|   +-- http_request.go   # HTTP GET/POST
+-- mcp/
|   +-- client.go         # MCP Client (initialize, tools/list, tools/call)
|   +-- transport.go      # Transport interface
|   +-- stdio.go          # Stdio transport (subprocess)
|   +-- sse.go            # SSE transport (HTTP)
|   +-- tool.go           # MCPTool adapter
|   +-- jsonrpc.go        # JSON-RPC 2.0 types
+-- cmd/agent/            # CLI application
+-- swebench/             # SWE-bench evaluation adapter
+-- examples/             # Usage examples
```

## Test Coverage

```bash
go test ./...                                    # Unit tests
go test -tags e2e ./...                          # E2E tests (requires API key)
go test -tags integration ./mcp/                 # MCP integration tests
```

| Category | Tests | Coverage |
|----------|-------|----------|
| Agent loop | 5 | SimpleText, ToolUse, MaxTurns, EmptyInput, NoProvider |
| Session | 3 | IndependentMemory, ClearMemory, BackwardCompatible |
| Sub-Agent | 6 | LLMDriven, CodeDriven, EmptyTask, StructuredResult, ContextPassthrough, SharedState |
| Approver | 3 | AutoApprove, DenyBlocks, PatternSelective |
| Sandbox | 6 | BlocksDangerous, AllowsSafe, PathWhitelist, CheckToolCall, AgentIntegration |
| Compression | 11 | AutoCompress, PreservesFirst, PreservesRecent, ToolInfo, SimplifiesResults, TokenAware |
| MessageBus | 11 | PubSub, ReqRep, PushPull, Pipeline, FanOut, ContextPropagation |
| MCP | 5 | Initialize, ListTools, CallTool, Error, ToolAdapter |
| MCP Integration | 5 | Lifecycle, Echo, Add, ToolAdapter, AgentIntegration |
| Stream | 3 | Collect, CollectWithToolUse, Close |
| Retry | 2 | RetryOnRateLimit, NoRetryOnAuth |
| OS Sandbox | 4 | IsAvailable, ContextPropagation, WrapCommand, BlocksAccess |

## Commit History

```
61e4c7d tune: set maxExplorationTurns=15 based on testing
da7d126 feat: abort agent when stuck in exploration mode
dc92827 perf: reduce agent turns with better tooling and prompt
a0ff9fd refactor: keep simplified tool results and dynamic 10/80/10 compression
d7f6780 feat: token-aware context compression at 70% window usage
cba7c44 feat: SWE-bench adapter with edit_file tool + per-turn logging
a32b3e6 feat: context compression + OS-level sandbox
c7cf9b4 feat: ZMQ-style MessageBus for multi-agent communication
138b13f feat: sandbox for tool execution security
65122e1 feat: add list_files and grep tools
f280f3c feat: tool approval system + double Ctrl+C interrupt
937bb83 feat: enhance sub-agent communication
6979f60 feat: sub-agent mechanism via AgentTool adapter
0135a41 feat: MCP client integration
b1019ff feat: streaming support with StreamProvider interface
5479cf6 feat: structured ProviderError with automatic retry
eea1470 feat: concurrent tool execution with errgroup
187c34b feat: add Session for conversation isolation
30bcdba feat: initial implementation of Go agent runtime
```
