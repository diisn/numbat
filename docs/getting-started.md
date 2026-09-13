# 快速开始

## 配置

```bash
# 方式 1：.env 文件
cp .numbat/.env.example .numbat/.env
# 编辑 .numbat/.env 填写 API key

# 方式 2：环境变量
$env:NUMBAT_ANTHROPIC_API_KEY="your-key"
$env:NUMBAT_BASE_URL="https://api.deepseek.com/anthropic"
$env:NUMBAT_DEFAULT_MODEL="deepseek-chat"
```

配置加载优先级（低→高）：默认值 → `~/.numbat/config.toml` → `./.numbat/config.toml` → `numbat_*` 环境变量 → `.numbat/.env`

详见 [config.md](config.md)。

## 构建

```bash
go build ./cmd/numbat-core && go build ./cmd/numbat-tui
```

> **Windows 注意**：运行中的 exe 被锁定，改完 Go 代码必须先停进程、重新构建、再启动——直接重启旧 exe 会跑旧逻辑，观测到的行为与源码不符（详见根目录 [AGENTS.md](../AGENTS.md) 第 7 节）。

## 启动

```bash
# 终端 1：守护进程（numbat-core）
go run ./cmd/numbat-core

# 终端 2：TUI 客户端
go run ./cmd/numbat-tui -addr 127.0.0.1:7437
```

numbat-core 启动两个对外入口：

- **TCP RPC :7437**：TUI / 脚本（NDJSON + JSON-RPC 2.0）。
- **HTTP/WS 网关 :7438**：`/health` `/metrics` `/ws` 与 `/app/*`（内嵌 WebUI）；WebSocket 复用同一套 RPC 协议与事件订阅。

可用 `curl http://127.0.0.1:7438/health` 快速验证进程存活。端口经 `config.GatewayPort` / `NUMBAT_GATEWAY_PORT` 调整。

## WebUI

前端源码在 `webui/`（React + TypeScript + Vite），生产产物由 numbat-core 经 go:embed 内嵌，挂在网关 `/app/*` 下（SPA fallback 到 index.html）。

```bash
# 生产：构建前端 → 重建二进制（内嵌产物）→ 浏览器访问 http://127.0.0.1:7438/app/
cd webui && npm install && npm run build    # 产物输出到 ../internal/webui/dist
cd .. && go build ./cmd/numbat-core

# 开发：Vite dev server（:5173），/ws、/health、/metrics 代理到本地 :7438
cd webui && npm run dev
```

- 开发期默认用 Mock 数据独立于后端工作；联调时把 `webui/src/lib/client.ts` 的 `USE_MOCK` 改为 `false` 并启动 numbat-core，UI 代码零改动。契约与 Mock 规范见 [webui-api-contract.md](webui-api-contract.md) 与根目录 `AGENTS.md`。
- 测试：`npm run test`（vitest）、`npm run typecheck`（tsc）。

## IM 通道（可选）

配置了 `enabled=true` 的 `[[channels]]` 时，numbat-core 会额外启动 IM 接入（Telegram/飞书），消息按路由规则送达 Agent。详见 [channel.md](channel.md)。

## TUI 操作

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

### TUI 模式

| 模式 | 说明 |
|------|------|
| `input` | 默认模式，输入文本 |
| `browse` | 浏览历史消息，`Esc` 切回 |
| `perm` | 权限审批弹窗 |
| `slash` | 斜杠命令补全 |
| `help` | 帮助页面 |
