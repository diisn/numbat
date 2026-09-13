package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/youngyangyang04/numbat/internal/bus"
)

// Client 是 numbat-core 的 TCP 客户端。
type Client struct {
	addr      string
	conn      net.Conn
	reader    *bufio.Reader
	mu        sync.Mutex
	events    chan bus.Envelope
	responses map[int]chan bus.Envelope
	nextID    int
}

// NewClient 创建客户端。
func NewClient(addr string) *Client {
	return &Client{
		addr:      addr,
		events:    make(chan bus.Envelope, 100),
		responses: make(map[int]chan bus.Envelope),
	}
}

// Connect 连接到 numbat-core。
func (c *Client) Connect() error {
	conn, err := net.Dial("tcp", c.addr)
	if err != nil {
		return err
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)
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
	c.mu.Unlock()

	c.mu.Lock()
	if _, err := c.conn.Write(data); err != nil {
		delete(c.responses, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("write failed: %w", err)
	}
	_, _ = c.conn.Write([]byte("\n"))
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
		line, err := c.reader.ReadBytes('\n')
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
		return n
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
		return c.conn.Close()
	}
	return nil
}
