package events

import (
	"reflect"

	"github.com/youngyangyang04/numbat/internal/llm"
)

// topicNames 将事件结构体映射为点分 topic 名，
// 供事件流 type 字段、topic glob 过滤与回放共用。
var topicNames = map[reflect.Type]string{
	reflect.TypeOf(RunStarted{}):          "run.started",
	reflect.TypeOf(RunFinished{}):         "run.finished",
	reflect.TypeOf(LLMRequest{}):          "llm.request",
	reflect.TypeOf(LLMResponse{}):         "llm.response",
	reflect.TypeOf(LLMTokens{}):           "llm.token",
	reflect.TypeOf(LLMUsage{}):            "llm.usage",
	reflect.TypeOf(ToolCallStarted{}):     "tool.call_started",
	reflect.TypeOf(ToolCallFinished{}):    "tool.call_finished",
	reflect.TypeOf(ToolCallFailed{}):      "tool.call_failed",
	reflect.TypeOf(PermissionRequested{}): "permission.requested",
	reflect.TypeOf(PermissionGranted{}):   "permission.granted",
	reflect.TypeOf(PermissionDenied{}):    "permission.denied",
	reflect.TypeOf(ContextCompacted{}):    "context.compacted",
	reflect.TypeOf(SessionCreated{}):      "session.created",
	reflect.TypeOf(SessionClosed{}):       "session.closed",
	reflect.TypeOf(SubagentStarted{}):     "subagent.started",
	reflect.TypeOf(SubagentFinished{}):    "subagent.finished",
	reflect.TypeOf(SkillInvoked{}):        "skill.invoked",
}

// Topic 返回事件对应的点分 topic 名；未映射的类型回退为 Go 结构体名。
func Topic(ev Event) string {
	if name, ok := topicNames[reflect.TypeOf(ev)]; ok {
		return name
	}
	return reflect.TypeOf(ev).Name()
}

// RunStarted 表示一次 run 开始。
type RunStarted struct {
	RunID string `json:"run_id"`
	Goal  string `json:"goal"`
}

// RunFinished 表示一次 run 结束。
type RunFinished struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Result string `json:"result"`
	// Reason 是失败原因（llm_error / cancelled / exceeded_max_steps）；
	// Status 为 success 时为空。
	Reason string `json:"reason"`
	// Steps 是本次 run 实际执行的轮数。
	Steps int `json:"steps"`
}

// LLMRequest 表示即将向 LLM 发起请求。
type LLMRequest struct {
	RunID    string        `json:"run_id"`
	Messages []llm.Message `json:"messages"`
	System   string        `json:"system"`
}

// LLMResponse 表示收到 LLM 响应。
type LLMResponse struct {
	RunID string `json:"run_id"`
	Text  string `json:"text"`
}

// LLMTokens 表示流式输出的一个文本增量。
type LLMTokens struct {
	RunID string `json:"run_id"`
	Token string `json:"token"`
}

// LLMUsage 表示一次 LLM 调用的 token 用量统计。
type LLMUsage struct {
	RunID        string  `json:"run_id"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	ContextPct   float64 `json:"context_pct"`
	// prompt cache 命中/写入量；端点未返回时为 0。
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// ToolCallStarted 表示工具调用开始。
type ToolCallStarted struct {
	RunID     string         `json:"run_id"`
	ToolUseID string         `json:"tool_use_id"`
	ToolName  string         `json:"tool_name"`
	Params    map[string]any `json:"params"`
}

// ToolCallFinished 表示工具调用成功。
type ToolCallFinished struct {
	RunID     string `json:"run_id"`
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	Output    string `json:"output"`
	ElapsedMs int    `json:"elapsed_ms"`
}

// ToolCallFailed 表示工具调用失败。
type ToolCallFailed struct {
	RunID     string `json:"run_id"`
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
	ElapsedMs int    `json:"elapsed_ms"`
	// Attempt 是第几次尝试（从 1 开始）；重试过程可观测。
	Attempt int `json:"attempt"`
}

// PermissionRequested 表示需要权限审批。
type PermissionRequested struct {
	RunID     string         `json:"run_id"`
	ToolUseID string         `json:"tool_use_id"`
	ToolName  string         `json:"tool_name"`
	Params    map[string]any `json:"params"`
	Preview   string         `json:"preview"`
	SessionID string         `json:"session_id"`
}

// PermissionGranted 表示权限已授予。
type PermissionGranted struct {
	RunID     string `json:"run_id"`
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	// Decision 是本次判定来源：allow_once / always_allow / auto_allow。
	Decision string `json:"decision"`
}

// PermissionDenied 表示权限已拒绝。
type PermissionDenied struct {
	RunID     string `json:"run_id"`
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	// Decision 是本次判定来源：deny_once / always_deny / auto_deny / timeout / cancelled。
	Decision string `json:"decision"`
}

// ContextCompacted 表示上下文已压缩。
type ContextCompacted struct {
	RunID          string `json:"run_id"`
	SessionID      string `json:"session_id"`
	OriginalTokens int    `json:"original_tokens"`
	SummaryTokens  int    `json:"summary_tokens"`
}

// SessionCreated 表示会话已创建。
type SessionCreated struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
}

// SessionClosed 表示会话已关闭。
type SessionClosed struct {
	SessionID string `json:"session_id"`
}

// SubagentStarted 表示子 Agent 已启动。
type SubagentStarted struct {
	RunID       string `json:"run_id"`
	ParentRunID string `json:"parent_run_id"`
	Description string `json:"description"`
}

// SubagentFinished 表示子 Agent 已结束。
type SubagentFinished struct {
	RunID       string `json:"run_id"`
	ParentRunID string `json:"parent_run_id"`
	Status      string `json:"status"`
}

// SkillInvoked 表示一个 skill 被触发。
type SkillInvoked struct {
	SkillName string `json:"skill_name"`
	Arguments string `json:"arguments"`
	RunID     string `json:"run_id"`
}
