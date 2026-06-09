# goagent 阶段性总结

## 2026-06-08 轨迹收敛与能力评估落盘

### 本轮已完成

1. **核心 agent 轨迹事件**
   - 新增 `AgentEvent` / `EventSink` / `WithEventSink(...)`，覆盖 `run_started`、`model_response`、`tool_call_started`、`tool_call_finished`、`guard_triggered`、`run_finished`。
   - `RunResult` 增加 `StopReason`、`GuardReason`、`RunMetrics`，用于区分 `success_text`、`max_turns`、`stopped_by_guard`、`provider_error` 等结束原因。

2. **硬停止收敛 guard**
   - SWE-bench 默认启用 `DefaultConvergenceGuardConfig()`。
   - 当前会阻断：连续 read-only 工具调用、重复读取同一文件区域、成功 `edit_file` 后继续 read-only、exploration budget 已耗尽后继续读取。
   - guard 触发时会写 `guard_triggered` 事件，并以 `stopped_by_guard` 结束 run。

3. **SWE-bench 评估产物**
   - `swebench/run_case_collect.sh` 会把单 case 产物集中保存到 `swebench/runs/<timestamp>_<instance_id>/`。
   - 关键产物包括：`events.jsonl`、`metrics.json`、`summary.md`、`runner_status.jsonl`、`adapter.log`、`runner.stdout.log`、`runner.stderr.log`、`prediction.jsonl`、`prediction_patch.diff`、`repo.diff`、`repo_status.txt`、`env_summary.txt`。
   - `metrics.json` 是 source of truth；验证失败会覆盖 runner 的“已生成 patch”成功判定，避免把 verification failure 计为能力成功。

### 后续优化方向

1. **多 case 聚合分析**
   - 增加一个本地汇总脚本，扫描 `swebench/runs/*/metrics.json` 和 `runner_status.jsonl`，输出按 model/toolset/case 分组的成功率、guard 触发率、平均 turns、平均 patch size。
   - 评测指标与 LLM-as-judge 的边界见 `docs/agent-evaluation-and-llm-judge.md`。

2. **guard 阈值自适应**
   - 对 `sympy__sympy-11400`、`django__django-11179`、`pytest-dev__pytest-11143` 分别记录当前阈值下的失败类型，再决定是否按 repo/test family 设置不同的 read-only 和 budget 阈值。

3. **verification 泛化**
   - 继续把 `verify_patch.sh` 从单 case 经验扩展为 repo-aware verifier，让 `verify_status=failed` 能带更具体的失败分类，例如 import error、unit failure、timeout、setup error。

## 2026-06-01 SWE-bench 小样本优化进度

### 当前目标

本轮目标已经收敛为：先用 2-3 个 SWE-bench Lite case 做可重复的小样本优化，而不是一次性覆盖全部 benchmark。当前选定 case：

- `django__django-11179`
- `sympy__sympy-11400`
- `pytest-dev__pytest-11143`

这 3 个 case 已固化为本地 golden suite：`swebench/suites/golden_cases.txt`，可通过 `swebench/run_suite_collect.sh` 执行。

优化重点是减少无效探索、降低 token 消耗，并确保 agent 只能在准备好的本地 workspace 中操作。

### 已完成进展

1. **SWE-bench workspace 本地化与隔离**
   - Adapter 默认 workspace 从 `/tmp` 改为项目内 `swebench/workspace/<instance_id>`。
   - 可通过 `SWE_WORKSPACE_ROOT` 覆盖 workspace 根目录。
   - 每个 case 会 checkout 到 `base_commit`，并执行 `git reset --hard <base_commit>` 与 `git clean -fd`，保证 case 运行前状态干净。
   - Adapter 会 `chdir` 到当前 case repo，使模型只能围绕当前 checkout 后的仓库使用相对路径。
   - Agent 使用 `WithStrictSandbox(workDir)`，限制工具读写范围在当前 case workspace 内。

2. **工具集收敛**
   - 默认 `SWE_TOOLSET=lean`，只暴露 `grep`、`read_file`、`edit_file`。
   - `SWE_TOOLSET=full` 提供 `read_file`、`write_file`、`edit_file`、`list_files`、`grep`。
   - 两种工具集都不暴露 `shell`，避免 benchmark 过程中越过 workspace 边界执行任意命令。

3. **Token 与 turn 参数可配置**
   - `SWE_MAX_TOKENS` 默认 `4096`
   - `SWE_CONTEXT_WINDOW` 默认 `12000`
   - `SWE_RECENT_WINDOW` 默认 `4`
   - `SWE_TOOL_OUTPUT_CHARS` 默认 `5000`
   - `SWE_TOOL_SUMMARY_CHARS` 默认 `1000`
   - `SWE_EXPLORATION_BUDGET` 默认 `10`
   - `SWE_MAX_TURNS` 默认 `18`
   - `TURN_DELAY` 默认 `15s`

