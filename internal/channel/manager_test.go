package channel

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeChannel 是可编程的测试通道：入站消息由测试推入，出站消息记录到 sends。
type fakeChannel struct {
	name   string
	status ChannelStatus
	recv   chan InboundMessage
	sends  chan OutboundMessage
	once   sync.Once
}

func newFakeChannel(name string) *fakeChannel {
	return &fakeChannel{
		name:   name,
		status: ChannelStatusDisconnected,
		recv:   make(chan InboundMessage, 16),
		sends:  make(chan OutboundMessage, 16),
	}
}

func (f *fakeChannel) Name() string { return f.name }
func (f *fakeChannel) Connect(ctx context.Context) error {
	f.status = ChannelStatusConnected
	return nil
}
func (f *fakeChannel) Disconnect() error {
	f.once.Do(func() { close(f.recv) })
	f.status = ChannelStatusDisconnected
	return nil
}
func (f *fakeChannel) Send(ctx context.Context, msg OutboundMessage) error {
	f.sends <- msg
	return nil
}
func (f *fakeChannel) Receive() <-chan InboundMessage { return f.recv }
func (f *fakeChannel) Status() ChannelStatus          { return f.status }

// waitSend 带超时读取一条出站消息。
func waitSend(t *testing.T, sends chan OutboundMessage) OutboundMessage {
	t.Helper()
	select {
	case m := <-sends:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for outbound message")
		return OutboundMessage{}
	}
}

func TestManagerFlow(t *testing.T) {
	ch := newFakeChannel("tg-main")
	router := NewRouter("executor", nil, nil, func(name string) (*AgentConfig, bool) {
		return &AgentConfig{Name: name}, true
	})

	var mu sync.Mutex
	var gotHandler bool
	handler := func(ctx context.Context, msg InboundMessage, agent *AgentConfig) (string, error) {
		mu.Lock()
		gotHandler = true
		mu.Unlock()
		if agent.Name != "executor" {
			t.Errorf("handler got agent %q, want executor", agent.Name)
		}
		return "reply:" + msg.Content, nil
	}

	mgr := NewManager(router, handler)
	if err := mgr.Register(ch); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = mgr.Start(ctx); close(done) }()

	// 入站消息未填 ChannelName，应自动补全
	ch.recv <- InboundMessage{SenderID: "alice", Content: "hi", MsgID: "m1"}

	out := waitSend(t, ch.sends)
	if out.RecipientID != "alice" || out.Content != "reply:hi" {
		t.Fatalf("unexpected outbound: %+v", out)
	}
	mu.Lock()
	ok := gotHandler
	mu.Unlock()
	if !ok {
		t.Fatal("handler was not invoked")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not stop after cancel")
	}
}

func TestManagerSerialPerSender(t *testing.T) {
	ch := newFakeChannel("tg-main")
	router := NewRouter("executor", nil, nil, func(name string) (*AgentConfig, bool) {
		return &AgentConfig{Name: name}, true
	})

	var (
		mu     sync.Mutex
		order  []string
		cur    int32
		maxCon int32
	)
	handler := func(ctx context.Context, msg InboundMessage, agent *AgentConfig) (string, error) {
		c := atomic.AddInt32(&cur, 1)
		for {
			m := atomic.LoadInt32(&maxCon)
			if c <= m || atomic.CompareAndSwapInt32(&maxCon, m, c) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond) // 人为拉长处理，暴露并发问题
		mu.Lock()
		order = append(order, msg.Content)
		mu.Unlock()
		atomic.AddInt32(&cur, -1)
		return "ok", nil
	}

	mgr := NewManager(router, handler)
	if err := mgr.Register(ch); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = mgr.Start(ctx) }()
	defer cancel()

	// 同一发送者连发三条，必须严格串行且按序执行
	for _, c := range []string{"m1", "m2", "m3"} {
		ch.recv <- InboundMessage{SenderID: "alice", Content: c, MsgID: c}
	}
	for i := 0; i < 3; i++ {
		waitSend(t, ch.sends)
	}

	if got := atomic.LoadInt32(&maxCon); got != 1 {
		t.Fatalf("same sender executed concurrently: maxConcurrent=%d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "m1" || order[1] != "m2" || order[2] != "m3" {
		t.Fatalf("messages out of order: %v", order)
	}
}

func TestManagerNoAgentDrops(t *testing.T) {
	// 无默认 Agent 且规则未命中时消息被丢弃（不回发、不执行 handler）。
	ch := newFakeChannel("tg-main")
	router := NewRouter("", nil, nil, func(name string) (*AgentConfig, bool) {
		return nil, false
	})

	var called int32
	handler := func(ctx context.Context, msg InboundMessage, agent *AgentConfig) (string, error) {
		atomic.AddInt32(&called, 1)
		return "never", nil
	}

	mgr := NewManager(router, handler)
	if err := mgr.Register(ch); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = mgr.Start(ctx) }()
	defer cancel()

	ch.recv <- InboundMessage{SenderID: "alice", Content: "hi", MsgID: "m1"}

	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&called) != 0 {
		t.Fatal("handler should not be called when no agent matched")
	}
	select {
	case m := <-ch.sends:
		t.Fatalf("unexpected outbound when agent missing: %+v", m)
	default:
	}
}

func TestManagerDuplicateRegister(t *testing.T) {
	mgr := NewManager(nil, func(ctx context.Context, msg InboundMessage, agent *AgentConfig) (string, error) {
		return "", nil
	})
	if err := mgr.Register(newFakeChannel("dup")); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := mgr.Register(newFakeChannel("dup")); err == nil {
		t.Fatal("duplicate register should fail")
	}
}
