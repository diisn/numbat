# WebUI 前端对接契约（webui-api-contract）

> WebUI（React + TypeScript）与 numbat-core 网关之间的**事实契约**。
> 权威来源为代码；本文基于 2026-09-08 代码状态整理、2026-09-12 随冒烟测试（20/20 通过，含错误路径）复核，若与代码冲突以代码为准：
> [transport/server.go](../internal/transport/server.go)、[transport/handler.go](../internal/transport/handler.go)、[transport/websocket.go](../internal/transport/websocket.go)、[transport/gateway.go](../internal/transport/gateway.go)、[events/events.go](../internal/events/events.go)、[bus/envelope.go](../internal/bus/envelope.go)、[llm/message.go](../internal/llm/message.go)。
> 参考客户端实现：[tui/client.go](../internal/tui/client.go)（WebSocket 版，与浏览器走同一协议）。

---

## 1. 传输与连接

| 项 | 值 |
|---|---|
| WebSocket 端点 | `ws://<host>:7438/ws`（端口由 config `GatewayPort` 决定，默认 7438） |
| HTTP 辅助路由 | `GET /health`（`{"status":"ok"}`）、`GET /metrics`（uptime/goroutines/subscribers） |
| 子协议 | JSON-RPC 2.0（`bus.Envelope`），每条 **WS text message = 一个 envelope** |
| 鉴权 | 无（本地优先设计）；Origin 检查默认放行，可经 `SetAllowedOrigins` 收紧 |
| 限流 | 可选令牌桶（每秒消息数 + burst）；超限连接被以 close code 1008 关闭 |
| 单消息上限 | 1 MB |
| 保活 | 服务端每 30s 发协议级 ping；60s 读超时。浏览器自动回 pong，前端无需应用层心跳 |
| 慢消费者 | 订阅事件缓冲 256 条；溢出则服务端**主动断开连接**（前端必须跟得上事件流） |

### 1.1 Envelope 格式

```jsonc
// 请求（client → server）
{"jsonrpc":"2.0","id":1,"method":"session.send_message","params":{"session_id":"...","content":"..."}}

// 响应（server → client，按 id 匹配，可乱序到达）
{"jsonrpc":"2.0","id":1,"result":{...}}
{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"..."}}

// 事件（server → client，无 id 字段 —— 以此区分响应与事件）
{"jsonrpc":"2.0","result":{"type":"run.started","run_id":"...","goal":"..."}}
```

**区分规则（与 TUI client 相同）**：`id != null` 的是 RPC 响应；`id` 缺失的是事件（事件负载固定带 `type` 字段 = 点分 topic 名）。

### 1.2 错误码

| code | 含义 |
|---|---|
| -32700 | parse error |
| -32601 | method not found |
| -32602 | invalid params |
| -32603 | handler 内部错误（message 为原始 err.Error()，无结构化 data） |

### 1.3 连接行为要点（前端必读）

1. **每个请求独立 goroutine 处理，响应乱序返回**。`session.send_message` 会阻塞到整个 run 结束才返回；期间 `permission.respond`、`session.list` 等照常可用。前端不能把 send_message 的 RPC 响应当作主要反馈通道——**实时 UI 完全由事件流驱动**，RPC 响应只作终态确认/错误上报。RPC 超时建议设长（TUI 默认 10 分钟）。
2. **event.subscribe 每连接订阅一次即可**。重复订阅已被安全处理（旧订阅的事件通道关闭、writeLoop 退出但不关连接），但无需重复订阅。
3. **回放（replay_from_run）事件保证先于实时事件**；但 subscribe 的 RPC 响应与回放事件之间的先后顺序不保证，不要依赖。
4. 断线重连是全新连接：需要重新 event.subscribe；未完成 run 的 token 流不可恢复（llm.token 不落 trace），但可按已知 run_id 回放工具/权限类事件（见 §5 F5）。

---

## 2. RPC 方法清单

### core.ping
- params：无。result：`"pong"`。用途：连接健康检查。

### agent.run
- params：`{"goal": string}`。result：`{"run_id","status","result"}`。
- 语义：无会话的一次性 run（不落会话历史）。WebUI V1 主要走 session.send_message，此方法备用。

### agent.abort
- params：`{"run_id": string}`（必填）。result：`boolean`（是否命中活动 run）。
- 语义：按 runID 取消运行中的 run；被取消的 run 会正常发出 `run.finished`。

### permission.respond
- params：`{"tool_use_id": string, "decision": string}`（均必填）。result：`boolean`。
- `decision` 取值：`allow_once` | `deny_once` | `always_allow` | `always_deny`（后两者持久化到 policy）。
- 语义：响应 `permission.requested` 事件；对已过期/不存在的 tool_use_id 返回 `false`。

### session.create
- params：`{"mode": "chat"|"one_shot"（非法值回退 chat）, "title": string}`。result：Session 对象。

