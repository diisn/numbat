# AGENTS.md — 前端开发行为准则

## 核心原则：契约驱动 + Mock 先行

前端在真正联调完成之前，一律使用 Mock 数据独立开发与测试。前后端分离开发，互不阻塞。

但——**Mock 不是随意编造**。Mock 必须忠于后端真实契约（`docs/webui-api-contract.md` + `src/types/api.ts`），这样联调时只需替换数据源（MockClient → WsClient），UI 逻辑零改动。

## 行为准则

### 1. Mock 必须忠于契约

- Mock 数据的字段名、类型、嵌套结构必须与 `src/types/api.ts` 完全一致
- `src/types/api.ts` 是事实来源，它镜像了 Go struct（`internal/transport/handler.go` / `internal/events/events.go` / `internal/session/model.go` / `internal/llm/message.go`）
- 如果对某个字段不确定，去读 Go 源码，不要猜

### 2. 事件流必须模拟真实顺序

Mock 不仅要返回 RPC 结果，还要按真实事件流顺序发射事件：

```
run.started → llm.token*（流式） → llm.response → [tool.call_started → tool.call_finished]* → run.finished
```

每个事件都必须带正确的 `run_id` 和字段。UI 的 parts[] 合并逻辑依赖这个顺序。

### 3. 接口一致：Mock 与 WsClient 实现相同接口

- `MockClient` 与 `WsClient` 暴露相同的公开方法：`connect()` / `disconnect()` / `getStatus()` / `onEvent()` / `onStatus()` / `call()`
- 切换只需改 `src/lib/client.ts` 的工厂函数，UI 代码零改动
- `src/lib/client.ts` 的 `USE_MOCK` 常量是唯一的切换开关

### 4. 开发 Mock 时必须查阅后端实际能力

写 Mock 前，心里要有谱——后端能提供什么：

| 能力 | 后端实现位置 | Mock 对应 |
|------|-------------|-----------|
| RPC 方法 | `internal/transport/handler.go` | MockClient.handleCall 的 switch 分支 |
| 事件类型 | `internal/events/events.go` | MockClient.emit 的 Event 子类型 |
| 会话模型 | `internal/session/model.go` | Mock 返回的 Session 对象 |
| 消息结构 | `internal/llm/message.go` | Mock 返回的 ContentBlock[] |
| 传输层 | `internal/transport/gateway.go` | 不需要 Mock（Vite proxy 直通） |

### 5. 不造后端没有的能力

- 如果后端没有 `run.list`，Mock 也不要提供
- 如果后端的 `session.create` 返回完整 Session，Mock 也必须返回完整 Session
- 如果后端的 `session.send_message` 会异步发射事件流，Mock 也必须模拟这个异步过程
- 宁可 Mock 欠缺功能，也不要超前造后端不存在的接口

### 6. 联调切换路径

当后端准备好时，切换步骤：
1. 启动 numbat-core（`.\bin\numbat-core.exe`）
2. 将 `src/lib/client.ts` 的 `USE_MOCK` 改为 `false`
3. UI 代码不做任何改动
4. 如果联调发现问题，优先检查契约差异（Mock 行为 vs 后端实际行为），而非改 UI

### 7. 教训：每轮改完 Go 代码必须重建再启动

- Windows 下运行中的 exe 被锁定，必须先停进程再 `go build -o .\bin\numbat-core.exe .\cmd\numbat-core`，然后重新启动
- 直接重启旧 exe 会跑旧逻辑：曾因二进制比源码旧 20 小时，冒烟观测到的行为（`run.finished.reason` 消失、send_message 误抛 RPC error）与代码不符，排查被严重误导

## 文件职责

| 文件 | 职责 |
|------|------|
| `src/types/api.ts` | 事实来源：TS 类型镜像 Go struct |
| `src/lib/ws-client.ts` | 真实协议边界：WebSocket + JSON-RPC |
| `src/lib/mock-client.ts` | Mock 协议边界：模拟 RPC + 事件流 |
| `src/lib/client.ts` | 工厂：USE_MOCK 决定用哪个 |
| `src/App.tsx` | UI：只消费 client 的公开接口 |
| `docs/webui-api-contract.md` | 契约文档（注意：字段表可能有滞后，以 api.ts 和 Go 源码为准）|
| `docs/webui-prd.md` | 完整 PRD（M1/M2/M3）|
