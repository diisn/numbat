# internal/config — 配置加载

## 加载顺序（低 → 高优先级）

```text
默认值 → ~/.numbat/config.toml → ./.numbat/config.toml → .numbat/.env → 系统环境变量
```

同一用途存在多个变量名时按 names 顺序取第一个有值的，例如 API key 依次尝试
`ANTHROPIC_API_KEY`、`NUMBAT_ANTHROPIC_API_KEY`。

## 配置项

| 字段 | TOML 键 | 默认值 | 环境变量 | 说明 |
|------|---------|--------|---------|------|
| Host | `host` | `127.0.0.1` | `NUMBAT_HOST` | 监听地址 |
| GatewayPort | `gateway_port` | `7438` | `NUMBAT_GATEWAY_PORT` | HTTP/WebSocket 网关端口（唯一对外入口） |
| RateLimit | `rate_limit` | `0`（禁用） | `NUMBAT_RATE_LIMIT` | WS 单连接限流：每秒消息数 |
| RateBurst | `rate_burst` | 与 rate 一致 | `NUMBAT_RATE_BURST` | 限流突发上限 |
| LogLevel | `log_level` | `INFO` | `NUMBAT_LOG_LEVEL` | slog 日志级别 |
| AnthropicAPIKey | `anthropic_api_key` | — | `ANTHROPIC_API_KEY` / `NUMBAT_ANTHROPIC_API_KEY` | LLM API key |
| DefaultModel | `default_model` | `claude-sonnet-4-6` | `NUMBAT_LLM_DEFAULT_MODEL` / `NUMBAT_DEFAULT_MODEL` | 默认模型 |
| BaseURL | `base_url` | `https://api.anthropic.com/v1/messages` | `NUMBAT_BASE_URL` | API 端点 |
| MaxSteps | `max_steps` | `20` | `NUMBAT_MAX_STEPS` | ReAct 最大步数 |
| MaxTokens | `max_tokens` | `8192` | — | LLM max_tokens |
| ContextWindow | `context_window` | `200000` | — | 上下文窗口大小，用于换算 `context_pct` |
| AutoCompactThreshold | `auto_compact_threshold` | `0`（禁用） | `NUMBAT_COMPACT_THRESHOLD` | run 中途自动压缩的 `context_pct` 阈值，取值 0–1 |
| ToolResultLimit | `tool_result_limit` | `8000` | — | tool_result 截断触发字符数 |
| ToolResultKeep | `tool_result_keep` | `4000` | — | 截断后保留的前缀字符数 |
| PermissionTimeoutSec | `permission_timeout_sec` | `60` | — | 权限审批等待超时（秒，0 = 不超时） |
| ToolTimeoutSec | `tool_timeout_sec` | `120` | — | 工具执行超时（秒） |
| MaxSubagentDepth | `max_subagent_depth` | `2` | — | 子 Agent 最大嵌套深度 |
| McpServers | `[[mcp_servers]]` | — | — | MCP 服务器配置 |
| Channels | `[[channels]]` | — | — | 外部 IM 通道接入（feishu/telegram），缺省不启动 |
| Routing | `[routing]` | — | — | 通道消息 → Agent 的路由规则 |

## 通道与路由配置

```toml
[[channels]]
name      = "telegram-main"
type      = "telegram"        # feishu | telegram
enabled   = true
token     = "..."             # telegram: bot token
# app_id     = "..."          # feishu: 应用 app_id
# app_secret = "..."          # feishu: 应用 app_secret
# chat_id    = ""             # 可选

[routing]
default_agent = "executor"    # 优先级最低
[routing.by_sender]           # 优先级最高：发送者 ID -> Agent 名
"123456" = "planner"
[routing.by_channel]          # 通道名 -> Agent 名
"telegram-main" = "executor"
```

`enabled=false` 或缺省空配置时通道不启动，不影响既有行为。详见 [channel.md](channel.md)。

> 参考 `.numbat/.env.example`：对接 DeepSeek 时设置 `NUMBAT_BASE_URL=https://api.deepseek.com/anthropic`、`NUMBAT_DEFAULT_MODEL=deepseek-chat`。
