package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/youngyangyang04/numbat/internal/bus"
)

// Client 是 numbat-core 网关（/ws 端点）的 WebSocket 客户端，
// 协议与浏览器 WebUI 完全一致：JSON-RPC 2.0 envelope，每条 WS 消息一帧。
type Client struct {
	url       string
	conn      *websocket.Conn
	mu        sync.Mutex
	events    chan bus.Envelope
	responses map[int]chan bus.Envelope
	nextID    int
}

// NewClient 创建客户端；addr 支持 "host:port" 或完整 ws:// URL。
func NewClient(addr string) *Client {
	return &Client{
		url:       wsURL(addr),
		events:    make(chan bus.Envelope, 100),
		responses: make(map[int]chan bus.Envelope),
	}
}

// wsURL 把 host:port 形式规范化为网关 /ws 端点。
func wsURL(addr string) string {
	if strings.Contains(addr, "://") {
		return addr
	}
	return "ws://" + addr + "/ws"
}

// Connect 连接到 numbat-core 网关。
// 服务端每 30s 发协议级 ping，gorilla 在读期间自动回 pong，readLoop 常驻即保活。
func (c *Client) Connect() error {
	conn, _, err := websocket.DefaultDialer.Dial(c.url, nil)
	if err != nil {
		return err
	}
	conn.SetReadLimit(8 * 1024 * 1024)
	c.conn = conn
	go c.readLoop()
	return nil
}

// Events 返回事件通道。
func (c *Client) Events() <-chan bus.Envelope {
	return c.events
}

// Subscribe 订阅事件。
func (c *Client) Subscribe() error {
	_, err := c.Call("event.subscribe", nil)
	return err
}

// Call 发送 RPC 请求并等待响应，默认超时 10 分钟。
func (c *Client) Call(method string, params any) (any, error) {
	return c.CallWithTimeout(method, params, 10*time.Minute)
}

// CallWithTimeout 发送 RPC 请求并等待响应，可指定超时。
// 超时后返回错误，避免因服务端不响应而永久阻塞。
func (c *Client) CallWithTimeout(method string, params any, timeout time.Duration) (any, error) {
	c.mu.Lock()
	c.nextID++
	if c.nextID <= 0 {
		c.nextID = 1 // 防止溢出后变 0/负数（0 会被 JSON 编码为 null 导致 ID 匹配失败）
	}
	id := c.nextID
	c.mu.Unlock()

	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, err
		}
	}
	req := bus.Envelope{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	respCh := make(chan bus.Envelope, 1)
	c.mu.Lock()
	c.responses[id] = respCh
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		delete(c.responses, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write failed: %w", err)
	}
	c.mu.Unlock()

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, fmt.Errorf("RPC error: %s", resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.responses, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("RPC timeout after %s: %s", timeout, method)
	}
}

func (c *Client) readLoop() {
	for {
		_, line, err := c.conn.ReadMessage()
		if err != nil {
			// 连接断开：唤醒所有等待中的 Call，避免永久阻塞
			c.mu.Lock()
			for id, ch := range c.responses {
				ch <- bus.Envelope{JSONRPC: "2.0", ID: id, Error: &bus.RPCError{Code: -32000, Message: "connection closed"}}
			}
			c.mu.Unlock()
			close(c.events)
			return
		}
		var env bus.Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}

		// 如果是带 id 的响应，分发给 waiting Call
		if env.ID != nil {
			id := toInt(env.ID)
			c.mu.Lock()
			if ch, ok := c.responses[id]; ok {
				delete(c.responses, id)
				c.mu.Unlock()
				ch <- env
				continue
			}
			c.mu.Unlock()
		}

		// 否则作为事件
		c.events <- env
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// Close 关闭连接。
func (c *Client) Close() error {
	if c.conn != nil {
		_ = c.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second))
		return c.conn.Close()
	}
	return nil
}
