# internal/transport — RPC 服务器（TCP/WebSocket）+ HTTP 网关

`internal/transport` 提供两层对外接口：

- **RPC 服务器（server.go）**：JSON-RPC 2.0 dispatch，可同时承载 TCP + NDJSON 与 WebSocket 两种传输（经 `connWriter` 抽象复用同一套 dispatch/事件订阅逻辑）。
- **HTTP 网关（gateway.go）**：对外 HTTP 入口（`/health`、`/metrics`、`/ws`），`/app/*` 服务 go:embed 内嵌的 WebUI 产物（SPA fallback，带哈希资源长缓存）。

## TCP 服务器（server.go）

### 并发模型
- `Run` 监听 TCP，每连接一个 `handleConn` goroutine
- 每请求再分配独立 goroutine，响应按 ID 乱序写回
- **per-connection 写锁**：非订阅连接每连接一把 `sync.Mutex`，防止并发写破坏 NDJSON 帧
- **WaitGroup 防 goroutine 泄露**：连接退出前 `wg.Wait()` 等待所有 in-flight 请求完成
- **优雅关闭**：关闭监听 → 等 in-flight 请求 → 关 MCP 子进程 → 刷 trace

### connWriter 抽象
`connWriter`（`Write`/`Close`/`SetWriteDeadline`）是事件订阅与 RPC 响应所需的最小连接抽象：

- `net.Conn`（TCP）天然满足；
- WebSocket 经 `websocketConn` 包装后满足；
- 使 `subscriber`、`broadcastEvent`、`handleRequest`、`writeEnvelope` 等与传输载体解耦。

## HTTP 网关（gateway.go）

- 基于 `gin`（HTTP 路由）+ `gorilla/websocket`（WS 升级），默认端口 7438（`config.GatewayPort`）。
- 路由：
  - `GET /health` → `{"status":"ok"}`
  - `GET /metrics` → uptime / goroutines / 订阅数
  - `GET /ws` → WebSocket 升级入口（未配置 RPC server 时返回 503）
  - `/app`、`/app/*` → WebUI 静态托管（`gin.WrapH` 包装标准 handler，SPA 回退到 index.html）
- `http.Server.WriteTimeout` 置零，避免影响 `/ws` 长连接。
- `SetRPCServer`：把 JSON-RPC dispatch 挂到网关。
- `SetAllowedOrigins`：`Upgrader.CheckOrigin` 白名单，空列表 = 本地默认放行。
- `SetRateLimit`：单连接令牌桶限流（rate 每秒消息数、burst 突发上限；rate<=0 禁用）。

## WebSocket 传输（websocket.go）

- `handleWSConn`：读取消息 → `handleRequest` dispatch → `writeEnvelope` 响应，与 TCP 完全同构。
- **连接保活**：30s 一次 Ping，60s 读超时 + Pong handler 续期。
- **限流**：读循环入口 `rateLimiter.allow()`，超限发送 Close(1008) 并断开。
- 复用 TCP 的 `subscriber`/`writeLoop`/`broadcastEvent`：事件订阅、topic/scope 过滤、慢消费者断开在 WebSocket 上同样生效（单条消息 = 单个 TextMessage，天然成帧，无 NDJSON `\n` 拼接）。

## run 级取消（run_cancel.go）

`runCanceler` 维护 `runID -> context.CancelFunc` 注册表，支撑 `agent.abort`：

- `agent.run` / `session.send_message` 启动时 `Register(runID, cancel)`，结束后 `Done(runID)`；
- `agent.abort` 按 `run_id` 调 `Abort` 触发 `cancel()`，中断 ReAct 循环与工具执行。

## RPC handler（handler.go）

| 方法 | 参数 | 返回 | 说明 |
|------|------|------|------|
| `core.ping` | — | `"pong"` | 健康检查 |
| `agent.run` | `{goal}` | `{run_id,status,result}` | 一次性任务（无会话、无 note_save） |
| `agent.abort` | `{run_id}` | `bool` | 按 runID 取消运行（是否命中并触发取消） |
| `permission.respond` | `{tool_use_id, decision}` | `bool` | 审批 |
| `session.create` | `{mode, title}` | Session | mode 非法回退 chat |
| `session.send_message` | `{session_id, content}` | `{run_id,status,result}` | 核心入口 |
| `session.get_history` | `{session_id}` | `[]llm.Message` | |
| `session.list` | — | `[]Session` | 内存 + 磁盘并集，按更新时间倒序 |
| `session.clear` | `{session_id}` | `"cleared"` | 清空消息历史（保留 meta/notes） |
| `session.close` | `{session_id}` | `"closed"` | |
| `session.compact` | `{session_id}` | `{original_tokens,summary_tokens}` | |
| `event.subscribe` | `{topics[], scope, replay_from_run}` | `{subscription_id, replayed_count}` | |

### session.send_message 处理流程
读历史 → 自动压缩（历史 > 6000 字符）→ 追加用户消息 → 解析 `/skill` → 构造 run 级工具注册表 → 注册 run cancel → 发布 `RunStarted` → 调 `loop.Run` → 注销 cancel → 写回 assistant 消息 → 返回

### 事件订阅过滤
- **topic glob**：`path.Match` 匹配（如 `"tool.*"`）；空 topics 匹配全部
- **scope**：`"global"` 全通；`"run:<id>"` 只收该 run
- **replay_from_run**：从 `<runsDir>/<runID>/events.jsonl` 回放历史

## 测试（gateway_test.go）

- HTTP 路由状态码与响应体（/health、/metrics）
- WebSocket 全链路：Dial `/ws` → 发 `core.ping` → 收 JSON-RPC 响应
- 未配置 RPC server 时 `/ws` 返回 503
- `Run`/`Shutdown` 启停

## RPC 客户端（tui/client.go）

TUI 使用的 TCP + NDJSON 客户端，`readLoop` goroutine 持续读行：带 ID 的分发到 `responses[id]` channel（唤醒等待的 `Call`）；无 ID 的推入 `events` channel。

- `CallWithTimeout`：带超时的 RPC 调用，超时返回错误
- 连接断开时给所有等待中的 Call 注入错误响应，避免永久阻塞