4. **Runner 支持固定 case 选择**
   - 新增 `--instances` 参数，可显式指定小样本 case 列表。
   - 默认随机样本数量从 5 收敛为 3。
   - 修复 runner 调 adapter 的路径：优先使用已构建的 `adapter` 二进制，不存在时 fallback 到 `go run .`。

5. **验证与测试**
   - 新增 adapter/runner 单元测试，覆盖 lean/full 工具集、workspace root、instance 选择等行为。
   - 已通过：
     - `GOCACHE=/private/tmp/goagent-gocache go test -count=1 ./...`
     - `GOCACHE=/private/tmp/goagent-gocache go test -count=1 .`（`swebench/adapter`）
     - `GOCACHE=/private/tmp/goagent-gocache go test -count=1 .`（`swebench/runner`）
   - 已验证当前 OpenAI-compatible 配置支持 native tool calling。

### 环境配置获取方式

生产/实验运行仍优先使用标准环境变量：

```bash
export OPENAI_API_KEY="<secret_key>"
export OPENAI_BASE_URL="<base_url>"
export LLM_MODEL="xopkimik25"
```

当前仓库的 e2e 测试文件中存在一份本地 fallback 配置，可用于本地复现实验。不要把 key 值写入日志或文档，可用以下方式在 shell 中提取并仅注入子进程环境：

```bash
KEY=$(perl -ne 'print "$1\n" if /apiKey = "([^"]+)"/' e2e_test.go | head -1)
BASE=$(perl -ne 'print "$1\n" if /baseURL = "([^"]+)"/' e2e_test.go | head -1)

OPENAI_API_KEY="$KEY" OPENAI_BASE_URL="$BASE" LLM_MODEL=xopkimik25 \
  GOCACHE=/private/tmp/goagent-gocache \
  go test -tags e2e -run TestE2E_ToolCalling -count=1 -v .
```

已知 fallback `base_url` 为 `https://maas-api.cn-huabei-1.xf-yun.com/v2`；`secret_key` 从 `e2e_test.go` 或 `subagent_e2e_test.go` 中提取，不应明文复制到其他文档。

### 后续优化方向

1. **小样本实验闭环**
   - 对固定 3 个 case 跑 `lean` 配置，记录成功率、turn 数、patch size、日志中的工具调用次数。
   - 再用同样 case 对比 `full` 工具集，确认 `list_files/write_file` 是否真正提升成功率。

2. **指标解析器**
   - 从 `swebench/workspace/logs/*.log` 和 runner 输出中自动汇总：turn 数、工具调用次数、exploration budget 触发次数、patch 字节数、是否生成 prediction。
   - 后续可以把每轮参数和指标写成 JSONL，方便比较。

3. **验证脚本泛化**
   - 当前 `swebench/verify_patch.sh` 仍偏向单 case/单仓库验证。
   - 后续应抽象为 per-repo/per-instance verifier，按 Django/SymPy/Pytest 的实际测试命令选择最小验证集。

4. **repo 缓存与 clone 加速**
   - 增加本地 bare mirror/cache，避免每次 workspace 缺失时都从 GitHub clone。
   - workspace 只从本地 cache checkout，可减少网络波动对实验的影响。

5. **读文件精度优化**
   - 给 `read_file` 输出增加稳定行号或 compact context window，减少 edit_file 定位失败。
   - 结合 grep 结果自动建议最小读取范围，继续压缩无效 token。

**日期**: 2026-04-16  
**版本**: fe67bbb

## 本阶段完成的核心功能

### 1. Skill System（独立子系统）

**设计文档**: `docs/superpowers/specs/2026-04-16-skill-system-design.md`

借鉴 Codex 的 Skill 机制，实现了完整的 Skill 生命周期管理：

- **SkillRegistry**: 核心注册表，管理 skill 的发现、匹配、激活、释放
- **SKILL.md 格式**: YAML frontmatter + Markdown instructions，无外部依赖的轻量解析器
- **Progressive Disclosure**: 启动时只加载 name+description（~50 token/skill），激活时加载全文
- **双重调用方式**:
  - 显式：`use_skill` tool，模型通过 tool_use 调用
  - 隐式：关键词匹配，自动激活相关 skill
- **混合注册模式**:
  - 文件系统：`WithSkillDir("./skills/")` 扫描 SKILL.md
  - 代码注册：`WithSkill(&Skill{...})` 直接注册
  - 代码注册优先级 > 文件系统
- **内存管理**: 最多 3 个并发激活 skill，LRU 淘汰策略
- **测试覆盖**: 11 个单元测试，覆盖注册、激活、匹配、加载、解析

**代码量**: 702 行新增（skill.go + skill_tool.go + skill_test.go + 集成）

**使用示例**:
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

### 2. Grep Tool 优化

