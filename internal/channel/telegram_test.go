package channel

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// mockTelegramAPI 是 telegramAPI 的测试替身：
// getUpdates 每次调用返回预设的 updates（取后清空），sendMessage 记录调用。
type mockTelegramAPI struct {
	mu      sync.Mutex
	updates []telegramUpdate
	calls   int
	sent    []telegramOutbound
	sendErr error
}

type telegramOutbound struct {
	chatID int64
	text   string
}

func (m *mockTelegramAPI) getUpdates(ctx context.Context, offset int) ([]telegramUpdate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	us := m.updates
	m.updates = nil
	return us, nil
}

func (m *mockTelegramAPI) sendMessage(ctx context.Context, chatID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, telegramOutbound{chatID: chatID, text: text})
	return m.sendErr
}

func (m *mockTelegramAPI) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for condition")
}

func TestTelegramUpdateParsing(t *testing.T) {
	// 文本消息：转换为统一入站消息
	raw := `{"update_id":10001,"message":{"message_id":42,"chat":{"id":123456},"text":"hello"}}`
	var u telegramUpdate
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	msg, ok := toTelegramInbound("tg-main", u)
	if !ok {
		t.Fatal("text message should be accepted")
	}
	if msg.SenderID != "123456" || msg.Content != "hello" ||
		msg.MsgID != "123456:42" || msg.ChannelName != "tg-main" {
		t.Fatalf("unexpected inbound: %+v", msg)
	}

	// 无文本的消息（如图片、无 message 的 update）应被忽略
	for _, raw := range []string{
		`{"update_id":10002,"message":{"message_id":43,"chat":{"id":1}}}`,
		`{"update_id":10003,"edited_message":{"message_id":44,"chat":{"id":1},"text":"edited"}}`,
		`{"update_id":10004}`,
	} {
		var e telegramUpdate
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if _, ok := toTelegramInbound("tg-main", e); ok {
			t.Fatalf("non-text update should be ignored: %s", raw)
		}
	}
}

func TestTelegramPollOnce(t *testing.T) {
	mock := &mockTelegramAPI{updates: []telegramUpdate{
		{UpdateID: 10001, Message: &telegramMessage{MessageID: 42, Text: "hi"}},
		{UpdateID: 10002, Message: &telegramMessage{MessageID: 43, Text: "there"}},
		{UpdateID: 10003}, // 空 update 也应推进 offset
	}}
	ch := newTelegramChannelWithAPI("tg-main", "test-token", mock)

	next := ch.pollOnce(context.Background(), 0)
	if next != 10004 {
		t.Fatalf("offset should advance past all updates, got %d", next)
	}
	for i, want := range []string{"hi", "there"} {
		select {
		case m := <-ch.Receive():
			if m.Content != want {
				t.Fatalf("message %d: want %q, got %q", i, want, m.Content)
			}
			if m.SenderID != "0" { // chat.ID 未设置时为 0
				t.Fatalf("unexpected sender: %q", m.SenderID)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for inbound message")
		}
	}
}

func TestTelegramSend(t *testing.T) {
	mock := &mockTelegramAPI{}
	ch := newTelegramChannelWithAPI("tg-main", "test-token", mock)

	if err := ch.Send(context.Background(), OutboundMessage{RecipientID: "123456", Content: "pong"}); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if len(mock.sent) != 1 || mock.sent[0].chatID != 123456 || mock.sent[0].text != "pong" {
		t.Fatalf("unexpected sent messages: %+v", mock.sent)
	}

	if err := ch.Send(context.Background(), OutboundMessage{RecipientID: "not-a-number", Content: "x"}); err == nil {
		t.Fatal("invalid recipient should fail")
	}
}

func TestTelegramConnectDisconnect(t *testing.T) {
	ch := NewTelegramChannel("", "")
	if err := ch.Connect(context.Background()); err == nil {
		t.Fatal("connect with empty token should fail")
	}
	if ch.Status() != ChannelStatusError {
		t.Fatalf("status after failed connect: %s", ch.Status())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock := &mockTelegramAPI{}
	ch = newTelegramChannelWithAPI("tg-main", "test-token", mock)
	if err := ch.Connect(ctx); err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	if ch.Status() != ChannelStatusConnected {
		t.Fatalf("status after connect: %s", ch.Status())
	}
	// 轮询 goroutine 应已发起 getUpdates 调用
	waitFor(t, func() bool { return mock.callCount() >= 1 })

	if err := ch.Disconnect(); err != nil {
		t.Fatalf("disconnect failed: %v", err)
	}
	if ch.Status() != ChannelStatusDisconnected {
		t.Fatalf("status after disconnect: %s", ch.Status())
	}
	// 断开后 Receive() 通道被关闭
	select {
	case _, ok := <-ch.Receive():
		if ok {
			t.Fatal("receive channel should be closed after disconnect")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for receive channel close")
	}
}
