# internal/channel — 外部 IM 通道接入 + 消息路由

`internal/channel` 把不同 IM 平台（飞书、Telegram）抽象为统一 Channel，由 Manager 调度到 Router 选出的 Agent；`channel` 包不依赖 loop/llm/transport，Agent 执行以注入回调承接（模块间解耦）。

## Channel 接口（channel.go）

```go
type Channel interface {
    Name() string                            // 通道名，全局唯一
    Connect(ctx context.Context) error        // 建立连接；ctx 取消时断开退出
    Disconnect() error                        // 断开并关闭 Receive() 通道
    Send(ctx context.Context, msg OutboundMessage) error
    Receive() <-chan InboundMessage           // 入站消息流
    Status() ChannelStatus                    // connected | disconnected | error
}
```

| 消息 | 字段 | 说明 |
|------|------|------|
| `InboundMessage` | `SenderID` / `ChannelName` / `Content` / `MsgID` | 平台消息转换后的统一入站消息；`MsgID` 供去重 |
| `OutboundMessage` | `RecipientID` / `Content` | 统一出站消息，`RecipientID` 与 `SenderID` 对应 |

适配器内部职责：事件去重（按 MsgID）、平台消息 ⇄ `Inbound/OutboundMessage` 格式转换。

## Router（router.go）

按优先级为 `(channelName, senderID)` 选择目标 Agent：

1. 发送者 ID 精确匹配（`by_sender`）
2. 通道名匹配（`by_channel`）
3. 默认 Agent（`default_agent`）

某条规则命中的 Agent 名解析失败时逐级回退；全部失败返回 nil（由 Manager 拒绝消息）。`Resolver`（`func(name) (*AgentConfig, bool)`）由组装层注入（app 中基于 `agents.Loader`），Agent 配置含 `Name/Model/SystemPrompt/AllowedTools`。

## Manager（manager.go）

生命周期与消息调度：

- **Register**：注册 Channel，重名拒绝。
- **Start(ctx)**：逐通道 Connect 并启动独立读循环 goroutine；阻塞至 ctx 取消 → Disconnect 全部 → 等读循环退出。无成功连接通道时跳过。
- **读循环**（每 Channel 一个）：读 `Receive()`，补全 `ChannelName`，同步投递到 per-sender 串行队列（队列满形成背压，不丢消息）。
- **per-sender 串行队列**（每 SenderID 一个 worker）：保证同一发送者的消息顺序执行、不会并发触发多个 Agent 运行导致会话冲突。
- **process 处理链**：`Router.Route` 选 Agent → `MessageHandler` 回调执行（组装层注入，返回回发文本）→ 经原 `Channel.Send` 回发。无 Agent 命中丢弃，handler 出错记日志不回复，空输出不回发。

## 适配器

### Telegram（telegram.go）— Bot API 长轮询

- 纯 `net/http` + `encoding/json`，无外部 SDK。
- `Connect` 校验 name/token 后启动 `pollLoop` goroutine；`getUpdates?offset=N&timeout=30` 长轮询。
- **offset 推进**：按已返回 `update_id` 递增，保证不重不漏。
- 只投递带文本的消息（图片/编辑等本期忽略）；私聊 `chat.id` 即 `SenderID`，回发时作 `RecipientID`。
- `telegramAPI` 接口抽象网络层（`httpTelegramAPI` 真实实现），便于测试注入 mock。

### 飞书（feishu.go）— 骨架

- `Connect` 校验 `app_id`/`app_secret` 非空后启动占位接收循环。
- TODO：接入飞书官方 SDK WebSocket 长连接、换取 tenant_access_token、`im/v1/messages` 发送。
- 未引入任何 SDK，保持可编译。

## 配置（config.go）

```toml
[gateway]  # 无；gateway_port 见 config.md
[[channels]]
name    = "telegram-main"
type    = "telegram"   # feishu | telegram
enabled = true
token   = "..."        # telegram: bot token
# app_id / app_secret   # feishu 用

[routing]
default_agent = "executor"
[routing.by_sender]
"123456" = "planner"
[routing.by_channel]
"telegram-main" = "executor"
```

`app.go` 中 `newChannelManager`：仅配置了 `enabled=true` 的通道才组装适配器并启动；缺省空配置返回 nil，不影响既有 TCP/WS 行为。Agent 名称经 `agents.Loader` 解析为完整配置注入 Router。

> 当前 `MessageHandler` 为占位实现（TODO：接入 `agent.run` / `session.send_message` 执行链，按 `AgentConfig` 覆盖 model/system_prompt/工具白名单）。
