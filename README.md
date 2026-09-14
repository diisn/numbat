# Numbat

<p align="center">
  <img src="https://trae-api-cn.mchost.guru/api/ide/v1/text_to_image?prompt=dark%20developer%20workspace%20with%20a%20glowing%20AI%20agent%20terminal%2C%20floating%20code%20panels%20with%20syntax%20highlighting%2C%20purple%20and%20blue%20neon%20glow%2C%20futuristic%20command%20center%20dashboard%2C%20cinematic%20lighting%2C%20high%20detail%2C%20professional%20product%20banner&image_size=landscape_16_9" alt="Numbat Banner" width="100%"/>
</p>

用 **Go 语言**完全实现的开源 AI 编程智能体，核心是一个**类似 Claude Code 的 Agent Loop**（`loop.AgentLoop`），并配有一层**统一 WebSocket 网关**（HTTP/WS :7438），支持 **TUI / WebUI / IM** 三种交互入口。

- 语言 / 运行时：Go 1.26.2（单一二进制，`go:embed` 内嵌前端产物）
- Agent Loop：类 Claude Code 的「LLM 调用 → 并行工具执行 → 回填结果」迭代循环
- 网关层：TUI 与浏览器经同一 `/ws` 端点复用完全相同的 JSON-RPC 2.0 协议与事件订阅广播
- LLM 接入：自研 Anthropic Messages API 兼容客户端（SSE 流式），可对接 Anthropic / DeepSeek 等
- 数据根目录：`~/.numbat/`

---

## 特性

- **Go 语言实现**：全部核心以 Go 编写，单一守护进程二进制，`go:embed` 内嵌 WebUI 产物，无运行时依赖。
- **类 Claude Code 的 Agent Loop**：`loop.AgentLoop` 迭代「LLM 调用 → 并行工具执行 → 回填结果」，直到 `end_turn`，与 Claude Code 的 agentic loop 行为一致。
- **统一 WebSocket 网关层**：`transport.Gateway`（HTTP/WS :7438）承载全部对外入口——TUI 与浏览器同经 `/ws` 双工通信（JSON-RPC 2.0），另提供 `/health` `/metrics` 与 `/app/*`（内嵌 WebUI）。
- **三种交互入口**：TUI 客户端（WS）、浏览器 WebUI（WS + 静态资源）、外部 IM（Telegram / 飞书，可选）。
- **工具系统**：内建 `read_file` / `list_dir` / `write_file` / `bash`，run 级任务与笔记工具，支持超时、指数退避重试与权限审批。
- **权限与审批**：`always_allow` / `always_deny` 持久化策略 + 运行时审批弹窗（TUI 中 `y/a/n/d`）。
- **会话管理**：基于文件的会话存储（`thread.jsonl` + `meta.json` + `notes.md`），支持手动与自动上下文压缩。
- **Skill 与 Agent Profile**：按「项目 `.numbat` → 用户 `~/.numbat` → 内建」三级优先级加载。
- **子 Agent**：支持嵌套、后台并行任务与独立工具白名单（planner / executor / reviewer）。
- **MCP 接入**：将外部 MCP Server 的工具注册进统一工具注册表。
- **事件驱动 + Trace**：内存事件总线统一广播；`~/.numbat/runs/<runID>/events.jsonl` 与全局三层 trace 落盘。
- **内嵌 WebUI**：React + TypeScript + Vite 前端，经 `go:embed` 打进二进制，挂在网关 `/app/*`。

---

## 架构

<p align="center">
  <img src="https://trae-api-cn.mchost.guru/api/ide/v1/text_to_image?prompt=software%20architecture%20diagram%2C%20layered%20cloud%20infrastructure%2C%20glowing%20nodes%20connected%20by%20lines%2C%20blue%20and%20purple%20network%20visualization%2C%20clean%20minimal%20modern%20tech%20style%2C%20high%20detail&image_size=landscape_16_9" alt="Architecture Illustration" width="100%"/>
</p>

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
  (RPC server +  (Agent   (事件总线) (会话管理)  (子 Agent)    (外部工具)
   gateway/WS)   Loop)
        │           │        │        │           │             │
        │   internal/llm (Anthropic Provider, SSE 流式)           │
        │   internal/tools (工具注册表 + Invoker)                 │
        │   internal/permissions (策略 + 审批)                    │
        │   internal/compact / context / memory / skills          │
        │   internal/trace (事件落盘) + webui (内嵌前端)           │
        └───────────┴────────┴────────┴───────────┴─────────────┘
