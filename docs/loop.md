# internal/loop — ReAct 主循环

## AgentLoop.Run（loop.go）

1. 循环条件 `!execCtx.IsDone() && Step < MaxSteps`，每轮 `Step++`
2. `compact.TruncateToolResults` 截断超长 tool_result
3. 发布 `LLMRequest` → 构造 `StreamCallbacks`（`OnToken`→`llm.token`，`OnUsage`→`llm.usage`）→ `provider.Chat`
4. `provider.Chat` 失败：ctx 已取消 → Status=failed、reason=cancelled，返回 error；
   否则 Status=failed、reason=llm_error，**不返回 error**（run 正常收尾，调用方仍落库）
5. 追加 assistant 消息并记录 `FinalAssistant`
6. `act` 执行本轮请求：
   - `tool_use` → **并行** Invoke 所有工具（`sync.WaitGroup`，结果按下标回填 `results[i]`）→ 追加 `user` 角色的 tool_result 消息
   - `max_tokens` 且有 tool_use → 补写合成错误 tool_result，保持消息平衡
   - `max_tokens` 且无 tool_use → 不追加任何内容，继续下一轮让模型补救
7. `end_turn` / 空 stop_reason → success；否则 `Step >= MaxSteps` → failed（reason=exceeded_max_steps）
8. `compactIfNeeded`：仅当本轮以 `tool_use` 收尾、run 未结束、且 `context_pct >= 阈值` 时压缩
   （与 Python 版条件一致；阈值默认 0 = 禁用，见 `config.AutoCompactThreshold`）

> 压缩发生在 run 中途，不在会话边界：把历史替换为「摘要 + 确认」两条后，
> 下一次 LLM 调用拿到的仍是合法输入。阈值 <= 0 表示关闭。
