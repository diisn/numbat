# cmd/numbat-tui — TUI 客户端

基于 bubbletea (Elm 架构)，6 文件结构：

| 文件 | 行数 | 职责 |
|------|------|------|
| [main.go](../cmd/numbat-tui/main.go) | ~89 | 入口 + 全局样式 + Markdown 渲染 |
| [model.go](../cmd/numbat-tui/model.go) | ~1009 | 状态、Update、事件处理、异步命令 |
| [view.go](../cmd/numbat-tui/view.go) | ~215 | View 渲染、状态栏、帮助页、viewport 内容构建 |
| [lines.go](../cmd/numbat-tui/lines.go) | ~333 | line 接口、textLine/toolLine、语法高亮 |
| [keymap.go](../cmd/numbat-tui/keymap.go) | ~143 | 权限按键、slash 补全按键 |
| [styles.go](../cmd/numbat-tui/styles.go) | ~93 | lipgloss 样式集中定义 |

## 模式系统（mode 枚举）

```go
type mode int
const (
    modeInput  mode = iota  // 默认：输入文本
    modeBrowse              // 浏览历史消息
    modePerm                // 权限审批弹窗
    modeSlash               // 斜杠命令补全
    modeHelp                // 帮助页面
)
```

替代了旧的 `focusInput` + `permMode` + `slashVisible` 布尔标志组合，状态转换清晰无冲突。

## 消息滚动（viewport）

接入 `bubbles/viewport`，全功能滚动：

| 按键 | 作用 |
|------|------|
| `↑` `↓` / `k` `j` | 逐行滚动（选中行跟随） |
| `PageUp` `PageDown` | 整页翻页 |
| `g` / `Home` | 跳转到顶部 |
| `G` / `End` | 跳到底部 |

**智能跟随**：默认自动跟随底部（新消息自动滚到最下面）；用户手动向上滚动后暂停跟随；滚回底部 / 切回输入模式时恢复跟随。

## 工具输出语法高亮

使用 chroma 库自动推断语言：bash(go/py/js/ts/json/yaml/md/toml/html/css/rs/sh)，自动检测 JSON 格式输出。

- 配色：monokai 主题 + 256 色终端输出
- 高亮失败自动回退纯文本
- 展开/收起状态加 ▶ / ▼ 标记

## 工具输出内嵌滚动

展开后 output 固定显示 20 行（`maxOutputLines`）：

| 按键 | 作用 |
|------|------|
| `[` `]` | 逐行滚动 |
| `{` `}` | 翻页滚动 |
| 滚动指示器 | `[start-end/total lines pct%]` |

## 输入历史

`↑` `↓` 调出历史消息，去重 + 循环浏览。空输入和非空输入行为合理区分。

## 帮助页面

`?` 键弹出快捷键说明页，分模式列出所有按键。`Esc` / `q` 关闭。

## Spinner 动画

工具执行中（`tool.call_started` 到 `call_finished` 之间）显示 `bubbles/spinner` 转动动画。

## 消息复制

浏览模式下选中消息按 `y`，复制纯文本到剪贴板。

## 并发安全

所有异步操作通过 `tea.Cmd` + Msg 回传 Update 循环，不在 goroutine 里直接修改 model：

| 原调用 | 改为 |
|--------|------|
| `go m.createSessionAndSend(value)` | `createSessionAndSendCmd` → `sessionCreatedMsg` |
| `go m.sendMessage(...)` | `sendMessageCmd` → `rpcErrMsg` |
| `go m.respondPermission(...)` | `respondPermissionCmd` → `permRespondedMsg` |

## 断线重连

连接失败/断开后每 2s 自动重连，重连成功后恢复事件流。

## 优雅关闭

Ctrl+C 时优雅关闭 WebSocket 连接，不等永久阻塞的 RPC 调用。
