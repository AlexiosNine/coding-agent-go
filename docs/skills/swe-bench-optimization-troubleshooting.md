---
name: swe-bench-optimization-troubleshooting
description: Use when optimizing SWE-bench agents and encountering token waste, repeated reads, API rate limits, concurrent map panics, or data pipeline issues
tags: [swe-bench, optimization, troubleshooting, debugging, agent]
---

# SWE-bench Agent 优化故障排查

## 概述

SWE-bench agent 优化中的常见问题及系统化解决方案。基于实际优化经验总结。

## 问题分类速查

| 症状 | 可能原因 | 快速检查 |
|------|---------|---------|
| Agent 重复读取同一文件 | nudge 未生效 | `grep "Note:" logs/*.log` |
| Turn 7-10 API rate limit | 压缩阈值过高 | 检查 context token 数 |
| `concurrent map writes` panic | SessionFactCache 无锁 | 运行 `go test -race` |
| edit_file 反复失败 | old_string 不精确 | 检查错误提示质量 |
| grep 返回 unmarshal error | 参数类型不兼容 | 检查 bool 字段定义 |

---

## 问题 1: Nudge 在数据管道中丢失

### 现象
- Agent 重复读取同一文件 3+ 次
- 日志中没有 "Already read" 或 "[Note:" 提示
- read_file 代码中已实现 nudge 机制

### 根本原因
Nudge 在数据处理管道中被截断：
1. **Compressor 截断**: `truncateReadFile()` 使用 head+tail 策略，nudge 在末尾被切掉
2. **Summarizer 丢弃**: `summarizeReadFile()` 重建输出时未保留 nudge 后缀

### 解决方案

**修复 Compressor** (`tool_output_compressor.go`):
```go
func truncateReadFile(output string, max int) string {
    // 提取并保留 nudge
    var nudge string
    if idx := strings.Index(output, "\n[Note: "); idx >= 0 {
        nudge = output[idx:]
        output = output[:idx]
    }
    
    // 截断内容
    suffix := truncateSuffix(len(output) - max)
    usable := max - len(suffix) - len(nudge)
    headSize := usable * 6 / 10
    tailSize := usable - headSize
    
    // 重新附加 nudge
    return output[:headSize] + "\n...\n" + 
           output[len(output)-tailSize:] + suffix + nudge
}
```

**修复 Summarizer** (`tool_result_summarizer.go`):
```go
func (s *ToolResultSummarizer) summarizeReadFile(output string) string {
    // 提取 nudge
    var nudge string
    if idx := strings.Index(output, "\n[Note: "); idx >= 0 {
        nudge = output[idx:]
        output = output[:idx]
    }
    
    // 处理内容...
    
    // 所有返回路径都附加 nudge
    return headerStr + "Content:\n" + content + nudge
}
```

### 验证
```bash
# 测试 nudge 是否出现在输出中
./run_test.sh django__django-11179 | grep -A 2 "Note:"
```

**相关 commit**: `5943b76`, `cbf1fe2`

---

## 问题 2: 重复读取检测过于激进

### 现象
- Agent 需要重新读取文件来构造 edit_file 的 old_string
- 被 "Already read" 拦截，无法获取精确内容
- 被迫使用 shell/sed 绕过，浪费轮数

### 根本原因
重复读取检测与 edit_file 工作流冲突：
- edit_file 需要**精确的 old_string**
- 模型必须看到实际内容才能构造
- 任何阈值（70%, 95%, 99%）都会阻止必要的重读

### 解决方案

**改为 nudge-only 模式**（提示但不阻塞）:
```go
// read_file.go
var nudge string
for _, prev := range readHistory {
    if overlap > 0.8 {
        nudge = fmt.Sprintf("\n[Note: You've already read %s lines %d-%d. If you need to make changes, use edit_file.]", 
            in.Path, prev.start+1, prev.end)
        break
    }
}

// 总是返回完整内容 + nudge
return content + nudge, nil
```

**关键**: 永远不要 `return error` 或截断内容，只附加提示。

### 验证
```bash
# 检查是否有 "Already read" 错误
grep "ERROR.*Already read" logs/*.log
# 应该没有输出

# 检查 nudge 是否出现
grep "Note:.*already read" logs/*.log
# 应该有输出
```

