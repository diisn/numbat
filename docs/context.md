# internal/context — 执行上下文

## ExecutionContext（context.go）

维护一次 run 的状态：

| 字段 | 说明 |
|------|------|
| `RunID` | 唯一标识 |
| `Goal` | 用户目标 |
| `MaxSteps` | 最大步数（从 config 读取） |
| `Messages` | 消息历史 `[]llm.Message` |
| `Step` | 当前步数 |
| `Status` | running / success / failed |
| `Result` | 最终输出 |
| `Reason` | 失败原因：`llm_error` / `cancelled` / `exceeded_max_steps` / `panic`（子 Agent） |
| `FinalAssistant` | 最后一条 assistant 消息的 content blocks，供调用方落库（中途压缩不会覆盖它） |
| `Compacted` | 本次 run 中途是否发生过上下文压缩：为真时 `Messages` 已被换成「摘要 + 确认」，调用方据此只落最终答复 |
| `SystemPromptOverride` | 非空时替换角色基础提示词（skill / 子 Agent profile 场景） |
| `GlobalContext` / `ProjectContext` / `SessionNotes` | 三层记忆来源 |

## SystemPrompt

`SystemPrompt(base)` 组合本次 run 的 system prompt：

```text
base（或被 SystemPromptOverride 替换）
  + "\n\n## Global Context\n"   + GlobalContext     （非空时）
  + "\n\n## Project Context\n"  + ProjectContext    （非空时）
  + "\n\n## Session Notes\n"    + SessionNotes
    + "\n\nRemember important durable facts by calling note_save."  （非空时）
```

三段记忆均来自 `internal/memory` 与 session notes，为空时整段省略。
角色基础提示词用 `llm.DefaultSystemPrompt`；调用方必须显式传入，否则该段会缺失。

## MarkSuccess / MarkFailed

`MarkSuccess(result)` 置 `Status="success"`；`MarkFailed(reason, result)` 置
`Status="failed"` 并记录 `Reason`。`IsDone()` 在两者之后均返回 true。
