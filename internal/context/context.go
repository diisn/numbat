package context

import (
	"strings"

	"github.com/youngyangyang04/numbat/internal/llm"
)

// ExecutionContext 维护一次 run 的状态。
type ExecutionContext struct {
	RunID    string
	Goal     string
	MaxSteps int
	Messages []llm.Message
	Step     int
	Status   string
	Result   string
	// Reason 是失败原因（llm_error / cancelled / exceeded_max_steps / panic）。
	// Status 为 failed 时才有意义。
	Reason string
	// FinalAssistant 是本次 run 最后一条 assistant 消息的 content blocks，供调用方落库。
	// 中途发生上下文压缩时 Messages 会被整体替换，故单独保留一份。
	FinalAssistant []llm.ContentBlock
	// Compacted 标记本次 run 中途发生过上下文压缩：Messages 已被替换为「摘要 + 确认」，
	// 调用方据此只落最终答复，而不是把摘要当成真实对话轮次落库。
	Compacted bool

	// system prompt 的三个来源。
	// SystemPromptOverride 非空时替换角色基础提示词（skill / 子 Agent profile 场景），
	// 记忆层始终追加在其后。
	SystemPromptOverride string
	GlobalContext        string
	ProjectContext       string
	SessionNotes         string
}

// NewExecutionContext 创建一个新的执行上下文。
func NewExecutionContext(runID, goal string, maxSteps int) *ExecutionContext {
	return &ExecutionContext{
		RunID:    runID,
		Goal:     goal,
		MaxSteps: maxSteps,
		Messages: make([]llm.Message, 0),
		Status:   "running",
	}
}

// SystemPrompt 组合本次 run 的 system prompt：角色基础提示词（或被 override 替换）
// 在前，记忆层（Global / Project / Session Notes）依次追加在后。
func (c *ExecutionContext) SystemPrompt(base string) string {
	if c.SystemPromptOverride != "" {
		base = c.SystemPromptOverride
	}
	parts := []string{base}
	if v := strings.TrimSpace(c.GlobalContext); v != "" {
		parts = append(parts, "## Global Context\n"+v)
	}
	if v := strings.TrimSpace(c.ProjectContext); v != "" {
		parts = append(parts, "## Project Context\n"+v)
	}
	if v := strings.TrimSpace(c.SessionNotes); v != "" {
		parts = append(parts, "## Session Notes\n"+v+"\n\nRemember important durable facts by calling note_save.")
	}
	return strings.Join(parts, "\n\n")
}

// AddUserMessage 添加一条用户文本消息。
func (c *ExecutionContext) AddUserMessage(content string) {
	c.Messages = append(c.Messages, llm.NewTextMessage("user", content))
}

// IsDone 返回 run 是否已结束。
func (c *ExecutionContext) IsDone() bool {
	return c.Status != "running"
}

// MarkSuccess 将 run 标记为成功。
func (c *ExecutionContext) MarkSuccess(result string) {
	c.Status = "success"
	c.Result = result
}

// MarkFailed 将 run 标记为失败并记录原因。
func (c *ExecutionContext) MarkFailed(reason, result string) {
	c.Status = "failed"
	c.Reason = reason
	c.Result = result
}
