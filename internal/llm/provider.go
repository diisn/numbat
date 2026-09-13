package llm

import "context"

// ToolDefinition 是提供给 LLM 的工具定义。
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Response 是 LLM 的响应。
type Response struct {
	StopReason string
	Text       string
	Content    []ContentBlock
}

// UsageStats 是一次 LLM 调用的 token 用量统计。
type UsageStats struct {
	InputTokens  int
	OutputTokens int
	ContextPct   float64
	// prompt cache 的命中量与写入量；端点未返回时为 0。
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

// StreamCallbacks 是流式输出的回调集合；nil 字段跳过对应回调。
// 由调用方（loop 层）实现并转发到事件总线，避免 llm 包反向依赖 events。
type StreamCallbacks struct {
	OnToken func(token string)     // 每个文本增量
	OnUsage func(usage UsageStats) // 一次调用的用量统计
}

// Provider 抽象 LLM 调用能力。
type Provider interface {
	Chat(ctx context.Context, messages []Message, system string, tools []ToolDefinition, stream *StreamCallbacks) (*Response, error)
	Model() string
}
