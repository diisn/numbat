package tools

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/permissions"
)

// fakeTool 是可编程的测试工具：按调用序号返回结果、记录调用次数、可选阻塞。
type fakeTool struct {
	name     string
	required []string

	mu       sync.Mutex
	calls    int
	results  []Result // 按调用序号取用；耗尽后复用最后一个
	blockFor time.Duration
}

func (f *fakeTool) Name() string { return f.name }

func (f *fakeTool) Description() string { return "fake tool for tests" }

func (f *fakeTool) InputSchema() map[string]any {
	required := f.required
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "required": required}
}

func (f *fakeTool) Invoke(ctx context.Context, params map[string]any) (Result, error) {
	f.mu.Lock()
	f.calls++
	idx := f.calls - 1
	var res Result
	switch {
	case len(f.results) == 0:
		res = Result{Content: "ok"}
	case idx < len(f.results):
		res = f.results[idx]
	default:
		res = f.results[len(f.results)-1]
	}
	block := f.blockFor
	f.mu.Unlock()

	if block > 0 {
		time.Sleep(block)
	}
	return res, nil
}

func (f *fakeTool) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// eventRecorder 收集关注的事件，便于断言「是否发出某类事件」。
type eventRecorder struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *eventRecorder) record(_ context.Context, ev events.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func (r *eventRecorder) all() []events.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]events.Event, len(r.events))
	copy(out, r.events)
	return out
}

// newRecorder 订阅与权限/工具调用相关的全部事件类型。
func newRecorder(bus *events.Bus) *eventRecorder {
	rec := &eventRecorder{}
	for _, tp := range []reflect.Type{
		reflect.TypeOf(events.PermissionRequested{}),
		reflect.TypeOf(events.PermissionGranted{}),
		reflect.TypeOf(events.PermissionDenied{}),
		reflect.TypeOf(events.ToolCallStarted{}),
		reflect.TypeOf(events.ToolCallFinished{}),
		reflect.TypeOf(events.ToolCallFailed{}),
	} {
		bus.Subscribe(tp, rec.record)
	}
	return rec
}

