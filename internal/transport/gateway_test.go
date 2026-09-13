package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/youngyangyang04/numbat/internal/events"
)

func TestGatewayRoutes(t *testing.T) {
	g := NewGateway(":0")
	g.SetRPCServer(NewServer(":0"))
	server := httptest.NewServer(g.router)
	defer server.Close()

	cases := []struct {
		path       string
		wantStatus int
	}{
		{"/health", http.StatusOK},
		{"/metrics", http.StatusOK},
	}

	for _, c := range cases {
		resp, err := http.Get(server.URL + c.path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", c.path, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != c.wantStatus {
			t.Errorf("GET %s: want status %d, got %d", c.path, c.wantStatus, resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			t.Errorf("GET %s: empty response body", c.path)
		}
		if c.path == "/health" {
			var m map[string]string
			if err := json.Unmarshal(body, &m); err != nil || m["status"] != "ok" {
				t.Errorf("GET /health: unexpected body %s", string(body))
			}
		}
	}
}

func TestGatewayWebSocket(t *testing.T) {
	g := NewGateway(":0")
	rpc := NewServer(":0")
	rpc.Register("core.ping", func(ctx context.Context, params json.RawMessage) (any, error) {
		return "pong", nil
	})
	g.SetRPCServer(rpc)
	server := httptest.NewServer(g.router)
	defer server.Close()

	wsURL := "ws" + server.URL[4:] + "/ws"
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial /ws failed: %v", err)
	}
	defer ws.Close()
	if resp != nil {
		_ = resp.Body.Close()
	}

	// 发送 core.ping，期待 pong 响应。
	req := map[string]any{"jsonrpc": "2.0", "method": "core.ping", "id": 1}
	if err := ws.WriteJSON(req); err != nil {
		t.Fatalf("write request failed: %v", err)
	}
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var res map[string]any
	if err := ws.ReadJSON(&res); err != nil {
		t.Fatalf("read response failed: %v", err)
	}
	if res["id"] != float64(1) {
		t.Errorf("want id 1, got %v", res["id"])
	}
	if res["error"] != nil {
		t.Errorf("unexpected error: %v", res["error"])
	}
}

func TestGatewayWebSocketWithoutRPCServer(t *testing.T) {
	g := NewGateway(":0")
	server := httptest.NewServer(g.router)
	defer server.Close()

	resp, err := http.Get(server.URL + "/ws")
	if err != nil {
		t.Fatalf("GET /ws failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want status 503, got %d", resp.StatusCode)
	}
}

func TestGatewayRunShutdown(t *testing.T) {
	g := NewGateway(":0")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- g.Run(ctx)
	}()

	// 给予服务器启动时间。
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway did not shut down in time")
	}
}

// newTestGateway 构造带事件总线的测试网关，注册 test.emit 方法用于发布 RunStarted 事件。
func newTestGateway(t *testing.T) (*events.Bus, *httptest.Server) {
	g := NewGateway(":0")
	rpc := NewServer(":0")
	bus := events.New()
	rpc.SetBus(bus)
	rpc.Register("test.emit", func(ctx context.Context, params json.RawMessage) (any, error) {
		_ = bus.Publish(ctx, events.RunStarted{RunID: "run-test", Goal: "goal"})
		return "ok", nil
	})
	g.SetRPCServer(rpc)
	server := httptest.NewServer(g.router)
	t.Cleanup(server.Close)
	return bus, server
}

// dialTestWS 连接测试网关的 /ws 端点。
func dialTestWS(t *testing.T, server *httptest.Server) *websocket.Conn {
	wsURL := "ws" + server.URL[4:] + "/ws"
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial /ws failed: %v", err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// readEnvelope 读取一条消息并解析为 JSON envelope。
// 非 JSON 帧（如 NDJSON 遗留的独立 "\n" 消息）直接判失败。
func readEnvelope(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read message failed: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("want text message, got opcode %d", mt)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("message is not a JSON envelope (got %q): %v", string(data), err)
	}
	return m
}

func TestGatewayWebSocketEventStream(t *testing.T) {
	bus, server := newTestGateway(t)
	ws := dialTestWS(t, server)

	// 订阅事件。
	if err := ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "event.subscribe", "id": 1}); err != nil {
		t.Fatalf("write subscribe failed: %v", err)
	}
	if m := readEnvelope(t, ws); m["id"] != float64(1) || m["error"] != nil {
		t.Fatalf("unexpected subscribe response: %v", m)
	}

	// 触发事件；事件与 emit 响应都会到达，先后顺序不保证。
	if err := ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "test.emit", "id": 2}); err != nil {
		t.Fatalf("write emit failed: %v", err)
	}
	gotEvent, gotResp := false, false
	for i := 0; i < 2; i++ {
		m := readEnvelope(t, ws)
		if m["id"] == float64(2) {
			gotResp = true
			continue
		}
		if res, _ := m["result"].(map[string]any); res != nil && res["type"] == "run.started" {
			if res["run_id"] != "run-test" {
				t.Errorf("unexpected run_id: %v", res["run_id"])
			}
			gotEvent = true
		}
	}
	if !gotEvent {
		t.Error("run.started event not received")
	}
	if !gotResp {
		t.Error("emit response not received")
	}

	// session.created 事件应被广播（事件无 id 字段）。
	_ = bus.Publish(context.Background(), events.SessionCreated{SessionID: "sess-1", Mode: "chat"})
	m := readEnvelope(t, ws)
	res, _ := m["result"].(map[string]any)
	if res == nil || res["type"] != "session.created" || res["session_id"] != "sess-1" {
		t.Errorf("session.created event not broadcast: %v", m)
	}
}

func TestGatewayWebSocketResubscribe(t *testing.T) {
	_, server := newTestGateway(t)
	ws := dialTestWS(t, server)

	// 同一连接重复订阅：连接应保持可用。
	for i := 1; i <= 2; i++ {
		if err := ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "event.subscribe", "id": i}); err != nil {
			t.Fatalf("write subscribe #%d failed: %v", i, err)
		}
		if m := readEnvelope(t, ws); m["id"] != float64(i) || m["error"] != nil {
			t.Fatalf("unexpected subscribe #%d response: %v", i, m)
		}
	}

	// 重新订阅后事件仍应送达新订阅。
	if err := ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "test.emit", "id": 3}); err != nil {
		t.Fatalf("write emit failed: %v", err)
	}
	gotEvent := false
	for i := 0; i < 2; i++ {
		m := readEnvelope(t, ws)
		if m["id"] == float64(3) {
			continue
		}
		if res, _ := m["result"].(map[string]any); res != nil && res["type"] == "run.started" {
			gotEvent = true
		}
	}
	if !gotEvent {
		t.Fatal("event not received after re-subscribe (connection broken)")
	}
}
