# internal/llm — Provider 接口 + AnthropicProvider

## Provider 接口（provider.go）

```go
Chat(ctx, messages, system, tools, *StreamCallbacks) (*Response, error)
```

## 消息模型（message.go）

`Message{Role, Content[]ContentBlock}`；`ContentBlock` 支持 `text` / `tool_use` / `tool_result` / `thinking`（含 `Signature`，回传须原样保留）。

## AnthropicProvider（anthropic.go）

- `Chat`：`max_tokens` 从 config 读取（默认 8192）、`stream=true`，system 与最后一个 tool 加 `cache_control: ephemeral`
- 网络中断按 `1s/2s/4s` 退避重试（最多 3 次），重试时 `publishTokens=false` 避免 TUI 重复显示
- `parseSSE`：解析 `message_start` / `content_block_start` / `content_block_delta`（text/thinking/signature/input_json_delta）/ `message_delta` / `message_stop` / `error`
- `tool_use` 的 input 通过 `input_json_delta` 累积后反序列化
- `filterMessages`：丢弃空 thinking 块（旧数据兼容）
- `contextPct = inputTokens / contextWindow`（contextWindow 从 config 读取，默认 200000）
