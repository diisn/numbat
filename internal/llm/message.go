package llm

import "encoding/json"

// ContentBlock 表示消息中的内容块。
type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
	// Thinking / Signature 用于 extended thinking 块：回传时必须原样保留
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// MarshalJSON 保证 tool_use 块始终输出 input 字段。
//
// Input 带 omitempty：input 为 nil 或空 map 时会整字段省略。但 Anthropic
// 兼容端点（如 DeepSeek）要求 tool_use 必须携带 input，否则返回
// 400 "messages[i].content: missing field `input`"。
//
// 非 tool_use 块维持原行为（不输出 input），避免向端点发送多余字段。
func (b ContentBlock) MarshalJSON() ([]byte, error) {
	type plain ContentBlock // 别名，避免 MarshalJSON 递归调用自身
	if b.Type != "tool_use" {
		return json.Marshal(plain(b))
	}
	// 外层 Input 的嵌套深度更浅，会覆盖 plain 中被 omitempty 省略的同名字段
	return json.Marshal(struct {
		plain
		Input map[string]any `json:"input"`
	}{
		plain: plain(b),
		Input: nonNilInput(b.Input),
	})
}

// nonNilInput 把 nil map 归一化为空对象，确保序列化为 {} 而非 null。
func nonNilInput(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	return in
}

// Message 表示一条对话消息。
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// NewTextMessage 构造一条纯文本消息。
func NewTextMessage(role, text string) Message {
	return Message{
		Role:    role,
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}
