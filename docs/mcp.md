# internal/mcp — MCP 客户端

## Client（client.go）

stdio 子进程 + JSON-RPC 2.0。

- `Connect`：完成 `initialize`（协议版本 2024-11-05）握手 + `notifications/initialized` 通知
- `call`：串行发送并匹配响应 ID（mutex 保护）
- `ListTools`：解析 `tools/list`
- `CallTool`：拼接 `content` 中的 text 类型结果
- stderr 由 `drainStderr` 丢弃

## Tool 适配器（tool.go）

把 MCP 工具包装为 `tools.Tool` 接口。

## 配置

`[[mcp_servers]]`：`name / command / args`

- 连接或 `list_tools` 失败仅告警不阻塞启动
- 工具名与内置冲突时跳过（无 `server__` 前缀）