### session.send_message
- params：`{"session_id": string, "content": string}`。result：`{"run_id","status","result"}`。
- 语义：**阻塞至本 run 完成**；期间事件流推送全部过程。会话关闭时报错 `session is closed`。
- 内容以 `/` 开头触发 skill 解析（`/skill_name args`），并发出 `skill.invoked`；WebUI 自身的斜杠命令若不想透传给后端，需在发送前剥离前缀。
- 副作用：历史超阈值时先自动压缩（发 `context.compacted`）；run 结束后追加 assistant 消息、更新 session 状态（chat→waiting_for_input，one_shot→closed）。
- **同一会话的消息必须串行发送**（前端自律；后端无强制锁，并发发送会交错破坏历史）。

### session.get_history
- params：`{"session_id": string}`。result：`[]Message`（见 §4.1）。

### session.list
- params：无。result：`[]Session`（全量，无分页）。
- 会话增删现经 `session.created` / `session.closed` 事件通知；本方法用于初始加载与按需刷新。

### session.clear
- params：`{"session_id": string}`。result：`"cleared"`。清空历史（保留会话）。

### session.close
- params：`{"session_id": string}`。result：`"closed"`。

### session.compact
- params：`{"session_id": string}`。result：`{"original_tokens","summary_tokens"}`。手动压缩（调用 LLM，耗时）。

### run.list
- params：`{"limit"?: number}`（省略或 ≤0 时用默认 30）。result：`{"runs": []RunSummary, "total": number}`。
- 数据源：聚合 `<runsDir>/<run_id>/events.jsonl`（trace）。按 `started_at` 倒序。
- 子 Agent 不单独成条：`subagent.started` 中 `run_id` 等于本 run 的，合并进父 run 的 `subagents`。
- `RunSummary`：
```jsonc
{ "run_id","goal","status"("running"|"success"|"failed"),
  "started_at","finished_at","duration_ms",        // finished_at 为空串表示未结束
  "skill"?,                                        // 该 run 内 skill.invoked
  "tokens":{"input_tokens","output_tokens","context_pct"},  // 最后一次 llm.usage
  "tools":[{"tool_name","tool_use_id","status","elapsed_ms"}],
  "subagents":[{"run_id","description","status"}],
  "events":[{"type","ts","detail"}] }              // 最多 100 条
```
- 注意：`tokens` 依赖 `llm.usage` 落盘；该事件此前不在 trace 中，故早于本次改动的历史 run 会显示 0。

### event.subscribe（连接级，非 handlers 表）
- params：`{"topics": []string, "scope": "global"|"run:<run_id>", "replay_from_run": string}`（均可省略）。
  - `topics`：点分 topic 的 fnmatch/glob 模式（如 `["tool.*","permission.*"]`）；空 = 全部。
  - `scope`：`global`（默认）收全部；`run:<id>` 只收该 run 的事件（按事件内 `run_id` 字段过滤）。
  - `replay_from_run`：非空时先回放该 run 的 `events.jsonl` 中命中 topics 的历史事件（llm.token/llm.usage/session.* 不落盘，不会出现在回放中）。
- result：`{"subscription_id","replayed_count"}`。

### Session 对象
```jsonc
{ "id","mode"("chat"|"one_shot"),"status"("active"|"waiting_for_input"|"closed"),
  "title","created_at","updated_at","run_ids":[] }
```
（注意：`run_ids` 在 run **结束后**才追加；进行中的 run 不在列表内。）

---

## 3. 事件清单

事件负载 = 结构体字段 + 注入的 `type` 字段（topic 名）。多数事件带 `run_id`。

| type | 字段 | 前端用途 |
|---|---|---|
| `run.started` | run_id, goal | 开启 run 消息组 |
| `run.finished` | run_id, status, result, reason, steps | 收口 run 组；终态。`reason` ∈ llm_error/cancelled/exceeded_max_steps，成功时为空串 |
| `llm.token` | run_id, token | 流式追加文本 |
| `llm.usage` | run_id, input_tokens, output_tokens, context_pct, cache_read_input_tokens, cache_creation_input_tokens | 用量显示（不落盘，不回放）；cache 字段端点未返回时为 0 |
| `llm.request` | run_id, messages, system | 调试视图（体积大，可用 topics 过滤掉） |
| `llm.response` | run_id, text | 本轮完整文本（可校准/替换累计 token） |
| `tool.call_started` | run_id, tool_use_id, tool_name, params | 工具卡片（运行中） |
| `tool.call_finished` | run_id, tool_use_id, tool_name, output, elapsed_ms | 工具卡片（完成） |
| `tool.call_failed` | run_id, tool_use_id, tool_name, error, error_type, elapsed_ms, attempt | 工具卡片（失败）；`attempt` 从 1 起，重试可观测 |
| `permission.requested` | run_id, tool_use_id, tool_name, params, preview, session_id | 审批卡片（待决） |
| `permission.granted` | run_id, tool_use_id, tool_name, decision | 审批卡片（通过）。**仅人工决策时发射**，auto_allow 不发 |
| `permission.denied` | run_id, tool_use_id, tool_name, decision | 审批卡片（拒绝）。auto_deny 不发 |
| `context.compacted` | run_id, session_id, original_tokens, summary_tokens | 系统提示条 |
| `subagent.started` | run_id, parent_run_id, description | 嵌套 run 组 |
| `subagent.finished` | run_id, parent_run_id, status | 嵌套 run 组收口 |
| `skill.invoked` | run_id, skill_name, arguments | 用户消息 skill 徽标 |
| `session.created` / `session.closed` | session_id[, mode] | 会话增删（现已广播，驱动侧栏刷新） |