**相关 commit**: `775fe0b`, `cb5fb36`

---

## 问题 3: API Rate Limiting

### 现象
- Turn 7-10 时频繁出现 429 或 "system is busy"
- 压缩配置看起来合理：`WithTokenAwareCompressMemory(200000, 10)`
- 但压缩从未触发

### 根本原因
1. **压缩阈值过高**: 200k 太大，到 Turn 7 时 context 已经很大但还没触发压缩
2. **缺少 turn delay**: 连续请求导致 TPM (Tokens Per Minute) 超限

### 解决方案

**降低压缩阈值**:
```go
// 从 200k 降到 20k
cc.WithTokenAwareCompressMemory(20000, 10)
```

**添加 turn delay**:
```go
// 每轮之间延迟 15 秒
turnDelay := os.Getenv("TURN_DELAY")
if turnDelay == "" {
    turnDelay = "15s"
}
delay, _ := time.ParseDuration(turnDelay)
time.Sleep(delay)
```

### 诊断命令
```bash
# 检查每轮的 token 消耗
grep "total_tokens" logs/*.log | awk '{print $NF}'

# 检查压缩是否触发
grep "Compressing" logs/*.log
```

**相关 commit**: `45555b1`, `7d2b527`

---

## 问题 4: SessionFactCache 并发写入 Panic

### 现象
```
fatal error: concurrent map writes
goroutine 42 [running]:
runtime.throw(0x1a2b3c4)
```

### 根本原因
SessionFactCache 的 `facts` map 在多个 goroutine 中并发写入，没有加锁保护。

### 解决方案

**添加互斥锁**:
```go
type SessionFactCache struct {
    facts   []Fact
    seen    map[string]bool
    maxFacts int
    mu      sync.Mutex  // 添加锁
}

func (c *SessionFactCache) addFact(f Fact) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    if c.seen[f.Content] {
        return
    }
    // ... 其余逻辑
}
```

### 验证
```bash
# 运行 race detector
go test -race ./...

# 应该没有 race 警告
```

**相关 commit**: `6589969`

---

## 问题 5: grep 参数类型错误

### 现象
```
ERROR: unmarshal tool input: json: cannot unmarshal string into Go struct field grepInput.recursive of type bool
```

模型传 `"recursive": "true"` 字符串，但 Go struct 定义为 `*bool`。

### 根本原因
LLM 模型（xopkimik25, xopglm5）经常传字符串而非布尔值，JSON unmarshal 失败。

### 解决方案

**创建 flexBool 类型**:
```go
type flexBool struct {
    Value bool
}

func (f *flexBool) UnmarshalJSON(data []byte) error {
    // 尝试 bool
    var b bool
    if err := json.Unmarshal(data, &b); err == nil {
        f.Value = b
        return nil
    }
    
    // 尝试 string
    var s string
    if err := json.Unmarshal(data, &s); err == nil {
        switch strings.ToLower(s) {
        case "true", "1", "yes":
            f.Value = true
            return nil
        case "false", "0", "no", "":
            f.Value = false
            return nil
        }
    }
    
    // 尝试 number
    var n float64
    if err := json.Unmarshal(data, &n); err == nil {
        f.Value = n != 0
        return nil
    }
    
    return fmt.Errorf("cannot unmarshal %s as bool", string(data))
}

// 使用
type grepInput struct {
    Recursive *flexBool `json:"recursive,omitempty"`
}
```

**相关 commit**: `8956d70`

---

## 问题 6: edit_file 错误提示不够精确

### 现象
- edit_file 失败时只返回 "found partial match near line X"
- 模型不知道哪里不匹配，反复尝试错误的 old_string
- 浪费 2-3 轮

### 根本原因
错误提示缺少：
1. 逐行 diff（哪些行不匹配）
2. 足够的上下文（前后 5 行）
3. 实用建议

### 解决方案

**增强错误提示**:
```go
func computeLineDiff(expected, actual []string) string {
    var diff strings.Builder
    for i := 0; i < min(len(expected), len(actual), 10); i++ {
        if strings.TrimSpace(expected[i]) != strings.TrimSpace(actual[i]) {
            diff.WriteString(fmt.Sprintf("  Line %d:\n", i+1))
            diff.WriteString(fmt.Sprintf("    Expected: %s\n", expected[i]))
            diff.WriteString(fmt.Sprintf("    Actual:   %s\n", actual[i]))
        }
    }
    return diff.String()
}

// 在 findSimilarContent 中
hint := fmt.Sprintf(`Hint: found partial match near line %d.

