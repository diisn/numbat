package session

import (
	"testing"

	"github.com/youngyangyang04/numbat/internal/llm"
)

// trimOrphanToolUse 只裁尾部未配对的 tool_use，配对完整时原样返回（与 Python 版一致）。
func TestTrimOrphanToolUse(t *testing.T) {
	userText := llm.NewTextMessage("user", "hi")
	assistantText := llm.NewTextMessage("assistant", "done")
	assistantToolUse := llm.Message{Role: "assistant", Content: []llm.ContentBlock{
		{Type: "tool_use", ID: "tu-1", Name: "bash"},
	}}
	toolResult := llm.Message{Role: "user", Content: []llm.ContentBlock{
		{Type: "tool_result", ToolUseID: "tu-1", Content: "ok"},
	}}

	cases := []struct {
		name string
		in   []llm.Message
		want int
	}{
		{"配对完整", []llm.Message{userText, assistantToolUse, toolResult, assistantText}, 4},
		{"尾部孤立 tool_use", []llm.Message{userText, assistantToolUse}, 1},
		{"孤立 tool_use 之后的消息一并裁掉", []llm.Message{userText, assistantToolUse, assistantText}, 1},
		{"无 tool_use", []llm.Message{userText, assistantText}, 2},
		{"空历史", nil, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trimOrphanToolUse(tc.in)
			if len(got) != tc.want {
				t.Errorf("保留 %d 条消息, want %d", len(got), tc.want)
			}
		})
	}
}

// Clone 后空 RunIDs 必须保持为空切片而非 nil：nil 会被序列化成 JSON null，
// 违反前端契约 run_ids: string[]（api.ts）。
func TestCloneKeepsEmptyRunIDs(t *testing.T) {
	s := NewSession("sess-1", ModeChat, "t")
	cp := s.Clone()
	if cp.RunIDs == nil {
		t.Fatal("Clone 后 RunIDs 为 nil，序列化会变成 JSON null")
	}
	if len(cp.RunIDs) != 0 {
		t.Errorf("Clone 后 RunIDs = %v, want 空切片", cp.RunIDs)
	}
}

// ReadMessages 落盘的历史里若有孤立 tool_use，读取时必须裁掉，否则下一次请求会被上游拒绝。
func TestReadMessagesTrimsOrphanToolUse(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.AppendMessages("sess-1", []llm.Message{
		llm.NewTextMessage("user", "hi"),
		{Role: "assistant", Content: []llm.ContentBlock{{Type: "tool_use", ID: "tu-1", Name: "bash"}}},
	}); err != nil {
		t.Fatalf("AppendMessages 失败: %v", err)
	}

	msgs, err := store.ReadMessages("sess-1")
	if err != nil {
		t.Fatalf("ReadMessages 失败: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Errorf("ReadMessages = %+v, want 仅保留 user 消息", msgs)
	}
}
