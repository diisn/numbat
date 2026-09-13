# internal/task — 任务管理

## 模型（manager.go）

`Task{ID, Subject, Description, Status(pending/in_progress/completed), BlockedBy[], CreatedAt, UpdatedAt}`

每个任务一个 `task_<id>.json`。

## Manager

- `Create`：校验 `blocked_by` 依赖存在
- `Update`：支持改状态与增删依赖；置 `completed` 时自动从其他任务 `blocked_by` 中清除（`clearDependency`）
- `FormatList`：生成 `[ ]/#[>]/[x]` 标记列表

## per-run 隔离

管理器按 run 隔离（`<runsDir>/<runID>/.tasks`），主 run 与子 Agent 各持自己的目录实例。
