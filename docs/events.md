# internal/events — 事件总线 + 事件类型

## Bus（bus.go）

`map[reflect.Type][]Handler`，`Subscribe(type, handler)` / `Publish(ctx, event)`。

- 发布时复制 handler 切片防并发修改
- 所有 handler 都会执行，即使某个 handler 返回错误
- `Publish` 返回第一个非 nil 的错误（如有），但不影响其他 handler 执行
- **不做 panic 恢复**，handler panic 会向上冒泡

## 事件类型（events.go）

`Topic(ev)` 通过 `topicNames` 反射表映射为点分 topic 名：

| 事件 | Topic | 关键字段 |
|------|-------|---------|
| RunStarted / RunFinished | `run.started` / `run.finished` | run_id, goal / status, result, reason, steps |
| LLMRequest / LLMResponse | `llm.request` / `llm.response` | run_id, messages, system / text |
| LLMTokens / LLMUsage | `llm.token` / `llm.usage` | run_id, token / input_tokens, output_tokens, context_pct, cache_read_input_tokens, cache_creation_input_tokens |
| ToolCallStarted / Finished / Failed | `tool.call_started` / `tool.call_finished` / `tool.call_failed` | tool_use_id, tool_name, params / output, elapsed_ms / error, error_type, attempt |
| PermissionRequested / Granted / Denied | `permission.requested` / `permission.granted` / `permission.denied` | run_id, tool_use_id, tool_name, preview, session_id / decision |
| ContextCompacted | `context.compacted` | run_id, session_id, original_tokens, summary_tokens |
| SessionCreated / Closed | `session.created` / `session.closed` | session_id, mode / session_id |
| SubagentStarted / Finished | `subagent.started` / `subagent.finished` | run_id, parent_run_id, description / status |
| SkillInvoked | `skill.invoked` | skill_name, arguments, run_id |

> `llm.token` / `llm.usage` 只广播不写 trace；`session.*` 事件不入 trace。
