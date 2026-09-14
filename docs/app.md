# internal/app — 组件组装中心

`App.Run` 是 numbat-core 的装配入口，依次创建：

| 组件 | 创建方式 | 说明 |
|------|---------|------|
| 事件总线 | `events.New()` | 全局唯一 Bus |
| LLM Provider | `llm.NewAnthropicProvider(apiKey, model, baseURL)` | SSE 流式 |
| 工具注册表 | `tools.NewRegistry()` | 注册 read_file/list_dir/write_file/bash |
| 权限管理器 | `permissions.NewManager(policyPath, permTimeout)` | policy 路径当前为空（不落盘） |
| 工具调用器 | `tools.NewInvoker(registry, perm, bus, toolTimeout)` | 超时从 config 读取 |
| Trace | `trace.NewWriter(runsDir)` | 订阅总线并落盘 |
| MCP | 遍历 `config.McpServers` | 逐个 Connect + ListTools |
| 会话 | `session.NewManager(store, bus)` | 存储根 `~/.numbat/sessions` |
| run 取消 | `transport.NewRunCanceler()` | per-run cancel 注册表，供 `agent.abort` |
| RPC dispatch | `transport.NewServer()` | 设置 Bus、runsDir、trace，注册 RPC handler |
| HTTP/WS Gateway | `transport.NewGateway(host:gatewayPort)` | `/health` `/metrics` `/ws` `/app/*`；`SetRPCServer(server)` 复用同一套 RPC dispatch；`SetRateLimit` 配置单连接限流 |
| Channel Manager | `newChannelManager(config, loader)` | 可选；仅配置了 `enabled=true` 的通道才启动（详见 [channel.md](channel.md)） |

**run 级工具不在基础注册表**：任务工具（`task_*`）和 `note_save` 由 `transport.runToolInvoker` 按 run 动态派生；`spawn_agent`/`agent_result` 同样按当前 runID 重注册。

## 启动与优雅关闭

`App.Run` 启动唯一入口：网关的错误经 `errCh` 返回；配置了启用通道时额外启动 Channel Manager：

```go
if cm := newChannelManager(a.config, subagentLoader); cm != nil {
    go func() { _ = cm.Start(ctx) }()          // 可选：IM 通道
}
errCh := make(chan error, 1)
go func() { errCh <- gateway.Run(ctx) }()      // HTTP/WS 7438
select {
case err := <-errCh: // 网关出错即退出
case <-ctx.Done():   // 信号取消，优雅关闭
}
```

监听 SIGINT/SIGTERM → 取消 context → 各组件优雅关闭（网关 `Shutdown` + `waitConns` 排空 in-flight 请求、Channel Disconnect、关 MCP 子进程、刷 trace）。
