# Agent Evaluation Notes

这份文档不是一套通用 benchmark 说明，而是给当前 `goagent` + SWE-bench 本地实验用的工作笔记。它要回答一个更朴素的问题：

> 当一个 coding agent 跑完一个 case，我们到底学到了什么？

如果只看“有没有 patch”，会把失败算成成功。如果只看“测试有没有过”，又解释不了为什么失败。如果指标太多，就会变成好看的噪声。所以评测要从第一性原理出发：agent 的任务是把一个模糊的问题描述，转化成一个在隔离 workspace 中可验证的最小代码修改。

因此每次 run 只需要先回答三件事：

1. 它是否真的修好了问题。
2. 它为什么走到这个结果。
3. 下一次优化应该改哪一层，而不是到处乱调。

## Ground Truth

SWE-bench 的主结果必须来自确定性验证。

在当前项目里，`metrics.json` 是 source of truth。原因很简单：runner 能看到有没有 patch，但不知道 patch 是否真的通过 verifier；adapter 才能把 agent 结果、patch 和验证结果合在一起。

主结果只保留少数几类：

- `case_success`: 有 patch，且 verifier 通过。
- `case_failed`: 有 patch，但 verifier 失败，或者 verifier 证明 patch 不满足 case。
- `stopped_by_guard`: 轨迹没有自然完成，被收敛 guard 截断。
- `command_failed`: 运行环境、runner、adapter 或验证命令本身失败。

一个重要细节：`stop_reason` 不等于 `run_result`。

例如 `sympy__sympy-11400` 的成功 run 里：

- `run_result=case_success`
- `verify_status=passed`
- `stop_reason=stopped_by_guard`

这不是矛盾。它表示 patch 已经通过验证，但 edit 后继续读被 guard 拦住了。这个 run 应算成功，同时把 `post-edit guard` 作为收敛质量问题继续优化。

## What To Measure

指标不应该从“能记录什么”出发，而应该从“它能帮我们做什么决定”出发。

### 结果指标

这些指标决定模型/配置是否值得继续看：

| 指标 | 来源 | 解释 |
|---|---|---|
| `run_result` | `metrics.json` | 最终 case 结果 |
| `verify_status` | `metrics.json` | verifier 是否通过 |
| `patch_bytes` / `patch_lines` | `metrics.json` | 是否真的产生修改，修改是否异常大 |
| `runner_status.generated_prediction` | `runner_status.jsonl` | runner 是否拿到 prediction |

最小聚合：

- `pass_rate = case_success / total_runs`
- `verified_patch_rate = verify_status=passed / has_patch`
- `no_patch_rate = patch_bytes=0 / total_runs`
- `command_failed_rate = command_failed / total_runs`

其中 `pass_rate` 是能力指标，`command_failed_rate` 是实验基础设施指标，不要混在一起解释。

### 轨迹指标

这些指标解释 agent 有没有在“有效接近答案”。

| 指标 | 来源 | 解释 |
|---|---|---|
| `turns` | `metrics.json` | 总共走了多少回合 |
| `first_edit_turn` | `metrics.json` | 第几轮开始真正修改 |
| `tool_calls` | `metrics.json` | 总工具调用数 |
| `grep_calls` | `metrics.json` | 定位动作数量 |
| `read_file_calls` | `metrics.json` | 上下文读取成本 |
| `edit_file_calls` | `metrics.json` | 修改动作数量 |
| `guard_triggers` | `metrics.json` / `events.jsonl` | 是否被不收敛规则拦截 |
| `read_only_after_edit_blocks` | `metrics.json` | edit 后是否还想继续探索 |

比较有用的派生指标：

- `read_to_edit_ratio = read_file_calls / max(edit_file_calls, 1)`
- `time_to_first_edit = first_edit_turn`
- `guard_stop_rate = stopped_by_guard / total_runs`
- `post_edit_guard_rate = read_only_after_edit_blocks > 0 的比例`

这些指标的解释要小心。读得多不一定坏，读错地方才坏；edit 早不一定好，没有定位就 edit 可能只是瞎改。指标只能提示异常，不能代替看轨迹。

### 成本指标

这些指标回答“同样成功，哪个更便宜”。

| 指标 | 来源 | 解释 |
|---|---|---|
| `input_tokens` / `output_tokens` | `metrics.json` | 模型成本 |
| `duration_ms` | `metrics.json` | 端到端耗时 |
| `tool_calls` | `metrics.json` | 工具侧成本 |

常用聚合：

