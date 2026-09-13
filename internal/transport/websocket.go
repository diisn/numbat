package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/youngyangyang04/numbat/internal/bus"
)

// rateLimiter 是单连接令牌桶限流器。
type rateLimiter struct {
	rate  float64
	burst float64
	mu    sync.Mutex
	last  time.Time
	avail float64
}

// newRateLimiter 创建限流器；rate 为每秒令牌数，burst 为突发上限。
func newRateLimiter(rate, burst float64) *rateLimiter {
	return &rateLimiter{
		rate:  rate,
		burst: burst,
		last:  time.Now(),
		avail: burst,
	}
}

func (rl *rateLimiter) allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	rl.avail += rl.rate * now.Sub(rl.last).Seconds()
	rl.last = now
	if rl.avail > rl.burst {
		rl.avail = rl.burst
	}
	if rl.avail >= 1 {
		rl.avail--
		return true
	}
	return false
}

func (rl *rateLimiter) disabled() bool {
	return rl == nil || rl.rate <= 0
}

// websocketConn 将 gorilla/websocket 连接包装为 connWriter，
// 使现有 subscriber/writeLoop 无需感知 WebSocket 帧细节。
// writeMu 是必须的：gorilla 明确禁止对同一连接并发写（会 panic），
// 而 RPC 响应由每个请求各自的 goroutine 写出。
type websocketConn struct {
	*websocket.Conn
	writeMu sync.Mutex
}

func (c *websocketConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	err := c.WriteMessage(websocket.TextMessage, p)
	return len(p), err
}

// handleWSConn 处理一条已升级的 WebSocket 连接。
// 它复用 Server 的 JSON-RPC dispatch、事件订阅与广播机制。
// limiter 为 nil 或 rate<=0 时禁用限流。
func handleWSConn(ctx context.Context, s *Server, ws *websocket.Conn, limiter *rateLimiter) {
	wc := &websocketConn{Conn: ws}
	s.connWg.Add(1)
	defer s.connWg.Done()

	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		s.removeSubscriber(wc)
		_ = ws.Close()
	}()

	ws.SetReadLimit(1024 * 1024) // 1 MB
	_ = ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// 启动 ping 协程保活。
	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				_ = ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !limiter.disabled() && !limiter.allow() {
			slog.Warn("websocket rate limit exceeded, closing connection")
			_ = ws.WriteControl(websocket.ClosePolicyViolation, []byte("rate limit exceeded"), time.Now().Add(5*time.Second))
			return
		}

		mt, r, err := ws.NextReader()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				slog.Error("websocket read failed", "error", err)
			}
			return
		}
		if mt != websocket.TextMessage {
			continue
		}

		line, err := io.ReadAll(r)
		if err != nil {
			slog.Error("websocket read body failed", "error", err)
			continue
		}

		var req bus.Envelope
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(wc, nil, -32700, "parse error")
			continue
		}

		_ = ws.SetReadDeadline(time.Now().Add(60 * time.Second))

		wg.Add(1)
		go func(req bus.Envelope) {
			defer wg.Done()
			resp := s.handleRequest(ctx, &req, wc)
			if resp != nil {
				s.writeEnvelope(wc, resp)
			}
		}(req)
	}
}