```

- **Agent Loop（核心）**：`internal/loop` 实现类 Claude Code 的单会话迭代循环——加载历史 → LLM 流式响应 → 并行执行 `tool_use` → 回填 `tool_result` → 循环至 `end_turn`；run 中途可经 `agent.abort` 取消，上下文按阈值自动压缩。
- 对外唯一入口：**HTTP/WS 网关 :7438**（`Gateway`），提供 `/health` `/metrics` `/ws` 与 `/app/*`（内嵌 WebUI）；TUI 与浏览器同经 `/ws` 走 `transport.Server` 的同一套 `dispatch` 与事件订阅，策略（限流/Origin）单点覆盖。

详细的调用链与设计决策见 [CODE_WIKI.md](CODE_WIKI.md)。

---

## 快速开始

### 1. 配置

```bash
# 方式 1：.env 文件
cp .numbat/.env.example .numbat/.env
# 编辑 .numbat/.env 填写 API key

# 方式 2：环境变量（以 DeepSeek 为例）
export NUMBAT_ANTHROPIC_API_KEY="your-key"
export NUMBAT_BASE_URL="https://api.deepseek.com/anthropic"
export NUMBAT_DEFAULT_MODEL="deepseek-chat"
```

配置加载优先级（低 → 高）：默认值 → `~/.numbat/config.toml` → `./.numbat/config.toml` → `numbat_*` 环境变量 → `.numbat/.env`。详见 [config.md](docs/config.md)。

### 2. 构建

```bash
go build ./cmd/numbat-core && go build ./cmd/numbat-tui
```

### 3. 启动

```bash
# 终端 1：守护进程（numbat-core）
go run ./cmd/numbat-core

# 终端 2：TUI 客户端
go run ./cmd/numbat-tui -addr 127.0.0.1:7438
```

快速验证进程存活：`curl http://127.0.0.1:7438/health`。

### 4. 接入 WebUI（可选）

前端源码在 `webui/`（React + TypeScript + Vite），生产产物由 `numbat-core` 经 `go:embed` 内嵌。

```bash
# 生产：构建前端 → 重建二进制 → 访问 http://127.0.0.1:7438/app/
cd webui && npm install && npm run build
cd .. && go build ./cmd/numbat-core

# 开发：Vite dev server（:5173），/ws、/health、/metrics 代理到本地 :7438
cd webui && npm run dev
```

### 5. IM 通道（可选）

配置 `[[channels]] enabled=true`（Telegram / 飞书）后，`numbat-core` 会额外启动 IM 接入，消息按路由规则送达 Agent。详见 [channel.md](docs/channel.md)。

---

## TUI 快捷键

| 输入 | 作用 |
|------|------|
| 普通文本 | 发送消息（首次自动创建会话） |
| `↑` `↓` | 输入历史翻阅 / 浏览模式选择消息 |
| `Enter` | 发送 / 展开·收起工具块 |
| `Esc` | 切换 input ↔ browse 模式 |
| `g` `G` | 跳到消息顶部 / 底部 |
| `PageUp` `PageDown` | 翻页滚动 |
| `[` `]` | 工具输出块逐行滚动 |
| `{` `}` | 工具输出块翻页滚动 |
| `/` | 斜杠命令补全（内建命令 + skill） |
| `y` | 浏览模式下复制选中消息到剪贴板 |
| `?` | 帮助页面（显示所有快捷键） |
| `y/a/n/d` | 权限审批：allow_once / always_allow / deny_once / always_deny |

<p align="center">
  <img src="https://trae-api-cn.mchost.guru/api/ide/v1/text_to_image?prompt=terminal%20user%20interface%20mockup%2C%20dark%20background%2C%20colorful%20ANSI%20text%20interface%20with%20chat%20messages%20and%20tool%20call%20panels%2C%20retro%20futuristic%20hacker%20aesthetic%2C%20glowing%20green%20and%20purple%20accents%2C%20high%20detail&image_size=landscape_4_3" alt="TUI Preview" width="80%"/>
</p>

---

## 配置项（节选）

| 字段 | TOML 键 | 默认值 | 环境变量 | 说明 |
|------|---------|--------|---------|------|
| Host | `host` | `127.0.0.1` | `NUMBAT_HOST` | 监听地址 |
| GatewayPort | `gateway_port` | `7438` | `NUMBAT_GATEWAY_PORT` | HTTP/WebSocket 网关端口（唯一对外入口） |
| RateLimit | `rate_limit` | `0`（禁用） | `NUMBAT_RATE_LIMIT` | WS 单连接限流：每秒消息数 |
| RateBurst | `rate_burst` | 与 rate 一致 | `NUMBAT_RATE_BURST` | 限流突发上限 |
| AnthropicAPIKey | `anthropic_api_key` | — | `ANTHROPIC_API_KEY` / `NUMBAT_ANTHROPIC_API_KEY` | LLM API key |
| DefaultModel | `default_model` | `claude-sonnet-4-6` | `NUMBAT_DEFAULT_MODEL` | 默认模型 |
| BaseURL | `base_url` | `https://api.anthropic.com/v1/messages` | `NUMBAT_BASE_URL` | API 端点 |
| MaxSteps | `max_steps` | `20` | `NUMBAT_MAX_STEPS` | Agent Loop 最大迭代步数 |
| AutoCompactThreshold | `auto_compact_threshold` | `0`（禁用） | `NUMBAT_COMPACT_THRESHOLD` | run 中途自动压缩阈值（0–1） |
| McpServers | `[[mcp_servers]]` | — | — | MCP 服务器配置 |
| Channels | `[[channels]]` | — | — | 外部 IM 通道（feishu/telegram） |
| Routing | `[routing]` | — | — | 通道消息 → Agent 路由规则 |

完整说明见 [config.md](docs/config.md)。

---

## 数据布局（`~/.numbat/`）

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

## 目录结构

```text
numbat/
├── cmd/
│   ├── numbat-core/     # 守护进程入口
│   └── numbat-tui/      # TUI 客户端（bubbletea / lipgloss / glamour）
├── internal/
│   ├── app/             # 组件组装中心
│   ├── transport/       # RPC dispatch 核心 + HTTP/WS 网关（gin）
│   ├── loop/            # Agent Loop（类 Claude Code）
│   ├── llm/             # Anthropic Provider（SSE 流式）
│   ├── tools/           # 工具接口 + 内建工具 + Invoker
│   ├── permissions/     # 权限策略 + 审批
│   ├── session/         # 会话模型 + 文件存储
│   ├── compact/         # 上下文压缩
│   ├── context/         # 执行上下文
│   ├── memory/          # 记忆层
│   ├── skills/          # Skill 加载器
│   ├── agents/          # Agent Profile 加载器
│   ├── subagent/        # 子 Agent
│   ├── task/            # 任务管理
│   ├── mcp/             # MCP 客户端
│   ├── events/          # 事件总线
│   ├── bus/             # JSON-RPC Envelope
│   ├── channel/         # IM 通道（Telegram/飞书）与路由
│   ├── trace/           # 事件落盘
│   ├── config/          # 配置加载
│   ├── util/            # 路径等工具
│   └── webui/           # 内嵌前端产物
├── webui/               # 前端源码（React + TS + Vite）
└── docs/                # 模块文档
```

---

## 文档索引

| 文档 | 内容 |
|------|------|
| [CODE_WIKI.md](CODE_WIKI.md) | 架构总览、调用链、关键设计决策（推荐先读） |
| [getting-started.md](docs/getting-started.md) | 配置、构建、启动、TUI 操作 |
| [transport.md](docs/transport.md) | RPC dispatch 核心 + HTTP/WebSocket 网关 |
| [llm.md](docs/llm.md) | Provider 接口 + AnthropicProvider |
| [loop.md](docs/loop.md) | Agent Loop 主循环 |
| [tools.md](docs/tools.md) | 工具接口 + 注册表 + Invoker |
| [permissions.md](docs/permissions.md) | 策略评估 + 审批 |
| [session.md](docs/session.md) | 会话模型 + 存储 |
| [compact.md](docs/compact.md) | 上下文压缩 + 截断 |
| [skills.md](docs/skills.md) | Skill 加载器 |
| [subagent.md](docs/subagent.md) | 子 Agent |
| [mcp.md](docs/mcp.md) | MCP 客户端 |
| [channel.md](docs/channel.md) | 外部 IM 通道接入 + 消息路由 |
| [config.md](docs/config.md) | 配置加载 |
| [trace.md](docs/trace.md) | 事件落盘 |
| [webui-prd.md](docs/webui-prd.md) | WebUI 产品需求 |
| [webui-api-contract.md](docs/webui-api-contract.md) | WebUI 与后端的 RPC 契约 |

全部文档索引见 [CODE_WIKI.md](CODE_WIKI.md#模块索引)。

---

## License

本项目尚未指定开源许可证，使用前请与作者联系确认。