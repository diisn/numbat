package loop

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/youngyangyang04/numbat/internal/compact"
	execctx "github.com/youngyangyang04/numbat/internal/context"
	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/llm"
	"github.com/youngyangyang04/numbat/internal/permissions"
	"github.com/youngyangyang04/numbat/internal/tools"
)

// fakeProvider 按调用序号返回预设响应或错误；耗尽后复用最后一个响应。
type fakeProvider struct {
	responses []*llm.Response
	errs      []error
	calls     int
	// contextPct 会通过 stream.OnUsage 上报，用于驱动压缩触发判断。
	contextPct float64
}

func (p *fakeProvider) Chat(ctx context.Context, messages []llm.Message, system string, toolDefs []llm.ToolDefinition, stream *llm.StreamCallbacks) (*llm.Response, error) {
	idx := p.calls
	p.calls++
	if stream != nil && stream.OnUsage != nil {
		stream.OnUsage(llm.UsageStats{ContextPct: p.contextPct})
	}
	if idx < len(p.errs) && p.errs[idx] != nil {
		return nil, p.errs[idx]
	}
	if len(p.responses) == 0 {
		return &llm.Response{StopReason: "end_turn"}, nil
	}
	if idx < len(p.responses) {
		return p.responses[idx], nil
	}
	return p.responses[len(p.responses)-1], nil
}

func (p *fakeProvider) Model() string { return "fake" }

func newTestLoop(provider llm.Provider) *AgentLoop {
	bus := events.New()
	invoker := tools.NewInvoker(tools.NewRegistry(), permissions.NewManager("", 0), bus, 0)
	return New(provider, invoker, bus)
}

// max_tokens 且无 tool_use 时应继续循环，由下一轮补救，而不是直接 success。
func TestRunContinuesAfterMaxTokensWithoutToolUse(t *testing.T) {
	provider := &fakeProvider{responses: []*llm.Response{
		{StopReason: "max_tokens", Text: "半截", Content: []llm.ContentBlock{{Type: "text", Text: "半截"}}},
		{StopReason: "end_turn", Text: "完整答案", Content: []llm.ContentBlock{{Type: "text", Text: "完整答案"}}},
	}}
	l := newTestLoop(provider)
	c := execctx.NewExecutionContext("run-1", "goal", 5)
	c.AddUserMessage("goal")

	if err := l.Run(context.Background(), c, "system"); err != nil {
		t.Fatalf("Run 不应返回错误: %v", err)
	}
	if c.Step != 2 {
		t.Errorf("Step = %d, want 2", c.Step)
	}
	if c.Status != "success" {
		t.Errorf("Status = %q, want success", c.Status)
	}
	if c.Result != "完整答案" {
		t.Errorf("Result = %q, want 完整答案", c.Result)
	}
}

// LLM 调用失败不向上抛错，只把 run 标记为 failed/llm_error。
func TestRunLLMErrorDoesNotPropagate(t *testing.T) {
	provider := &fakeProvider{errs: []error{errors.New("boom")}}
	l := newTestLoop(provider)
	c := execctx.NewExecutionContext("run-1", "goal", 5)

	if err := l.Run(context.Background(), c, "system"); err != nil {
		t.Fatalf("LLM 错误不应向上抛: %v", err)
	}
	if c.Status != "failed" || c.Reason != "llm_error" {
		t.Errorf("Status/Reason = %q/%q, want failed/llm_error", c.Status, c.Reason)
	}
}

