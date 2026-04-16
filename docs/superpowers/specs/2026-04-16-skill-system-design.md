# Design: Skill System (Independent Subsystem)

**Date**: 2026-04-16
**Goal**: 为 goagent 添加 Skill 机制，支持工作流 skill（SKILL.md 指令）和能力 skill（Go func），借鉴 Codex 的 progressive disclosure 模式。

## Context

参考 Codex 的 Skill 实现：
- SKILL.md 文件格式（YAML frontmatter + Markdown instructions）
- 三级加载：启动时只加载 name+description（~50 token/skill），匹配时加载全文，完成后释放
- 支持文件系统发现 + 代码注册混合模式
- 支持隐式匹配（关键词）+ 显式调用（tool_use）

## Architecture

### Core Types

```go
type SkillMeta struct {
    Name        string   `yaml:"name"`
    Description string   `yaml:"description"`
    AutoMatch   bool     `yaml:"auto_match"`
    Keywords    []string `yaml:"keywords"`
}

type Skill struct {
    Meta         SkillMeta
    Instructions string   // SKILL.md body (lazy loaded)
    Tools        []Tool   // 附带的工具
    filePath     string   // 文件系统 skill 的路径
    loaded       bool     // instructions 是否已加载
}

type SkillRegistry struct {
    skills    map[string]*Skill
    active    map[string]bool
    maxActive int  // 最多同时激活数量（默认 3）
}
```

### Lifecycle: Discover → Match → Load → Execute → Release

```
Session 启动
  │
  ├─ Discover: 扫描 skills/ 目录，解析 SKILL.md frontmatter
  │            + 代码注册的 skill
  │            → 只把 name + description 注入 system prompt（~50 token/skill）
  │
  ├─ Match (每轮 LLM 调用前):
  │    隐式: 检查模型回复文本中是否包含 skill keywords
  │    显式: 模型通过 use_skill tool 调用
  │
  ├─ Load: 匹配成功 → lazy load 完整 instructions
  │        → 追加到 system prompt
  │        → 注册 skill 附带的 tools
  │
  ├─ Execute: agent 正常 loop，按 skill 指令执行
  │
  └─ Release: skill 任务完成 → 从 system prompt 移除
              → 注销 skill 附带的 tools
```

## SKILL.md Format

```yaml
---
name: database-migration
description: "Run and validate database migrations"
auto_match: true
keywords: ["migration", "schema", "migrate"]
---

## Instructions

1. Check current migration status
2. Run pending migrations
3. Validate schema
```

### File Structure

```
skills/
├── commit/
│   └── SKILL.md
├── code-review/
│   ├── SKILL.md
│   └── scripts/
│       └── lint.sh
└── swe-bench/
    └── SKILL.md
```

## Components

### 1. SkillRegistry (`goagent/skill.go`)

```go
func NewSkillRegistry() *SkillRegistry
func (r *SkillRegistry) Register(skill *Skill)           // 代码注册
func (r *SkillRegistry) LoadDir(dir string) error         // 扫描目录
func (r *SkillRegistry) Activate(name string) error       // 加载全文 + 注册 tools
func (r *SkillRegistry) Deactivate(name string)           // 移除指令 + 注销 tools
func (r *SkillRegistry) Match(text string) *Skill         // 关键词匹配
func (r *SkillRegistry) Summary() string                  // name+description 摘要
func (r *SkillRegistry) ActiveInstructions() string       // 激活 skill 的完整指令
```

### 2. use_skill Tool (`goagent/skill_tool.go`)

```go
type useSkillInput struct {
    Name   string `json:"name" desc:"Skill name to activate"`
    Action string `json:"action" desc:"activate or deactivate"`
}
```

内置 tool，模型通过 tool_use 显式激活/停用 skill。

## Integration Points

### session.go `step()`

System prompt 构建：
```
system = agent.system
  + "\n" + factCache.Render()
  + "\n" + skillRegistry.Summary()             // 所有 skill 摘要
  + "\n" + skillRegistry.ActiveInstructions()   // 激活 skill 全文
```

### session.go `Run()`

每轮 LLM 回复后，隐式匹配：
```go
if s.skillRegistry != nil {
    if skill := s.skillRegistry.Match(resp.Text()); skill != nil {
        s.skillRegistry.Activate(skill.Meta.Name)
    }
}
```

### agent.go

```go
// Agent 新增字段
skillRegistry *SkillRegistry
```

### options.go

```go
func WithSkillDir(dir string) Option      // 扫描目录
func WithSkill(skill *Skill) Option       // 代码注册
```

### NewSession()

如果 agent 有 skillRegistry，自动注册 `use_skill` tool。

## Edge Cases

**Skill 冲突**: 代码注册优先 > 文件系统。同名覆盖。

**Tool 命名冲突**: Skill 附带的 tool 加前缀 `skill:<skill-name>:<tool-name>`。

**文件系统错误**: SKILL.md 格式错误跳过并 warning；目录不存在静默跳过。

**内存管理**: 未激活 skill 不加载 instructions（lazy load）。最多同时激活 3 个，超限自动 deactivate 最早的。

## Hooks

```go
OnSkillLoad   func(ctx context.Context, name string) error
OnSkillUnload func(ctx context.Context, name string) error
```

## Configuration Example

```go
agent := cc.New(
    cc.WithProvider(provider),
    cc.WithSkillDir("./skills/"),
    cc.WithSkill(&cc.Skill{
        Meta: cc.SkillMeta{
            Name:        "quick-fix",
            Description: "Apply minimal code fix",
            AutoMatch:   true,
            Keywords:    []string{"fix", "bug", "patch"},
        },
        Instructions: "Focus on minimal changes. Do not refactor.",
    }),
)
```

## Future Extensions (Not Implemented Now)

- Skill 依赖声明 (`requires: ["git-commit"]`)
- Skill 参数化 (`params: {framework: pytest}`)
- Skill 版本管理
- Skill 仓库安装 (`cc skill install github.com/user/repo`)

## Verification

```bash
# Unit tests
go test -v -run "TestSkillRegistry|TestSkillTool"

# Integration: skill discovery from directory
go test -v -run "TestSkillDir"

# Integration: implicit matching + activation
go test -v -run "TestSkillImplicitMatch"
```
