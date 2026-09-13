package llm

import (
	"context"
	"time"
)

// TraceFunc 是 LLM 层 trace 回调。direction="CORE->LLM"/"LLM->CORE", layer="llm", kind="request"/"response"。
// 为 nil 时不记录 trace。
type TraceFunc func(direction, layer, kind string, data any)

// TracingProvider 包装真实 Provider，在 Chat 前后记录 trace。
type TracingProvider struct {
	Inner          Provider
	Trace          TraceFunc
	IncludePayload bool
}

// Model 返回内部 provider 的模型名称。
func (t *TracingProvider) Model() string {
	return t.Inner.Model()
}

// Chat 实现 Provider 接口，在调用前后记录 trace。
func (t *TracingProvider) Chat(
	ctx context.Context,
	messages []Message,
	system string,
	tools []ToolDefinition,
	stream *StreamCallbacks,
) (*Response, error) {
	// 记录请求
	if t.Trace != nil {
		reqData := map[string]any{
			"system":     system,
			"tool_count": len(tools),
			"model":      t.Inner.Model(),
		}
		if t.IncludePayload {
			reqData["messages"] = messages
			reqData["tools"] = tools
		}
		t.Trace("CORE->LLM", "llm", "request", reqData)
	}

	start := time.Now()
	resp, err := t.Inner.Chat(ctx, messages, system, tools, stream)
	elapsed := time.Since(start)

	// 记录响应（成功时）
	if t.Trace != nil && err == nil {
		// 统计 tool_use 块数量（从 resp.Content 提取）
		toolUseCount := 0
		for _, block := range resp.Content {
			if block.Type == "tool_use" {
				toolUseCount++
			}
		}
		respData := map[string]any{
			"stop_reason":    resp.StopReason,
			"text_len":       len(resp.Text),
			"tool_use_count": toolUseCount,
			"elapsed_ms":     elapsed.Milliseconds(),
		}
		if t.IncludePayload {
			respData["text"] = resp.Text
			respData["content"] = resp.Content
		}
		t.Trace("LLM->CORE", "llm", "response", respData)
	}

	// 记录错误（可选，确保不 panic）
	if t.Trace != nil && err != nil {
		errData := map[string]any{
			"error":      err.Error(),
			"elapsed_ms": elapsed.Milliseconds(),
		}
		t.Trace("LLM->CORE", "llm", "error", errData)
	}

	return resp, err
}