Differences:
%s

Actual content (lines %d-%d):
%s

Tip: Use read_file to get the exact content, then copy-paste as old_string.`,
    bestLine+1, diff, ctxStart+1, ctxEnd, contextLines)
```

**相关 commit**: `8956d70`

---

## 最佳实践

### Token 优化
- 压缩阈值设为 20k（不是 200k）
- 摘要长度 500-800 tokens
- 每轮延迟 15s 避免 TPM 超限
- 移除冗余工具（如 search）

### 数据管道设计
- **关键数据必须在所有管道阶段保留**（如 nudge）
- Compressor → Summarizer → Output 每个阶段都要显式处理
- 用 `strings.Index` 提取，处理后重新附加

### 并发安全
- 所有共享状态加锁（`sync.Mutex`）
- 用 `go test -race` 验证
- 优先使用 channel 而非共享内存

### 工具参数容错
- 对 LLM 常见错误（字符串 bool）做容错
- 用自定义 UnmarshalJSON 处理多种类型
- 记录 warning 但不阻塞执行

---

## 诊断清单

### 症状: Agent 陷入重复读取循环
- [ ] 检查 nudge 是否在日志中出现：`grep "Note:" logs/*.log`
- [ ] 检查 compressor 是否保留 nudge（查看 `tool_output_compressor.go`）
- [ ] 检查 summarizer 是否保留 nudge（查看 `tool_result_summarizer.go`）
- [ ] 验证 nudge 模式是 "提示" 而非 "阻塞"

### 症状: API rate limiting (429)
- [ ] 检查压缩阈值：应该是 20k 而非 200k
- [ ] 检查是否有 turn delay：至少 15s
- [ ] 查看每轮 token 消耗：`grep total_tokens logs/*.log`
- [ ] 确认压缩是否触发：`grep Compressing logs/*.log`

### 症状: Concurrent map writes panic
- [ ] 运行 `go test -race ./...`
- [ ] 检查所有 map 操作是否有锁保护
- [ ] 查看 SessionFactCache 的 `mu sync.Mutex`

### 症状: grep unmarshal error
- [ ] 检查 Recursive 字段类型：应该是 `*flexBool` 而非 `*bool`
- [ ] 验证 flexBool 的 UnmarshalJSON 实现
- [ ] 测试：`echo '{"recursive":"true"}' | go run test.go`

### 症状: edit_file 反复失败
- [ ] 检查错误提示是否包含 diff
- [ ] 检查上下文是否足够（±5 行）
- [ ] 验证 computeLineDiff 函数存在

---

## 工具参考

### 诊断命令
```bash
# Token 消耗分析
grep "total_tokens" logs/*.log | awk '{sum+=$NF; count++} END {print "Avg:", sum/count, "Total:", sum}'

# 重复读取检测
grep "read_file.*deletion.py" logs/*.log | wc -l

# API 错误统计
grep -c "error.*429\|busy" logs/*.log

# Race condition 检测
go test -race -count=10 ./...
```

### 测试命令
```bash
# 运行单个 case
cd swebench/adapter
./run_test.sh sympy__sympy-11400 xopkimik25

# 检查 nudge 是否生效
./run_test.sh django__django-11179 | grep -A 2 "Note:"

# 验证压缩触发
TURN_DELAY=15s ./run_test.sh sympy__sympy-11400 2>&1 | grep "Compressing"
```

---

## 参考资料

### 关键文件
- `tool/read_file.go` - nudge 机制
- `tool_output_compressor.go` - 输出压缩
- `tool_result_summarizer.go` - 结果摘要
- `session_fact_cache.go` - 并发安全
- `tool/grep.go` - 参数容错
- `tool/edit_file.go` - 错误提示

### 关键 Commits
- `5943b76` - Compressor nudge 保留
- `cbf1fe2` - Summarizer nudge 保留
- `775fe0b` - Nudge-only 模式
- `45555b1` - 压缩阈值优化
- `7d2b527` - Turn delay
- `6589969` - 并发安全修复
- `8956d70` - P0 优化（flexBool + edit_file hint）