// 超过最大轮数：每轮都 tool_use 但无实际工具块，达到 maxSteps 后失败。
func TestRunExceedsMaxSteps(t *testing.T) {
	provider := &fakeProvider{responses: []*llm.Response{
		{StopReason: "tool_use", Content: []llm.ContentBlock{}},
	}}
	l := newTestLoop(provider)
	c := execctx.NewExecutionContext("run-1", "goal", 2)

	if err := l.Run(context.Background(), c, "system"); err != nil {
		t.Fatalf("Run 不应返回错误: %v", err)
	}
	if c.Status != "failed" || c.Reason != "exceeded_max_steps" {
		t.Errorf("Status/Reason = %q/%q, want failed/exceeded_max_steps", c.Status, c.Reason)
	}
	if c.Step != 2 {
		t.Errorf("Step = %d, want 2", c.Step)
	}
}

// 取消语义：ctx 已取消时 Run 返回错误，run 标记为 failed/cancelled。
func TestRunCancelledContext(t *testing.T) {
	provider := &fakeProvider{errs: []error{context.Canceled}}
	l := newTestLoop(provider)
	c := execctx.NewExecutionContext("run-1", "goal", 5)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := l.Run(ctx, c, "system"); err == nil {
		t.Fatal("取消时 Run 应返回错误")
	}
	if c.Status != "failed" || c.Reason != "cancelled" {
		t.Errorf("Status/Reason = %q/%q, want failed/cancelled", c.Status, c.Reason)
	}
}

// 成功时 FinalAssistant 记录最后一次响应的 Content。
func TestRunRecordsFinalAssistant(t *testing.T) {
	final := []llm.ContentBlock{{Type: "text", Text: "最终"}}
	provider := &fakeProvider{responses: []*llm.Response{
		{StopReason: "end_turn", Text: "最终", Content: final},
	}}
	l := newTestLoop(provider)
	c := execctx.NewExecutionContext("run-1", "goal", 5)

	if err := l.Run(context.Background(), c, "system"); err != nil {
		t.Fatalf("Run 不应返回错误: %v", err)
	}
	if !reflect.DeepEqual(c.FinalAssistant, final) {
		t.Errorf("FinalAssistant = %+v, want %+v", c.FinalAssistant, final)
	}
}

// 压缩触发条件：仅当本轮以 tool_use 收尾且 context_pct 达阈值时才压缩（与 Python 版一致）。
func TestCompactOnlyTriggeredByToolUse(t *testing.T) {
	cases := []struct {
		name       string
		stopReason string
		content    []llm.ContentBlock
		want       int
	}{
		{
			name:       "tool_use 收尾触发压缩",
			stopReason: "tool_use",
			content:    []llm.ContentBlock{{Type: "tool_use", ID: "t1", Name: "bash", Input: map[string]any{"command": "ls"}}},
			want:       1,
		},
		{
			name:       "max_tokens 收尾不压缩",
			stopReason: "max_tokens",
			content:    []llm.ContentBlock{{Type: "text", Text: "半截"}},
			want:       0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fakeProvider{
				contextPct: 0.9,
				responses: []*llm.Response{
					{StopReason: tc.stopReason, Text: "半截", Content: tc.content},
					{StopReason: "end_turn", Text: "完成", Content: []llm.ContentBlock{{Type: "text", Text: "完成"}}},
				},
			}
			bus := events.New()
			var mu sync.Mutex
			compacted := 0
			bus.Subscribe(reflect.TypeOf(events.ContextCompacted{}), func(ctx context.Context, ev events.Event) error {
				mu.Lock()
				compacted++
				mu.Unlock()
				return nil
			})

			invoker := tools.NewInvoker(tools.NewRegistry(), permissions.NewManager("", 0), bus, 0)
			l := New(provider, invoker, bus, WithCompaction(compact.New(bus, t.TempDir(), "sess-1"), 0.5))
			c := execctx.NewExecutionContext("run-1", "goal", 3)

			if err := l.Run(context.Background(), c, "system"); err != nil {
				t.Fatalf("Run 不应返回错误: %v", err)
			}
			mu.Lock()
			got := compacted
			mu.Unlock()
			if got != tc.want {
				t.Errorf("context.compacted 次数 = %d, want %d", got, tc.want)
			}
		})
	}
}
