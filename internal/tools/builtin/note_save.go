package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/youngyangyang04/numbat/internal/tools"
)

// NoteStore 是 note_save 所需的会话笔记落库能力（由 *session.Store 实现）。
type NoteStore interface {
	AppendNote(sid, content, runID string) error
}

// NoteSaveTool 保存会话笔记：把一条事实/决定追加到当前 session 的 notes.md，
// 后续轮次的 system prompt 会读取并注入（Session Notes）。
// 仅在会话场景（session.send_message 及其 skill）绑定 Store/SessionID/RunID。
type NoteSaveTool struct {
	Store     NoteStore
	SessionID string
	RunID     string
}

// Name 返回工具名。
func (NoteSaveTool) Name() string { return "note_save" }

// Description 返回工具描述（提示笔记在本会话后续轮次可见）。
func (NoteSaveTool) Description() string {
	return "Save a concise fact or decision to this session's notes. " +
		"These notes are visible in future turns of the same session."
}

// InputSchema 返回参数 schema。
func (NoteSaveTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The durable fact or decision to remember.",
			},
		},
		"required": []string{"content"},
	}
}

// Invoke 执行工具：内容去空白后追加到会话笔记，返回 "saved"。
func (n NoteSaveTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	content, ok := params["content"].(string)
	if !ok {
		return tools.Result{Content: "content must be a string", IsError: true, ErrorType: "schema_error"}, nil
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return tools.Result{Content: "empty content", IsError: true, ErrorType: "runtime_error"}, nil
	}
	if n.Store == nil {
		return tools.Result{Content: "note_save is unavailable outside a session", IsError: true, ErrorType: "runtime_error"}, nil
	}
	if err := n.Store.AppendNote(n.SessionID, content, n.RunID); err != nil {
		return tools.Result{Content: fmt.Sprintf("failed to save note: %v", err), IsError: true, ErrorType: "runtime_error"}, nil
	}
	return tools.Result{Content: "saved"}, nil
}