func filterEvents[T any](rec *eventRecorder) []T {
	var out []T
	for _, ev := range rec.all() {
		if v, ok := ev.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func newTestPerm() *permissions.Manager {
	return permissions.NewManager("", 0)
}

// 参数校验先于权限：schema_error 时不应发出审批请求，也不应调用工具。
func TestInvokerValidatesParamsBeforePermission(t *testing.T) {
	bus := events.New()
	rec := newRecorder(bus)
	reg := NewRegistry()
	tool := &fakeTool{name: "need_path", required: []string{"path"}}
	reg.Register(tool)
	inv := NewInvoker(reg, newTestPerm(), bus, 0)

	res := inv.Invoke(context.Background(), "run-1", "tu-1", "need_path", map[string]any{})
	if res.ErrorType != "schema_error" {
		t.Fatalf("ErrorType = %q, want schema_error", res.ErrorType)
	}
	if tool.callCount() != 0 {
		t.Errorf("参数非法时不应调用工具: calls=%d", tool.callCount())
	}
	if n := len(filterEvents[events.PermissionRequested](rec)); n != 0 {
		t.Errorf("参数非法时不应发出 permission.requested: n=%d", n)
	}
}

// auto_allow（默认 Allow 的工具）不应发送 permission.granted。
func TestInvokerAutoAllowEmitsNoGranted(t *testing.T) {
	bus := events.New()
	rec := newRecorder(bus)
	reg := NewRegistry()
	reg.Register(&fakeTool{name: "read_file"})
	inv := NewInvoker(reg, newTestPerm(), bus, 0)

	res := inv.Invoke(context.Background(), "run-1", "tu-1", "read_file", map[string]any{})
	if res.IsError {
		t.Fatalf("调用应成功: %+v", res)
	}
	if n := len(filterEvents[events.PermissionGranted](rec)); n != 0 {
		t.Errorf("auto_allow 不应发送 permission.granted: n=%d", n)
	}
}

// 需要人工审批的工具：批准后发出 permission.granted，Decision 为 allow_once。
func TestInvokerManualAllowEmitsGranted(t *testing.T) {
	bus := events.New()
	rec := newRecorder(bus)
	reg := NewRegistry()
	reg.Register(&fakeTool{name: "unknown_tool_x"})
	inv := NewInvoker(reg, newTestPerm(), bus, 0)

	// Publish 同步回调：收到审批请求后立即批准（pending channel 带缓冲，Respond 不阻塞）
	bus.Subscribe(reflect.TypeOf(events.PermissionRequested{}), func(_ context.Context, ev events.Event) error {
		if p, ok := ev.(events.PermissionRequested); ok {
			inv.perm.Respond(p.ToolUseID, "allow_once")
		}
		return nil
	})

	res := inv.Invoke(context.Background(), "run-1", "tu-42", "unknown_tool_x", map[string]any{})
	if res.IsError {
		t.Fatalf("批准后调用应成功: %+v", res)
	}
	granted := filterEvents[events.PermissionGranted](rec)
	if len(granted) != 1 {
		t.Fatalf("want 1 permission.granted, got %d", len(granted))
	}
	if granted[0].Decision != "allow_once" {
		t.Errorf("Decision = %q, want allow_once", granted[0].Decision)
	}
}

// 权限拒绝：tool.call_failed 携带 tool_use_id，permission.denied 的 Decision 为 deny_once。
func TestInvokerManualDenyReportsToolUseID(t *testing.T) {
	bus := events.New()
	rec := newRecorder(bus)
	reg := NewRegistry()
	reg.Register(&fakeTool{name: "unknown_tool_x"})
	inv := NewInvoker(reg, newTestPerm(), bus, 0)

	bus.Subscribe(reflect.TypeOf(events.PermissionRequested{}), func(_ context.Context, ev events.Event) error {
		if p, ok := ev.(events.PermissionRequested); ok {
			inv.perm.Respond(p.ToolUseID, "deny_once")
		}
		return nil
	})

	const toolUseID = "tu-deny-1"
	res := inv.Invoke(context.Background(), "run-1", toolUseID, "unknown_tool_x", map[string]any{})
	if res.ErrorType != "permission_denied" {
		t.Fatalf("ErrorType = %q, want permission_denied", res.ErrorType)
	}

	failed := filterEvents[events.ToolCallFailed](rec)
	if len(failed) != 1 {
		t.Fatalf("want 1 tool.call_failed, got %d", len(failed))
	}
	if failed[0].ToolUseID != toolUseID || failed[0].ToolUseID == "" {
		t.Errorf("ToolUseID = %q, want %q", failed[0].ToolUseID, toolUseID)
	}
	if failed[0].ErrorType != "permission_denied" {
		t.Errorf("ErrorType = %q, want permission_denied", failed[0].ErrorType)
	}

	denied := filterEvents[events.PermissionDenied](rec)
	if len(denied) != 1 || denied[0].Decision != "deny_once" {
		t.Errorf("PermissionDenied = %+v, want 1 条 decision=deny_once", denied)
	}
}

// 工具阻塞超过 invoker 超时 → 归类为 timeout。
func TestInvokerTimeout(t *testing.T) {
	bus := events.New()
	reg := NewRegistry()
	reg.Register(&fakeTool{name: "read_file", blockFor: 200 * time.Millisecond, results: []Result{{Content: "late"}}})
	inv := NewInvoker(reg, newTestPerm(), bus, 50*time.Millisecond)

	res := inv.Invoke(context.Background(), "run-1", "tu-1", "read_file", map[string]any{})
	if res.ErrorType != "timeout" {
		t.Fatalf("ErrorType = %q, want timeout", res.ErrorType)
	}
	if !strings.Contains(res.Content, "tool timed out after") {
		t.Errorf("Content = %q, 应包含 \"tool timed out after\"", res.Content)
	}
}

// 重试：前两次 runtime_error、第三次成功，失败事件的 Attempt 依次为 1、2。
func TestInvokerRetryReportsAttempt(t *testing.T) {
	defer SetRetryBaseForTest(time.Millisecond)()

	bus := events.New()
	rec := newRecorder(bus)
	reg := NewRegistry()
	reg.Register(&fakeTool{name: "read_file", results: []Result{
		{Content: "boom-1", IsError: true, ErrorType: "runtime_error"},
		{Content: "boom-2", IsError: true, ErrorType: "runtime_error"},
		{Content: "ok"},
	}})
	inv := NewInvoker(reg, newTestPerm(), bus, 0)

	res := inv.Invoke(context.Background(), "run-1", "tu-1", "read_file", map[string]any{})
	if res.IsError {
		t.Fatalf("第三次应成功: %+v", res)
	}

	failed := filterEvents[events.ToolCallFailed](rec)
	if len(failed) != 2 {
		t.Fatalf("want 2 tool.call_failed, got %d", len(failed))
	}
	if failed[0].Attempt != 1 || failed[1].Attempt != 2 {
		t.Errorf("Attempt = %d, %d; want 1, 2", failed[0].Attempt, failed[1].Attempt)
	}
	if n := len(filterEvents[events.ToolCallFinished](rec)); n != 1 {
		t.Errorf("want 1 tool.call_finished, got %d", n)
	}
}
