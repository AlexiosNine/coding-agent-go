# Skill System 修复计划

**日期**: 2026-04-16  
**目标**: 修复 code-reviewer 发现的 5 个关键问题

## 修复任务分配

### Task 1: 跨 Session 状态污染 (Critical)
**负责**: Agent A  
**文件**: `skill.go`, `session.go`, `agent.go`  
**问题**: `active []string` 在 SkillRegistry 全局共享，导致多 Session 状态污染  
**修复方案**:
1. 在 `Session` 结构体中新增 `activeSkills []string` 字段
2. `SkillRegistry` 只保留 skill 元数据和发现逻辑，移除 `active` 字段
3. `Activate/Deactivate` 改为返回 skill 指针，由 Session 管理激活状态
4. `ActiveInstructions()` 改为 `GetInstructions(names []string) string`
5. `Match()` 保持无状态，只返回匹配的 skill

**测试要求**:
- 新增测试：两个 Session 并发激活不同 skill，验证互不影响
- 运行 `go test -race -run TestSkill` 验证无竞争

---

### Task 2: use_skill 数据竞争 (Critical)
**负责**: Agent B  
**文件**: `skill_tool.go`, `skill.go`  
**问题**: `registry.skills[input.Name]` 无锁读取  
**修复方案**:
1. 在 `SkillRegistry` 新增方法 `GetSkill(name string) (*Skill, bool)`，内部加锁
2. `use_skill` 改用 `GetSkill()` 获取 skill
3. 或者让 `Activate()` 返回 `(*Skill, error)`

**测试要求**:
- 运行 `go test -race -run TestSkill` 验证无竞争
- 新增测试：并发调用 `use_skill` + `Register`

---

### Task 3: 隐式匹配不生效 (High)
**负责**: Agent C  
**文件**: `session.go`  
**问题**: 匹配后立即 return，激活的 skill 不会在当前 run 生效  
**修复方案**:
1. 将隐式匹配逻辑移到 `Run()` 循环开始处（每轮 step 前检查上一轮文本）
2. 或者匹配后设置标志位，继续下一轮 turn（不立即 return）

**测试要求**:
- 新增集成测试：模拟 LLM 回复包含关键词，验证下一轮 system prompt 包含 skill 指令
- 验证 skill 激活后能影响后续对话

---

### Task 4: Lazy Loading 未实现 (Medium)
**负责**: Agent D  
**文件**: `skill.go`  
**问题**: `LoadDir` 直接读取全文，没有按需加载  
**修复方案**:
1. `LoadDir` 只解析 frontmatter，存储 `filePath`，`loaded=false`
2. 新增 `loadInstructions(skill *Skill) error` 私有方法
3. `Activate` 时检查 `!loaded && filePath != ""`，调用 `loadInstructions`
4. `Deactivate` 时可选清空 `Instructions`（节省内存）

**测试要求**:
- 新增测试：验证 LoadDir 后 `Instructions` 为空
- 新增测试：Activate 后 `Instructions` 非空
- 新增测试：Deactivate 后 `Instructions` 被清空（如果实现）

---

### Task 5: 隐式匹配算法改进 (Low)
**负责**: Agent E  
**文件**: `skill.go`  
**问题**: substring 匹配误报 + map 遍历随机  
**修复方案**:
1. 改用词边界匹配：`\b` + `regexp` 或手动检查前后字符
2. 按注册顺序排序 skill（或按 priority 字段）
3. 返回第一个匹配的 skill（确定性）

**测试要求**:
- 新增测试：`"prefix"` 不应匹配 keyword `"fix"`
- 新增测试：多个 skill 匹配时，返回优先级最高的
- 新增测试：同一输入多次调用 Match，结果一致

---

## 执行顺序

1. **Task 1 + Task 2 并行**（都是 Critical，互不依赖）
2. **Task 3**（依赖 Task 1 的 Session 状态重构）
3. **Task 4 + Task 5 并行**（优化项，不阻塞核心功能）

## 验证标准

每个 Task 完成后：
1. 运行 `go test -race -run TestSkill -count=1`
2. 运行 `go test ./... -count=1`（全量测试，确保无回归）
3. 提交前运行 `go build ./...`

## 最终汇总

所有 Task 完成后，主 Agent 汇总：
1. 列出修改的文件和行数
2. 新增测试数量
3. 运行完整测试套件，报告通过率
4. 生成修复总结文档