- `tokens_per_success = sum(input_tokens + output_tokens) / case_success`
- `avg_duration_success`
- `avg_tool_calls_success`

成本指标只能在成功率接近时比较。一个配置省 token 但不出 patch，不是更优，只是更早失败。

## Failure Classification

每个失败 run 都要先归类，再决定改哪里。

| 失败类型 | 典型信号 | 优先检查 |
|---|---|---|
| no patch | `patch_bytes=0`，`edit_file_calls=0` | prompt 是否让模型知道何时停止探索；read 输出是否能支持 edit |
| stopped before edit | `stopped_by_guard`，`first_edit_turn=null` | budget、重复读取、工具结果摘要 |
| bad edit | 有 patch，verifier failed | patch diff、目标测试、是否改错抽象层 |
| tool failure | `tool_error` 或事件里 `is_error=true` | tool schema、sandbox、路径规则 |
| provider failure | `provider_error` | endpoint、模型 tool calling 兼容性 |
| environment failure | `command_failed` | checkout、依赖、verifier、脚本 |

这个表比单纯看分数更有用，因为它直接映射到下一步动作：

- prompt 问题：改 `templates/`。
- 工具问题：改 tool schema、输出格式或 sandbox。
- guard 问题：改 guard 判断或预算。
- verifier 问题：改验证脚本。
- 模型问题：换模型或降低任务难度。

## Event Log Reading

`events.jsonl` 的价值不是“完整记录所有东西”，而是让失败可以复盘。

看事件时按这个顺序：

1. 找 `guard_triggered`，确认是不是 guard 结束。
2. 找第一次 `edit_file`，看是否存在、是否成功。
3. 找重复的 `read_file`，看是不是同一路径同一区域。
4. 找 tool error，看是 schema、路径还是 sandbox。
5. 最后才读 `adapter.log`。

事件里最有价值的字段：

- `turn`
- `event`
- `tool`
- `is_error`
- `input_preview`
- `output_chars`
- `guard_reason`

`input_preview` 很关键，因为很多失败不是模型“不聪明”，而是它给工具的路径、行号或参数已经偏了。

## LLM-as-Judge

LLM judge 的角色不应该是裁判长，而应该像实验记录审阅者。它不能决定 case 是否成功，因为这件事 verifier 已经做得更可靠。它能做的是把轨迹读一遍，指出人下一步该看哪里。

### Judge 可以判断什么

- 定位是否合理：有没有找到正确文件、符号和附近测试。
- 上下文是否足够：edit 前是否读到了必要代码。
- 轨迹是否收敛：有没有重复读、空转、edit 后继续探索。
- patch 是否最小：有没有无关改动或大范围替换。
- patch 是否可信：是否符合项目局部风格，是否像解决一般问题而不是只糊样例。
- 失败归因是否明确：失败更像 prompt、tool、guard、budget、verifier 还是模型能力。

### Judge 不应该判断什么

- 不应该把 verifier failed 判成 success。
- 不应该覆盖 `metrics.json.run_result`。
- 不应该凭感觉决定 patch 可合入。
- 不应该评价没有证据支持的安全、许可证或生产风险。

简单说：确定性结果由脚本给，解释性判断由 judge 给。

## Judge Input Contract

给 judge 的输入越稳定，输出越可比较。推荐输入：

1. case metadata：`instance_id`、repo、problem statement。
2. `metrics.json`。
3. `runner_status.jsonl`。
4. `summary.md`。
5. 压缩后的事件摘要，只保留 turn、event、tool、input_preview、is_error、guard_reason。
6. `prediction_patch.diff` 或 `repo.diff`。
7. verifier 摘要。

不要给：

- API key、secret key、provider 原始认证信息。
- 整个 workspace。
- 超长源码上下文。
- 与本 case 无关的日志。

## Judge Rubric

建议用少量维度，不要把 judge 做成另一套复杂 benchmark。

```json
{
  "judge_version": "agent-eval-rubric-v1",
  "instance_id": "sympy__sympy-11400",
  "classification": "strong_success",
  "overall_score": 4,
  "scores": {
    "localization": 5,
    "context": 4,
    "convergence": 4,
    "patch_minimality": 5,
    "patch_plausibility": 5
  },
  "primary_failure_mode": null,
  "evidence": [
    "Found the C printer and sinc definition before editing.",
    "Used one focused insertion edit.",
    "Verifier passed."
  ],
  "next_action": "Track post-edit guard separately from pre-edit guard."
}
```

分类建议：

