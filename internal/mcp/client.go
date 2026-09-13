package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ToolDef 是 MCP server 提供的工具定义。
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// readResult 是一次 stdout 读取的结果。
type readResult struct {
	line []byte
	err  error
}

// Client 是与 MCP server 通信的 JSON-RPC 2.0 stdio 客户端。
type Client struct {
	cmd    string
	args   []string
	env    []string
	proc   *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	lines  chan readResult // 常驻读协程投递的行
	done   chan struct{}   // 关闭后读协程退出，不再阻塞在投递上
	cancel context.CancelFunc

	mu     sync.Mutex
	nextID int
}

// NewClient 创建 MCP 客户端，cmd 为启动命令。
func NewClient(cmd string, args []string, env []string) *Client {
	return &Client{cmd: cmd, args: args, env: env, done: make(chan struct{})}
}

// Connect 启动子进程并完成 MCP initialize 握手。
func (c *Client) Connect(ctx context.Context) error {
	procCtx, cancel := context.WithCancel(ctx)
	proc := exec.CommandContext(procCtx, c.cmd, c.args...)
	proc.Env = append(os.Environ(), c.env...)
	// 上下文取消或进程退出后，最多再等 2s 管道 I/O，避免 Wait 永久阻塞。
	proc.WaitDelay = 2 * time.Second

	stdin, err := proc.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := proc.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := proc.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := proc.Start(); err != nil {
		cancel()
		return fmt.Errorf("start %s: %w", c.cmd, err)
	}
	c.proc = proc
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	c.lines = make(chan readResult, 16)
	c.cancel = cancel
	go c.readLoop()
	go drainStderr(stderr)

	if _, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "numbat", "version": "0.1"},
	}); err != nil {
		_ = c.Close()
		return err
	}
	return c.notify(ctx, "notifications/initialized", map[string]any{})
}

// ListTools 列出 MCP server 提供的工具定义。
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	result, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var defs []ToolDef
	tools, _ := result["tools"].([]any)
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		defs = append(defs, ToolDef{
			Name:        asString(m["name"]),
			Description: asString(m["description"]),
			InputSchema: asMap(m["inputSchema"]),
		})
	}
	return defs, nil
}

// CallTool 调用 MCP server 上的工具，返回所有 text 内容拼接结果。
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	result, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var parts []string
	content, _ := result["content"].([]any)
	for _, item := range content {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "text" {
			parts = append(parts, asString(m["text"]))
		}
	}
	return strings.Join(parts, "\n"), nil
}

// drainStderr 持续排空子进程 stderr，避免管道写满导致子进程阻塞。
func drainStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		if _, err := r.Read(buf); err != nil {
			return
		}
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asFloat64(v any) float64 {
	f, _ := v.(float64)
	return f
}

// Close 关闭 stdin、终止子进程，并让常驻读协程退出。
func (c *Client) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	if c.proc == nil {
		return nil
	}
	if c.cancel != nil {
		c.cancel() // CommandContext 会 kill 子进程，避免 Wait 永久阻塞
	}
	_ = c.stdin.Close()
	_ = c.proc.Wait()
	return nil
}

// call 发送请求并等待匹配 id 的响应，串行保证响应不错配。
func (c *Client) call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nextID++
	id := c.nextID
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("mcp write: %w", err)
	}

	for {
		line, err := c.readLine(ctx)
		if err != nil {
			return nil, err
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if asFloat64(msg["id"]) != float64(id) {
			continue // 服务端主动通知或其它响应，跳过
		}
		if errObj, ok := msg["error"].(map[string]any); ok {
			return nil, fmt.Errorf("MCP error: %s (code=%v)", asString(errObj["message"]), errObj["code"])
		}
		result, _ := msg["result"].(map[string]any)
		return result, nil
	}
}

// notify 发送无响应的 JSON-RPC 通知。
func (c *Client) notify(ctx context.Context, method string, params map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	req := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// readLoop 是 stdout 的唯一读取者，串行读取并投递到 c.lines。
// 必须常驻且唯一：若每次读取都新起 goroutine，超时后旧 goroutine 会滞留在
// ReadBytes 上，与下一次读取争抢同一个 bufio.Reader，导致泄漏与响应错配。
func (c *Client) readLoop() {
	for {
		line, err := c.stdout.ReadBytes('\n')
		select {
		case c.lines <- readResult{line: line, err: err}:
		case <-c.done:
			return
		}
		if err != nil {
			return
		}
	}
}

// readLine 读取一行 JSON，跳过空行；ctx 取消时立即返回。
func (c *Client) readLine(ctx context.Context) ([]byte, error) {
	for {
		select {
		case r := <-c.lines:
			if r.err != nil {
				return nil, fmt.Errorf("mcp read: %w", r.err)
			}
			if line := bytes.TrimRight(r.line, "\r\n "); len(line) > 0 {
				return line, nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
