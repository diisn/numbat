# Numbat Code Wiki

> Go 语言重写的 AI 智能体：双进程架构（numbat-core 守护进程 + numbat-tui 交互客户端）+ 统一 HTTP/WebSocket 网关（JSON-RPC 2.0）+ ReAct 循环。
>
> **阅读建议**：先读本页的架构总览和调用链，再按需点开 [模块文档](#模块索引) 深入。

---

## 1. 项目概览

| 维度 | 说明 |
|---|---|
| 语言 | Go 1.26.2 |
| 进程模型 | 双进程：`numbat-core`（守护进程）+ `numbat-tui`（TUI 客户端） |
| RPC 协议 | JSON-RPC 2.0，统一 WebSocket 传输（TUI 与 WebUI 同经 `/ws`） |
| 对外入口 | HTTP/WS 网关 :7438（`Gateway`，`/health` `/metrics` `/ws` `/app/*`） |
| IM 接入 | `internal/channel` 可选通道（Telegram/飞书）+ 按发送者路由到 Agent |
| 事件驱动 | 内存事件总线 + 订阅广播（WebSocket 连接共用） |
| LLM 接入 | 自研 Anthropic Messages API 兼容 client（SSE 流式） |
| 依赖 | bubbletea/bubbles/lipgloss/glamour、gin-gonic/gin、gorilla/websocket、BurntSushi/toml、chroma、slog |
| WebUI | React + TypeScript + Vite，生产产物经 go:embed 内嵌，挂在网关 `/app/*` |

---

## 2. 整体架构

```text
 TUI                        浏览器(WebUI)                     IM 用户(可选)
        │                            │                             │
   WebSocket :7438           HTTP/WS 网关 :7438              channel 适配器
  JSON-RPC 2.0             /health /metrics /ws /app       (Telegram/飞书)
        │                            │                             │
        │                    WebSocket 升级 / 静态资源             │
        ▼                            ▼                             │
  ┌─────────────────────────────────────┴───────────┐◄────────────┘
  │            transport.Server (JSON-RPC 2.0)      │  (MessageHandler 注入执行)
  │        dispatch / 事件订阅广播 / run 取消        │
  └──────────────────────────┬──────────────────────┘
                             ▼
                    cmd/numbat-core (internal/app 组装)
                             │
        ┌───────────┬────────┼────────┬───────────┬─────────────┐
        │           │        │        │           │             │
  internal/      internal/ internal/ internal/  internal/    internal/
  transport      loop     events   session    subagent      mcp
  (RPC server +  (ReAct   (事件总线) (会话管理)  (子 Agent)    (外部工具)
   gateway/WS)   主循环)
        │           │        │        │           │             │
        │   internal/llm (Anthropic Provider, SSE 流式)           │
        │   internal/tools (工具注册表 + Invoker)                 │
        │   internal/permissions (策略 + 审批)                    │
        │   internal/compact / context / memory / skills          │
        │   internal/trace (事件落盘) + webui (内嵌前端)           │
        └───────────┴────────┴────────┴───────────┴─────────────┘
```

### 一次完整请求的调用链（TUI 发消息为例）

1. TUI `tui.Client` 发送 `session.send_message` RPC → WebSocket `/ws`（:7438）
2. `transport.Server.handleWSConn` 为每连接分配 goroutine，每请求再分配独立 goroutine（WaitGroup 防泄露）
3. `transport.handler` 处理：读历史 → 自动压缩 → 追加用户消息 → 解析 `/skill` → 构造 run 级工具 → 注册 run cancel → 发布 `RunStarted`
4. `loop.AgentLoop.Run` 执行 ReAct：`provider.Chat`（SSE 流式）→ 并行 `Invoker`（权限→执行→重试）→ 回填 `tool_result` → 循环到 `end_turn`
5. 事件经 `events.Bus` 广播：transport 转发给订阅者（WS），trace 落盘；运行中可经 `agent.abort` 取消

### IM 通道消息调用链（可选）

IM 消息 → Channel 适配器 `Receive()` → `channel.Manager` 按发送者入串行队列 → `Router.Route` 选 Agent → `MessageHandler` 执行（占位，TODO 接入 agent 流程）→ 原 `Channel.Send` 回发。

---

## 3. 关键设计决策

| 决策 | 位置 | 说明 |
|---|---|---|
| 并发工具执行 | loop | 同一轮多个 `tool_use` 用 `sync.WaitGroup` 并行，结果按下标回填 |
| 请求并发 | transport | 每请求独立 goroutine，`permission.respond` 不被 `agent.run` 阻塞 |
| per-connection 写锁 | transport | `websocketConn` 自带 `writeMu` 串行化并发写 |
| connWriter 抽象 | transport | 最小连接接口让 subscriber/事件广播与传输载体解耦 |
| WebSocket 统一入口 | transport | TUI 与浏览器同经 `/ws` 走同一 `handleRequest`，一套 RPC 逻辑 |
| 网关单入口 | transport/app | HTTP/WS :7438 唯一入口；`agent.abort` 按 run 取消 |
| per-sender 串行队列 | channel | 同 SenderID 消息单 worker 顺序执行，防会话冲突 |
| 通道路由优先级 | channel | by_sender > by_channel > default_agent，逐级回退 |
| 通道默认不启用 | channel/app | `enabled=true` 才启动，缺省空配置不影响既有行为 |
| session store 文件锁 | session | per-session `sync.Mutex` 保护文件读写，防并发追加交错 |
| handleWSConn WaitGroup | transport | 连接退出前 `wg.Wait()` 等所有 in-flight 请求，防 goroutine 泄露 |
| Bus.Publish 防 panic | events | 单 handler panic 不中断后续 handler，recover 后记录错误继续 |
| 自动压缩机制 | compact/handler | 两种：**手动** `session.compact`；**自动** run 中途按 `auto_compact_threshold`（context_pct 阈值，缺省 0 禁用）触发，压缩后落盘摘要 |
| 工具超时与重试 | tools/invoker | `timeout<=0` 不设超时；`runtime_error`/`rate_limited` 指数退避重试 |
| 权限正则预编译 | permissions | 6 条 outside-cwd 正则在 `init` 时编译一次，运行时直接匹配 |
| trace 单次序列化 | trace | 事件只做 1 次 JSON marshal（含 ts+type），非 3 次 |
| 后台子 Agent 独立 | subagent | 后台模式用 detached context，不随父 ctx 取消终止 |
| TUI 模式枚举 | numbat-tui | `mode` 枚举（input/browse/perm/slash/help）替代布尔标志 |
| TUI viewport 滚动 | numbat-tui | `bubbles/viewport` 全功能滚动，智能跟随底部 |
| 配置可提取 | config | MaxTokens/ContextWindow/CompactThreshold/GatewayPort/Channels 从硬编码提取到 config |

---

## 4. 包依赖关系

```text
app ──> config, events, llm, tools(+builtin), permissions, session, subagent,
        mcp, trace, transport, agents, channel
transport ──> bus, events, llm, loop, context, memory, permissions, session,
              skills, subagent, task, tools(+builtin), compact, agents, gin, websocket
channel ──> （仅标准库 + slog；Agent 执行经注入回调承接，不依赖 loop/llm/transport）
loop ──> llm, tools, events, compact, context
llm ──> （仅标准库，不依赖 events；流式回调由调用方注入）
```

---

## 5. 数据布局（~/.numbat/）

```text
~/.numbat/
├── config.toml          # 全局配置
├── context.md           # 全局记忆
├── skills/              # 用户 Skill
├── agents/              # 用户 Agent Profile
├── policy.toml          # 权限持久化（always_allow / always_deny）
├── traces/daemon.jsonl  # 全局三层 trace（IPC/Event/LLM）
├── sessions/<sid>/
│   ├── meta.json        # 会话元数据（含 run_ids）
│   ├── thread.jsonl     # 消息历史
│   └── notes.md         # 会话笔记
└── runs/<runID>/
    ├── events.jsonl     # trace 事件轨迹
    └── .tasks/          # per-run 任务文件
```

---

## 模块索引

| 文档 | 包 | 职责 |
|------|---|------|
| [getting-started.md](docs/getting-started.md) | — | 配置、启动、TUI 操作 |
| [app.md](docs/app.md) | internal/app | 组件组装中心 |
| [tui.md](docs/tui.md) | cmd/numbat-tui | TUI 客户端（6 文件结构） |
| [transport.md](docs/transport.md) | internal/transport | RPC dispatch 核心 + HTTP/WS 网关 |
| [channel.md](docs/channel.md) | internal/channel | 外部 IM 通道接入 + 消息路由 |
| [bus.md](docs/bus.md) | internal/bus | JSON-RPC Envelope 定义 |
| [events.md](docs/events.md) | internal/events | 事件总线 + 事件类型 |
| [llm.md](docs/llm.md) | internal/llm | Provider 接口 + AnthropicProvider |
| [loop.md](docs/loop.md) | internal/loop | ReAct 主循环 |
| [tools.md](docs/tools.md) | internal/tools | 工具接口 + 注册表 + Invoker |
| [permissions.md](docs/permissions.md) | internal/permissions | 策略评估 + 审批 |
| [session.md](docs/session.md) | internal/session | 会话模型 + 存储 |
| [compact.md](docs/compact.md) | internal/compact | 上下文压缩 + 截断 |
| [context.md](docs/context.md) | internal/context | 执行上下文 |
| [skills.md](docs/skills.md) | internal/skills | Skill 加载器 |
| [subagent.md](docs/subagent.md) | internal/subagent | 子 Agent |
| [agents.md](docs/agents.md) | internal/agents | Agent Profile |
| [task.md](docs/task.md) | internal/task | 任务管理 |
| [mcp.md](docs/mcp.md) | internal/mcp | MCP 客户端 |
| [trace.md](docs/trace.md) | internal/trace | 事件落盘 |
| [config.md](docs/config.md) | internal/config | 配置加载 |
| [memory.md](docs/memory.md) | internal/memory | 记忆层 |
| [webui-api-contract.md](docs/webui-api-contract.md) | — | WebUI 与后端的 RPC 契约 |
| [webui-prd.md](docs/webui-prd.md) | — | WebUI 产品需求 |

---

*文档基于 2026-09-13 代码状态（Numbat 更名后：WebUI 实装、网关 /app、autocompact 配置化）。*