- `strong_success`: verifier 通过，patch 小，轨迹基本健康。
- `weak_success`: verifier 通过，但轨迹浪费明显或 patch 风险偏高。
- `promising_failure`: 没通过，但定位正确，失败接近可修。
- `non_convergent_failure`: 重复探索、没 edit、被 guard 停。
- `invalid_patch_failure`: 有 patch，但方向错或 verifier 明确失败。
- `tool_or_environment_failure`: 工具、sandbox、checkout、依赖或 verifier 本身失败。

## Judge Prompt

后续可以把这段放到 `templates/evaluation/llm_judge.md`。

```text
You are reviewing one local SWE-bench coding-agent run.

The deterministic result is authoritative:
- metrics.run_result is the source of truth.
- verification failure must not be judged as success.
- a verified patch remains success even if the stop reason is a post-edit guard.

Your job is to explain trajectory quality and the next engineering action.
Return strict JSON only.

Score 0-5:
- localization: found the right files/symbols/tests
- context: read enough relevant context before editing
- convergence: avoided repeated reads and unnecessary post-edit exploration
- patch_minimality: changed only what was needed
- patch_plausibility: patch matches local project style and likely fixes the general issue

Classify:
- strong_success
- weak_success
- promising_failure
- non_convergent_failure
- invalid_patch_failure
- tool_or_environment_failure

Inputs:
CASE_METADATA:
{{case_metadata}}

METRICS_JSON:
{{metrics_json}}

RUNNER_STATUS:
{{runner_status}}

SUMMARY:
{{summary_md}}

EVENTS_SUMMARY:
{{events_summary}}

PATCH:
{{patch_diff}}

VERIFIER_SUMMARY:
{{verifier_summary}}
```

## Calibration

Judge 需要校准，否则分数只是另一种噪声。

保留一小组固定 run：

- verified success。
- verified success 但 post-edit guard。
- no patch / stopped_by_guard。
- verifier failed。
- tool 或 environment failure。

每次换 judge prompt 或 judge 模型，先跑这组样本。只看三件事：

- 有没有把 failed 判成 success。
- 有没有把环境失败误判成模型能力失败。
- 同一个 run 多次评分是否稳定。

可接受标准：

- classification 基本一致。
- overall_score 波动不超过 1 分。
- next_action 指向同一层，而不是每次换一个方向。

## Minimal Workflow

单 case 调试：

1. 跑 `swebench/run_case_collect.sh <instance_id>`。
2. 读 `metrics.json`，先定结果。
3. 读 `events.jsonl`，找第一次 edit 和 guard。
4. 看 patch 和 verifier 摘要。
5. 让 judge 给失败归因和下一步动作。
6. 只改一层。
7. 重跑同一 case，对比同一组指标。

小样本评测：

1. 固定 2-3 个 case。
2. 固定 model、toolset、budget、templates 版本。
3. 每个 case 跑多次。
4. 先汇总确定性结果，再看 judge 解释。
5. 只有当失败类型稳定后，才扩大样本。

当前固定 suite 放在 `swebench/suites/golden_cases.txt`：

- `sympy__sympy-11400`
- `django__django-11179`
- `pytest-dev__pytest-11143`

执行入口：

```bash
./swebench/run_suite_collect.sh --max-turns 18 --budget 12 --toolset lean --turn-delay 0
```

suite 级输出在 `swebench/suite_runs/<timestamp>_golden/`，每个 case 的完整 artifacts 仍保存在该目录的 `cases/` 子目录。

## Current Baseline

当前可作为 sanity check 的成功 run：

- Run dir: `swebench/runs/20260609_011540_sympy__sympy-11400`
- `run_result`: `case_success`
- `verify_status`: `passed`
- `stop_reason`: `stopped_by_guard`
- `turns`: `10`
- `first_edit_turn`: `9`
- `tool_calls`: `12`
- `grep/read_file/edit_file`: `5/6/1`
- `patch_bytes`: `606`
- `patch_lines`: `17`

这条样本很适合校准 judge：它应该被判为成功，但也应该指出 post-edit guard 是一个需要单独标记的收敛现象。

## Next Work

最值得做的不是继续加指标，而是把这套评测闭环自动化：

1. 写一个聚合脚本，扫描 `swebench/runs/*/metrics.json`，输出 Markdown/JSON/CSV。
2. 把 judge prompt 放进 `templates/evaluation/llm_judge.md`。
3. 每次 run 生成可选的 `judge.json`。
4. 聚合报告里分开展示 deterministic result 和 judge explanation。
5. 用 2-3 个固定 case 建一个小 gold set。