**问题**: 模型调用 grep 时经常忘记传 `recursive: true`，导致只搜索顶层目录，找不到深层文件（如 xopkimik25 案例中的 `class sinc` 定义）。

**修复**:
- `recursive` 从 `bool`（默认 false）改为 `*bool`（默认 nil → true）
- 新增 `max_depth` 参数（默认 10），防止无限递归
- 自动跳过噪音目录：`.git`, `node_modules`, `__pycache__`, `.tox`, `.eggs`, `build`, `dist`
- 深度计算基于搜索根路径的相对深度

**测试覆盖**: 5 个单元测试
- `TestGrep_DefaultRecursive`: 验证默认递归行为
- `TestGrep_NonRecursive`: 验证非递归模式
- `TestGrep_MaxDepth`: 验证深度限制
- `TestGrep_SkipsNoisyDirs`: 验证噪音目录跳过
- `TestGrep_SingleFile`: 验证单文件搜索

**影响**: 预计可以减少 4-7 轮无效探索（基于 xopkimik25 案例分析）

## 近期完成的其他功能（上下文）

### 3. Structured Tool Result Summary + Session Fact Cache

**设计文档**: `docs/superpowers/specs/2026-04-09-structured-tool-result-summary-design.md`

- **ToolResultSummarizer**: 智能提取工具输出中的关键信息（定义、引用、编辑点）
- **SessionFactCache**: 会话级事实缓存，累积关键信息并注入 system prompt
- **Per-tool 策略**: read_file 保留更多代码上下文（maxLen 2000），其他工具压缩

### 4. SWE-bench 优化

- **Patch Verification**: Python 测试工具验证 patch 正确性
- **Exploration Budget**: 统一 ReadTracker 和 turn counting，防止无限探索
- **Tool Output Compressor**: 智能截断工具输出，per-tool 策略
- **Edit File Fuzzy Matching**: 空白符归一化，提高匹配成功率
- **Few-shot Example**: 成功工作流示例注入 prompt

### 5. ReadTracker 优化

- **50-line Region Buckets**: 从纯文件路径改为 50 行区域桶，更精细的重复读取检测
- **Shell Detection**: 检测 shell 命令输出，避免误判

## 测试状态

**总测试数**: 114 个  
**通过率**: 100%  
**覆盖模块**:
- goagent 核心: 会话管理、工具调用、hooks、retry
- mcp: MCP 客户端、工具包装
- tool: grep, edit_file, read_file 等工具
- skill: SkillRegistry, SKILL.md 解析, use_skill tool

## 技术债务 & 未来方向

### Skill System 扩展点（已预留设计空间）

1. **Skill 依赖声明**: `requires: ["git-commit", "docker-build"]`
2. **Skill 参数化**: `params: {framework: pytest}`
3. **Skill 版本管理**: 同一 skill 多版本共存
4. **Skill 市场/仓库**: `cc skill install github.com/user/repo`

### 待优化项

1. **Grep 性能**: 大型仓库（>10k 文件）扫描优化
2. **Skill Tool 冲突**: 当前用前缀 `skill:<name>:<tool>`，可能需要更优雅的命名空间
3. **Skill Hooks**: `OnSkillLoad`/`OnSkillUnload` 已定义但未实现

## 架构演进

```
v0.1 (初始)
  ├─ Agent + Session
  ├─ Tool 系统
  └─ Provider 抽象

v0.2 (Token 优化)
  ├─ ReadTracker (重复读取检测)
  ├─ Tool Output Compressor
  └─ Exploration Budget

v0.3 (智能提取)
  ├─ ToolResultSummarizer
  ├─ SessionFactCache
  └─ Edit File Fuzzy Matching

v0.4 (当前)
  ├─ Skill System (独立子系统)
  ├─ Grep 递归优化
  └─ Progressive Disclosure
```

## 参考资料

**Skill System 设计参考**:
- [Codex CLI Skills Guide](https://itecsonline.com/post/codex-cli-agent-skills-guide-install-usage-cross-platform-resources-2026)
- [Codex Technical Reference](https://blakecrosley.com/guides/codex)
- [Porting Skills to OpenAI Codex](https://blog.fsck.com/2025/10/27/skills-for-openai-codex/)

**相关 Commits**:
- `fe67bbb`: Skill system 实现
- `9313efc`: Skill system 设计文档
- `2bc0ff7`: Grep 测试
- `a208807`: Grep 深度限制
- `d9a716b`: Grep 默认递归
- `7cd1630`: Structured tool result summary
- `ad43228`: SWE-bench patch verification

---

**下一步计划**:
1. 实际场景测试 Skill System（SWE-bench, 代码审查, commit 工作流）
2. 编写常用 skill 库（commit, code-review, test-runner, deploy）
3. 性能 profiling（大型仓库 grep 优化）
4. Skill 依赖声明实现
