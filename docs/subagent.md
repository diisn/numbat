# internal/subagent — 子 Agent

## SpawnAgentTool（spawn.go）

参数：`description / prompt / run_in_background / subagent_type`

- **前台模式**：阻塞等待子循环完成并返回结果
- **后台模式**：立即返回 `run_id`，任务注册到 `TaskRegistry`。使用 **detached context**，不随父 ctx 取消而终止（真正的后台并行）

## 子 Agent 特性

- 启动于**冷启动上下文**（只有 prompt，不继承父历史）
- 事件发到父 bus（TUI 可见嵌套进度）
- `maxSubagentDepth = 2`：深度达到上限后不再注册嵌套工具；调用时报 "Subagent nesting limit reached"
- `buildChildRegistry`：按 Profile 白名单过滤工具
- 子 Agent 任务独立 `<runsDir>/<childRunID>/.tasks`
- `parent_run_id` 构造：每个 run 用当前 runID 作为 parent，`subagent.started/finished` 正确关联

## AgentResultTool

`agent_result(run_id)` 轮询后台任务，未完成返回 `"still running"`。
