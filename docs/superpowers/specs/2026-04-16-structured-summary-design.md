# Design: Structured Tool Result Summary + Session Fact Cache

**Date**: 2026-04-16
**Goal**: 减少 agent 轮数和 token 消耗，通过结构化摘要替代大段原始工具输出，并在 session 内累积可复用的知识卡片。

## Context

当前 SWE-bench 测试中，xopkimik25 模型用了 11 轮完成 sympy__sympy-11400。主要浪费在：
- 大段 read_file 输出占用上下文（500 行/页）
- 同样的信息（如文件中的函数列表）被多次读取
- 模型需要从长文本中自行提取关键事实

## Architecture

两个新组件 + 两个集成点：

```
tool.Execute() → 原始输出
  → OutputBuffer.Store()              // 保留原始，供翻页
  → ToolResultSummarizer.Summarize()  // 生成短摘要
  → SessionFactCache.Extract()        // 提取可复用事实
  → 返回摘要作为 tool_result 发给模型

step() 构建 ChatParams 时：
  system = agent.system + "\n" + factCache.Render()
```

## Component 1: ToolResultSummarizer

**文件**: `goagent/tool_result_summarizer.go`

```go
type ToolResultSummarizer struct {
    maxSummaryLen int // 默认 500
}

func NewToolResultSummarizer(maxLen int) *ToolResultSummarizer
func (s *ToolResultSummarizer) Summarize(toolName, output string) string
```

按工具类型的摘要策略：

| 工具 | 策略 | 示例 |
|------|------|------|
| `grep` | 匹配数 + 每条 `file:line` + 匹配行内容 | `Found 1 match: octave.py:395 def _print_sinc` |
| `read_file` | 文件路径 + 行范围 + 发现的 def/class 签名列表 | `ccode.py:100-300 contains: __init__, _print_Pow, ..., _print_sign (line 251)` |
| `shell` | exit code + 最后 5 行 | `exit 0, last: ...` |
| `list_files` | 目录 + 文件数 + 文件名列表 | `sympy/printing/ (15 files: ccode.py, octave.py, ...)` |
| `edit_file` / `write_file` | 不摘要，原样返回 | — |

提取 def/class 签名的方式：正则 `^\s*(def |class )\w+`，不需要 LLM。

## Component 2: SessionFactCache

**文件**: `goagent/session_fact_cache.go`

```go
type Fact struct {
    Category string // "definition", "reference", "insertion_point", "pattern"
    Content  string // 一行描述
    Source   string // 工具名 + turn
}

type SessionFactCache struct {
    facts    []Fact
    maxFacts int // 默认 20
}

func NewSessionFactCache(maxFacts int) *SessionFactCache
func (c *SessionFactCache) Extract(toolName, output string)
func (c *SessionFactCache) Render() string
```

提取规则（正则，不需要 LLM）：
- `grep` 结果中的 `file:line content` → `Fact{Category: "reference"}`
- `read_file` 中的 `def xxx` / `class xxx` → `Fact{Category: "definition"}`
- `edit_file` 成功 → `Fact{Category: "insertion_point"}`

`Render()` 输出格式：
```
[Session facts]
- reference: _print_sinc in octave.py:395
- definition: class sinc in trigonometric.py:1620
- insertion_point: after _print_sign in ccode.py:251
```

注入位置：system prompt 末尾，不占 user/assistant 消息位。

## Integration

### session.go `executeSingleTool()`

```
output := tool.Execute(ctx, tu.Input)
// 1. 存原始输出到 OutputBuffer（供翻页）
outputBuffer.Store(tu.ID, output)
// 2. 提取 facts
factCache.Extract(tu.Name, output)
// 3. 生成摘要替代原始输出
if summarizer != nil {
    output = summarizer.Summarize(tu.Name, output)
}
return ToolResultContent{Content: output}
```

### session.go `step()`

```
system := s.agent.system
if s.factCache != nil {
    system += "\n" + s.factCache.Render()
}
```

### agent.go

新增字段：
```go
toolResultSummarizer *ToolResultSummarizer
sessionFactCacheSize int
```

### options.go

```go
func WithToolResultSummary(maxLen int) Option
func WithSessionFactCache(maxFacts int) Option
```

### NewSession()

```go
if a.toolResultSummarizer != nil {
    s.summarizer = a.toolResultSummarizer
}
if a.sessionFactCacheSize > 0 {
    s.factCache = NewSessionFactCache(a.sessionFactCacheSize)
}
```

## Verification

```bash
# 单元测试
go test -v -run "TestToolResultSummarizer|TestSessionFactCache"

# SWE-bench 端到端
# 期望：轮数从 11 降到 7-8（xopkimik25）或保持 5（xopglm5）
```

## Trade-offs

- 摘要可能丢失模型需要的细节 → 原始输出保留在 OutputBuffer，模型可翻页获取
- Fact 提取基于正则，可能漏掉非标准格式 → 可逐步增加规则
- System prompt 变长 → facts 限制 20 条，约 500 字符，影响可控
