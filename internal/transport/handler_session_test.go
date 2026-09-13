package transport

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/llm"
	"github.com/youngyangyang04/numbat/internal/permissions"
	"github.com/youngyangyang04/numbat/internal/session"
	"github.com/youngyangyang04/numbat/internal/tools"
	"github.com/youngyangyang04/numbat/internal/tools/builtin"
)

// scriptedProvider 按调用序号返回预设响应；耗尽后一律返回 end_turn。
type scriptedProvider struct {
	responses []*llm.Response
	calls     int
}

func (p *scriptedProvider) Chat(_ context.Context, _ []llm.Message, _ string, _ []llm.ToolDefinition, stream *llm.StreamCallbacks) (*llm.Response, error) {
	idx := p.calls
	p.calls++
	if idx < len(p.responses) {
		return p.responses[idx], nil
	}
	return &llm.Response{
		StopReason: "end_turn",
		Text:       "done",
		Content:    []llm.ContentBlock{{Type: "text", Text: "done"}},
	}, nil
}

func (p *scriptedProvider) Model() string { return "scripted" }

// 一次 run 内产生的 tool_use / tool_result 必须一并落库，
// 否则下一轮的历史里只剩最终答复，模型会丢失自己调用过什么工具。
func TestSendMessagePersistsToolContext(t *testing.T) {
	root := t.TempDir()
	bus := events.New()
	store := session.NewStore(filepath.Join(root, "sessions"))
	mgr := session.NewManager(store, bus)
	sess, err := mgr.Create(session.ModeChat, "会话")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	provider := &scriptedProvider{responses: []*llm.Response{
		{StopReason: "tool_use", Content: []llm.ContentBlock{
			{Type: "tool_use", ID: "tu-1", Name: "read_file", Input: map[string]any{"path": "handler.go"}},
		}},
		{StopReason: "end_turn", Text: "读完了", Content: []llm.ContentBlock{{Type: "text", Text: "读完了"}}},
	}}
	registry := tools.NewRegistry()
	registry.Register(builtin.ReadFileTool{})

	handlers := NewHandlers(HandlerDeps{
		Provider:    provider,
		MaxSteps:    4,
		Invoker:     tools.NewInvoker(registry, permissions.NewManager("", 0), bus, 0),
		Bus:         bus,
		Perm:        permissions.NewManager("", 0),
		Session:     mgr,
		ToolTimeout: 5 * time.Second,
		RunsDir:     filepath.Join(root, "runs"),
	})

	params, err := json.Marshal(SessionSendMessageParams{SessionID: sess.ID, Content: "看看 handler.go"})
	if err != nil {
		t.Fatalf("序列化参数失败: %v", err)
	}
	if _, err := handlers["session.send_message"](context.Background(), params); err != nil {
		t.Fatalf("session.send_message 失败: %v", err)
	}

	msgs, err := store.ReadMessages(sess.ID)
	if err != nil {
		t.Fatalf("读取历史失败: %v", err)
	}
	var roles, types []string
	for _, m := range msgs {
		roles = append(roles, m.Role)
		for _, b := range m.Content {
			types = append(types, b.Type)
		}
	}

	wantRoles := []string{"user", "assistant", "user", "assistant"}
	wantTypes := []string{"text", "tool_use", "tool_result", "text"}
	if !reflect.DeepEqual(roles, wantRoles) {
		t.Errorf("roles = %v, want %v", roles, wantRoles)
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Errorf("block types = %v, want %v", types, wantTypes)
	}
}
