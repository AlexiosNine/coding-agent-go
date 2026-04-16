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
+---------+     +---------+     +----------+     +-------------+
|  Tool   |---->| Sandbox |---->| Approver |---->| ReadTracker |
| Registry|     | (regex/ |     | (auto/   |     | (nudge if   |
|         |     |  OS)    |     |  prompt) |     |  stuck)     |
+---------+     +---------+     +----------+     +-------------+
```

Core loop (ReAct pattern):
1. Send messages + tool definitions to LLM
2. If response contains `tool_use` -> approve -> sandbox check -> execute -> loop
3. If response is text only -> return to user
4. If max turns exceeded -> return with error
5. ReadTracker monitors read patterns, nudges model if stuck in exploration

## Features

### Core
- **ReAct Agent Loop** - think -> act -> observe -> loop
- **Session Isolation** - each conversation has independent memory
- **Functional Options** - `cc.New(cc.WithProvider(p), cc.WithTools(t1, t2))`

### LLM Providers
- **Anthropic Claude** - Messages API with tool_use
- **OpenAI Compatible** - works with GPT-4o, DeepSeek, Qwen, xopglm5, etc.
- **Streaming** - optional `StreamProvider` interface with SSE support

### Tool System (8 built-in tools)
| Tool | Description |
|------|-------------|
| `shell` | Execute shell commands with timeout + pagination |
| `read_file` | Read files (line ranges + pagination, default 500 lines/page) |
| `write_file` | Write/create files |
| `edit_file` | Targeted string replacement in files |
| `list_files` | List directory contents (recursive, paginated 100/page) |
| `grep` | Regex search in files (paginated 50 matches/page) |
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

### Token Optimization
- **Tool Definition Caching** - compute JSON schemas once, invalidate on AddTool
- **OutputBuffer** - session-scoped 50MB LRU cache for paginated tool outputs
- **Unified Pagination** - all tools support offset/limit for large outputs
- **Message Deduplication** - FNV-64a hash detects repeated tool results, replaces with reference
- **ReadTracker** - detects repeated reads (including shell sed/cat/grep), nudges model to take action

### Reliability
- **Structured Errors** - `ProviderError` with status code, type, retryable flag
- **Auto Retry** - exponential backoff for rate limits and server errors
- **Exploration Guard** - abort if agent spends too many turns without making changes
- **Shell Mutation Detection** - shell commands with >/open(/write( treated as non-exploration

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

| Config | Turns | Result | Notes |
|--------|-------|--------|-------|
| No limit (25 turns) | 25 | Pass | Extra unnecessary changes |
| maxExplorationTurns=15 | 15 | Pass | Clean, minimal patch |
| Token optimization + ReadTracker | 25 | Pass | ReadTracker nudged model to act |
| Without ReadTracker | 25 | Fail | Model stuck in repeated reads |

### Key Finding: ReadTracker

Models can get stuck in "exploration loops" - repeatedly reading the same files without making changes. ReadTracker detects this pattern (including shell-based reads via sed/cat/grep) and injects a system notice nudging the model to start editing. This was critical for reliable SWE-bench performance.

## Project Structure

```
goagent/                        # 51 Go files, ~7400 lines
+-- agent.go                    # Agent struct, New(), Run(), NewSession()
+-- session.go                  # Session: agent loop, tool execution
+-- message.go                  # Message, Content types
+-- provider.go                 # Provider interface, ChatParams
+-- tool.go                     # Tool interface, FuncTool generic helper
+-- options.go                  # All functional options
+-- memory.go                   # Memory interface, BufferMemory, WindowMemory
+-- memory_compress.go          # CompressMemory, token-aware compression
+-- output_buffer.go            # OutputBuffer for paginated tool outputs
+-- dedup.go                    # MessageDeduplicator
+-- read_tracker.go             # ReadTracker (nudge stuck models)
+-- subagent.go                 # AgentTool adapter, SharedState
+-- bus.go                      # MessageBus (Pub/Sub, Req/Rep, Push/Pull, Pipeline)
+-- approval.go                 # Approver interface and implementations
+-- sandbox.go                  # Application-level sandbox (regex + path whitelist)
+-- sandbox_os.go               # OS-level sandbox (Seatbelt/Docker)
+-- stream.go                   # StreamEvent, StreamReader, StreamProvider
+-- retry.go                    # RetryConfig, exponential backoff
+-- errors.go                   # Sentinel errors, ProviderError, ToolError
+-- hook.go                     # Lifecycle hooks
+-- provider/
|   +-- anthropic/              # Claude Messages API
|   +-- openai/                 # OpenAI Chat Completions API
+-- tool/
|   +-- shell.go                # Shell command execution + pagination
|   +-- read_file.go            # File reading (line ranges + pagination)
|   +-- write_file.go           # File writing
|   +-- edit_file.go            # Targeted string replacement
|   +-- list_files.go           # Directory listing + pagination
|   +-- grep.go                 # Regex file search + pagination
|   +-- http_request.go         # HTTP GET/POST
+-- mcp/
|   +-- client.go               # MCP Client (initialize, tools/list, tools/call)
|   +-- transport.go            # Transport interface
|   +-- stdio.go                # Stdio transport (subprocess)
|   +-- sse.go                  # SSE transport (HTTP)
|   +-- tool.go                 # MCPTool adapter
|   +-- jsonrpc.go              # JSON-RPC 2.0 types
+-- cmd/agent/                  # CLI application
+-- swebench/                   # SWE-bench evaluation adapter + runner
+-- examples/                   # Usage examples
```

## Test Coverage

```bash
go test ./...                                    # Unit tests (~70 tests)
go test -tags e2e ./...                          # E2E tests (requires API key)
go test -tags integration ./mcp/                 # MCP integration tests
```

| Category | Tests |
|----------|-------|
| Agent loop | 5 (SimpleText, ToolUse, MaxTurns, EmptyInput, NoProvider) |
| Session | 3 (IndependentMemory, ClearMemory, BackwardCompatible) |
| Tool Defs Cache | 1 (CachedAndInvalidated) |
| Sub-Agent | 6 (LLMDriven, CodeDriven, EmptyTask, StructuredResult, ContextPassthrough, SharedState) |
| Approver | 3 (AutoApprove, DenyBlocks, PatternSelective) |
| Sandbox | 6 (BlocksDangerous, AllowsSafe, PathWhitelist, CheckToolCall, AgentIntegration) |
| Compression | 11 (AutoCompress, PreservesFirst, PreservesRecent, ToolInfo, SimplifiesResults, TokenAware) |
| OutputBuffer | 2 (StoreAndPaginate, EvictsWhenOverSize) |
| Dedup | 2 (ReplacesDuplicates, DoesNotTouchUser) |
| MessageBus | 11 (PubSub, ReqRep, PushPull, Pipeline, FanOut, ContextPropagation) |
| MCP | 5 (Initialize, ListTools, CallTool, Error, ToolAdapter) |
| MCP Integration | 5 (Lifecycle, Echo, Add, ToolAdapter, AgentIntegration) |
| Stream | 3 (Collect, CollectWithToolUse, Close) |
| Retry | 2 (RetryOnRateLimit, NoRetryOnAuth) |
| OS Sandbox | 4 (IsAvailable, ContextPropagation, WrapCommand, BlocksAccess) |

## Commit History

```
74173b7 test: SWE-bench passes with ReadTracker shell detection
7fae67d fix: ReadTracker now detects shell-based repeated reads
b98fc0c feat: ReadTracker nudges model when stuck in repeated reads
34e4cd6 fix: detect shell-based file mutations as non-exploration turns
f41d1c9 docs: add pagination hint to shell tool description
b65fd94 tune: increase read_file default page size to 500 lines
049a373 docs: clarify start_line/end_line takes precedence over offset/limit
add567a feat: add message deduplication to reduce token usage
6acb614 feat: add pagination to grep and list_files tools
aa44d58 feat: add pagination to read_file and shell tools
0577437 perf: add session-scoped OutputBuffer for paginated tool results
6d3663a perf: cache tool definitions between turns
303182a docs: add token optimization implementation plan
42b2cbe docs: add token optimization design spec
6306100 docs: add project summary documentation
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
65f9f0c test: sub-agent E2E tests with real LLM
937bb83 feat: enhance sub-agent communication
6979f60 feat: sub-agent mechanism via AgentTool adapter
0a3d6f6 test: MCP integration tests with mock stdio server
0135a41 feat: MCP client integration
baeb7ea docs: add MCP client integration design spec
b1019ff feat: streaming support with StreamProvider interface
5479cf6 feat: structured ProviderError with automatic retry
eea1470 feat: concurrent tool execution with errgroup
187c34b feat: add Session for conversation isolation
e8702de docs: add goagent improvements design spec
30bcdba feat: initial implementation of Go agent runtime
```