---

## 4. 事件流 → 消息模型（parts[]）归并规则

前端统一消息模型（一行 = 一个 Part），历史（get_history）与实时（事件流）归并到**同一形状**：

```ts
type Part =
  | { type: "text";     text: string }
  | { type: "thinking"; text: string }
  | { type: "tool_use"; id: string;            // tool_use_id
      name: string; input: object;
      state: "running" | "done" | "failed";
      output?: string; error?: string; errorType?: string; elapsedMs?: number;
      permission?: "pending" | "granted" | "denied" }

type RunGroup = {
  runId: string; goal: string; status: "running" | string; result?: string;
  parts: Part[];                       // 按到达顺序追加
  usage?: { inputTokens: number; outputTokens: number; contextPct: number }
}
```

### 4.1 历史归并（get_history → parts）

`Message = {role, content: ContentBlock[]}`，`ContentBlock.type`：

| ContentBlock | 映射 |
|---|---|
| `text` | text part |
| `thinking`（thinking/signature） | thinking part |
| `tool_use`（id/name/input） | tool_use part（state: done，permission 未知置 undefined） |
| `tool_result`（tool_use_id/content/is_error） | 按 `tool_use_id` 并入对应 tool_use part 的 output/state |

> 历史加载没有 `tool.call_*` 事件流：tool 终态由 `tool_result` 块反推（`is_error` → failed，否则 success；实现见 `webui/src/App.tsx` 的 `deriveToolStates`，首连恢复与侧栏切换两条路径均已接入）。无 `tool_result` 的孤立 `tool_use`（被中止的 run 留下）无终态数据，保持「运行中」显示——刻意行为。

### 4.2 实时归并（事件 → parts）

| 事件 | 动作 |
|---|---|
| `run.started` | 新建 RunGroup（status: running） |
| `llm.token` | 追加到当前 text part（无则新建） |
| `llm.response` | 收口当前 text part（可用全文替换累计值） |
| `tool.call_started` | 追加 tool_use part（state: running） |
| `tool.call_finished` / `failed` | 按 tool_use_id 更新 state/output/error/elapsedMs |
| `permission.requested` | 按 tool_use_id 置 permission: pending（渲染审批卡片） |
| `permission.granted` / `.denied` | 更新 permission |
| `llm.usage` | 写入 RunGroup.usage |
| `context.compacted` | 插入系统提示条（独立渲染，非 part） |
| `subagent.started` / `.finished` | RunGroup 嵌套子组 |
| `skill.invoked` | 对应 goal 的用户消息打 skill 徽标 |
| `run.finished` | RunGroup 收口（status/result）；随后 RPC 响应到达可作二次确认 |

---

## 5. 已知缺陷与后端修复状态

### 已修复（本次）

| # | 问题 | 修复 |
|---|---|---|
| F1 | 订阅连接上每条 envelope 后跟一条 `"\n"` 独立 text message（NDJSON 遗留） | `subscriber.write` 对 `websocketConn` 跳过换行写入 |
| F2 | `session.created/closed` 已发布但未被 transport 订阅，永不广播 | `SetBus` 补订阅这两个事件 |
| F3 | 同连接重复 `event.subscribe` 导致旧订阅 goroutine 泄漏 | `registerSubscriber` 安全替换旧订阅（`detached` 标志，writeLoop 退出时不关连接） |
| F4 | `/app/*` 静态资源路由未实现 | `internal/webui/embed.go`（go:embed dist）+ gateway `/app/*`，SPA fallback 到 index.html，assets 长缓存 |
| F5 | 断线重连后无法发现 in-flight run（`session.run_ids` 只记已完成的） | 新增 `run.list`（聚合 trace 得 `RunSummary`）；`llm.usage` 已补入 trace |
| — | tool_use 块缺 `input` 字段导致上游 400 `missing field 'input'` | `ContentBlock.MarshalJSON` 对 tool_use 始终输出 `input`（nil → `{}`） |

### 待处理

| # | 问题 | 前端绕行 | 建议后端修复 |
|---|---|---|---|
| F6 | `session.send_message` 阻塞整个 run | 事件流驱动 UI；RPC 超时 ≥10 分钟 | —（设计如此，非缺陷） |

---

## 6. 前端实现约束（强制）

1. **单一 WS client 模块**持有全部协议知识（envelope 编解码、id 匹配、事件分发、重连）；UI 组件只消费归并后的 parts 模型。
2. 连接生命周期：connect → `event.subscribe`（一次）→ 正常工作 → 断开后指数退避重连并重新订阅；重连后刷新 `session.list` + 当前会话 `get_history`。
3. 同一会话内串行 `session.send_message`（上一 run 结束前禁用发送按钮或本地排队）。
4. run 进行中常驻显示 abort 入口（`agent.abort`）。
