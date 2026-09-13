package transport

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/youngyangyang04/numbat/internal/bus"
	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/trace"
)

// Handler 是 JSON-RPC 方法处理函数。
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// connWriter 是事件订阅与 RPC 响应所需的最小连接抽象。
// net.Conn 与 WebSocket 连接（经包装后）均满足此接口。
type connWriter interface {
	Write([]byte) (int, error)
	Close() error
	SetWriteDeadline(t time.Time) error
}

// subscriber 表示一个事件订阅连接，事件经缓冲 channel 由独立 goroutine 写出。
type subscriber struct {
	conn     connWriter
	ch       chan []byte
	topics   []string   // fnmatch 模式列表，空表示匹配全部
	scope    string     // "global" 或 "run:<run_id>"
	active   bool       // 未激活期间广播跳过该订阅（回放先入队、实时流后跟）
	detached bool       // 被同连接的新订阅替换时置位；writeLoop 退出时不关闭连接
	writeMu  sync.Mutex // 保护 conn 写入，与 RPC 响应互斥
}

// subscriberBufSize 是事件缓冲上限；慢消费者达到上限会被断开。
const subscriberBufSize = 256

// Server 是 TCP + NDJSON + JSON-RPC 服务器。
type Server struct {
	addr        string
	listener    net.Listener
	handlers    map[string]Handler
	eventBus    *events.Bus
	subscribers map[connWriter]*subscriber
	connStates  map[net.Conn]*sync.Mutex // 非订阅连接的写锁，防止并发写破坏 NDJSON 帧
	runsDir     string                   // run 轨迹根目录，event.subscribe 回放历史事件的来源
	mu          sync.Mutex
	globalTrace *trace.GlobalWriter
	connWg      sync.WaitGroup // 追踪所有活跃连接
}

// NewServer 创建 RPC 服务器。
func NewServer(addr string) *Server {
	return &Server{
		addr:        addr,
		handlers:    make(map[string]Handler),
		subscribers: make(map[connWriter]*subscriber),
		connStates:  make(map[net.Conn]*sync.Mutex),
	}
}

// SetGlobalTrace 设置全局 trace writer，用于记录 IPC 层 trace。
func (s *Server) SetGlobalTrace(w *trace.GlobalWriter) {
	s.globalTrace = w
}

// SetRunsDir 设置 run 轨迹根目录（即 ~/.numbat/runs），
// 供 event.subscribe 的 replay_from_run 读取 <dir>/<runID>/events.jsonl。
func (s *Server) SetRunsDir(dir string) {
	s.runsDir = dir
}

// SetBus 设置事件总线并订阅事件以转发给客户端。
func (s *Server) SetBus(bus *events.Bus) {
	s.eventBus = bus
	bus.Subscribe(reflect.TypeOf(events.RunStarted{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.RunFinished{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.LLMRequest{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.LLMResponse{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.ToolCallStarted{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.ToolCallFinished{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.ToolCallFailed{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.PermissionRequested{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.PermissionGranted{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.PermissionDenied{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.SubagentStarted{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.SubagentFinished{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.SkillInvoked{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	// session.created / session.closed 由 session.Manager 发布，转发给订阅客户端
	// （无 run_id 字段，scope 过滤对它们不生效，仅 topics 匹配）。
	bus.Subscribe(reflect.TypeOf(events.SessionCreated{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.SessionClosed{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	// 流式增量与用量统计（llm.token / llm.usage）不进 trace（避免事件文件膨胀），
	// 但需要实时广播给客户端；context.compacted 与 trace 记录保持一致一并广播。
	bus.Subscribe(reflect.TypeOf(events.LLMTokens{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.LLMUsage{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
	bus.Subscribe(reflect.TypeOf(events.ContextCompacted{}), func(ctx context.Context, ev events.Event) error {
		return s.broadcastEvent(ev)
	})
}

// broadcastEvent 将事件入队到每个 topic/scope 匹配的订阅连接，非阻塞；
// 慢消费者（缓冲满）被断开。过滤以点分 topic 名与事件 run_id 为准。
func (s *Server) broadcastEvent(ev events.Event) error {
	eventType := events.Topic(ev)
	payload, err := eventToMap(ev)
	if err != nil {
		return err
	}
	payload["type"] = eventType
	data, err := json.Marshal(bus.Envelope{JSONRPC: "2.0", Result: payload})
	if err != nil {
		return err
	}
	runID, _ := payload["run_id"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn, sub := range s.subscribers {
		if !sub.active || !topicMatches(eventType, sub.topics) || !scopeMatches(runID, sub.scope) {
			continue
		}
		select {
		case sub.ch <- data:
		default:
			// 缓冲已满：订阅者消费过慢，关闭并移除，避免阻塞事件发布者
			delete(s.subscribers, conn)
			close(sub.ch)
		}
	}
	return nil
}

// topicMatches 判断点分事件类型是否命中订阅 topics 中的任一 fnmatch 模式；
// 空 topics（旧客户端未传参）视为匹配全部，保持向后兼容。
func topicMatches(eventType string, topics []string) bool {
	if len(topics) == 0 {
		return true
	}
	for _, pattern := range topics {
		if ok, _ := path.Match(pattern, eventType); ok {
			return true
		}
	}
	return false
}

// scopeMatches 判断事件 run_id 是否落在订阅 scope 内：
// "global"（或缺省）全通；"run:<id>" 精确匹配；其余不匹配。
func scopeMatches(runID, scope string) bool {
	if scope == "" || scope == "global" {
		return true
	}
	if strings.HasPrefix(scope, "run:") {
		return runID == scope[len("run:"):]
	}
	return false
}

// registerSubscriber 注册事件订阅连接并启动 writer goroutine。
// 返回的订阅默认未激活，调用 activateSubscriber 后才接收实时广播，
// 用于保证 replay_from_run 的回放事件先于实时流按序送达。
// 同一连接重复订阅时替换旧订阅：关闭其事件通道使其 writeLoop 退出（不关连接）。
func (s *Server) registerSubscriber(conn connWriter, topics []string, scope string) *subscriber {
	sub := &subscriber{
		conn:   conn,
		ch:     make(chan []byte, subscriberBufSize),
		topics: topics,
		scope:  scope,
	}
	s.mu.Lock()
	if old, ok := s.subscribers[conn]; ok {
		old.detached = true
		close(old.ch)
	}
	s.subscribers[conn] = sub
	s.mu.Unlock()
	go sub.writeLoop()
	return sub
}

// activateSubscriber 激活订阅，之后广播的事件才会入队该订阅。
func (s *Server) activateSubscriber(sub *subscriber) {
	s.mu.Lock()
	sub.active = true
	s.mu.Unlock()
}

// replayRun 从 <runsDir>/<runID>/events.jsonl 读取历史事件，将命中 topics 的
// 事件封包后按序投入订阅缓冲（先于实时流），返回实际投递条数。
func (s *Server) replayRun(sub *subscriber, runID string, topics []string) int {
	// 防止 run_id 中的 ".." 造成路径穿越
	if s.runsDir == "" || runID == "" || filepath.Base(runID) != runID {
		return 0
	}
	f, err := os.Open(filepath.Join(s.runsDir, runID, "events.jsonl"))
	if err != nil {
		return 0 // 无历史记录则静默忽略
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		eventType, _ := rec["type"].(string)
		if !topicMatches(eventType, topics) {
			continue
		}
		data, err := json.Marshal(bus.Envelope{JSONRPC: "2.0", Result: rec})
		if err != nil {
			continue
		}
		if !safeSend(sub.ch, data) {
			break // 订阅连接已被清理
		}
		count++
	}
	return count
}

// safeSend 向缓冲 channel 投递数据；连接被清理关闭 channel 时捕获 panic 返回 false。
func safeSend(ch chan []byte, data []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	ch <- data
	return true
}

// newSubscriptionID 生成形如 sub-xxxxxxxx 的订阅 ID。
func newSubscriptionID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "sub-" + hex.EncodeToString(b[:])
}

// writeLoop 将缓冲中的事件写入连接。
func (sub *subscriber) writeLoop() {
	for data := range sub.ch {
		if sub.write(data) != nil {
			break
		}
	}
	if !sub.detached {
		_ = sub.conn.Close()
	}
}

// write 串行写入一条完整消息（事件或 RPC 响应）。
// WebSocket 以独立消息成帧，无需追加 NDJSON 换行分隔。
func (sub *subscriber) write(data []byte) error {
	sub.writeMu.Lock()
	defer sub.writeMu.Unlock()
	_ = sub.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := sub.conn.Write(data); err != nil {
		return err
	}
	if _, ok := sub.conn.(*websocketConn); ok {
		return nil
	}
	_, err := sub.conn.Write([]byte("\n"))
	return err
}

// removeSubscriber 移除订阅者并关闭其缓冲 channel。
func (s *Server) removeSubscriber(conn connWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub, ok := s.subscribers[conn]; ok {
		delete(s.subscribers, conn)
		close(sub.ch)
	}
}

func eventToMap(ev events.Event) (map[string]any, error) {
	data, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Addr 返回监听地址。
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Register 注册一个 RPC 方法。
func (s *Server) Register(method string, h Handler) {
	s.handlers[method] = h
}

// Run 启动服务器并阻塞直到 ctx 取消。
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln

	slog.Info("numbat-core listening", "addr", s.addr)

	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				// 优雅关闭：等待所有活跃连接完成（最多 10 秒）
				done := make(chan struct{})
				go func() {
					s.connWg.Wait()
					close(done)
				}()
				select {
				case <-done:
					slog.Info("graceful shutdown: all connections closed")
				case <-time.After(10 * time.Second):
					slog.Warn("graceful shutdown: timeout waiting for connections, forcing close")
				}
				return nil
			}
			slog.Error("accept failed", "error", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	s.connWg.Add(1)
	defer s.connWg.Done()

	// 为非订阅连接注册写锁，防止多 goroutine 并发写破坏 NDJSON 帧
	writeMu := &sync.Mutex{}
	s.mu.Lock()
	s.connStates[conn] = writeMu
	s.mu.Unlock()

	var wg sync.WaitGroup

	defer func() {
		// 等待所有 in-flight 请求 goroutine 完成后再关闭连接，
		// 避免 goroutine 向已关闭连接写入触发 panic 或数据丢失。
		wg.Wait()
		s.mu.Lock()
		delete(s.connStates, conn)
		s.mu.Unlock()
		s.removeSubscriber(conn)
		conn.Close()
	}()

	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				slog.Error("read failed", "error", err)
			}
			return
		}

		var req bus.Envelope
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(conn, nil, -32700, "parse error")
			continue
		}

		// 每个请求独立 goroutine 处理，避免长耗时请求（如 agent.run）
		// 阻塞后续请求（如 permission.respond），响应按 ID 匹配乱序返回。
		wg.Add(1)
		go func(req bus.Envelope) {
			defer wg.Done()
			resp := s.handleRequest(ctx, &req, conn)
			if resp != nil {
				s.writeEnvelope(conn, resp)
			}
		}(req)
	}
}

// eventSubscribeParams 对应 event.subscribe 的入参。
type eventSubscribeParams struct {
	Topics        []string `json:"topics"`          // 点分类型 fnmatch 模式；缺省/为空 = 全部
	Scope         string   `json:"scope"`           // "global"（默认）| "run:<run_id>"
	ReplayFromRun string   `json:"replay_from_run"` // 非空时先从该 run 的 events.jsonl 回放历史
}

func (s *Server) handleRequest(ctx context.Context, req *bus.Envelope, conn connWriter) *bus.Envelope {
	// 记录 IPC 层请求 trace
	if s.globalTrace != nil {
		s.globalTrace.Write(trace.TraceRecord{
			Ts:        time.Now().UTC().Format(time.RFC3339),
			Direction: "CLI->CORE",
			Layer:     "ipc",
			Kind:      "request",
			Data:      map[string]any{"method": req.Method, "id": req.ID},
		})
	}

	resp := &bus.Envelope{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	// defer 统一记录 IPC 层响应 trace，覆盖所有 return 点
	defer func() {
		if s.globalTrace != nil {
			s.globalTrace.Write(trace.TraceRecord{
				Ts:        time.Now().UTC().Format(time.RFC3339),
				Direction: "CORE->CLI",
				Layer:     "ipc",
				Kind:      "response",
				Data:      map[string]any{"method": req.Method, "id": req.ID, "ok": resp.Error == nil},
			})
		}
	}()

	if req.Method == "event.subscribe" {
		var p eventSubscribeParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err != nil {
				resp.Error = &bus.RPCError{Code: -32602, Message: "invalid params: " + err.Error()}
				return resp
			}
		}
		if p.Scope == "" {
			p.Scope = "global"
		}
		// 先注册（默认未激活），回放入队完毕后再激活，
		// 保证订阅响应前收到的 replay 事件总是先于该订阅的实时事件。
		sub := s.registerSubscriber(conn, p.Topics, p.Scope)
		replayed := 0
		if p.ReplayFromRun != "" {
			replayed = s.replayRun(sub, p.ReplayFromRun, p.Topics)
		}
		s.activateSubscriber(sub)
		resp.Result = map[string]any{
			"subscription_id": newSubscriptionID(),
			"replayed_count":  replayed,
		}
		return resp
	}

	h, ok := s.handlers[req.Method]
	if !ok {
		resp.Error = &bus.RPCError{Code: -32601, Message: "method not found"}
		return resp
	}

	result, err := h(ctx, req.Params)
	if err != nil {
		resp.Error = &bus.RPCError{Code: -32603, Message: err.Error()}
		return resp
	}

	resp.Result = result
	return resp
}

func (s *Server) writeError(conn connWriter, id any, code int, message string) {
	resp := &bus.Envelope{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &bus.RPCError{Code: code, Message: message},
	}
	s.writeEnvelope(conn, resp)
}

func (s *Server) writeEnvelope(conn connWriter, env *bus.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	s.mu.Lock()
	sub, ok := s.subscribers[conn]
	s.mu.Unlock()
	if ok {
		_ = sub.write(data)
		return
	}
	// 非订阅连接：用 per-connection 写锁保护，防止并发写破坏 NDJSON 帧。
	// connStates 仅覆盖 TCP 连接；WebSocket 由 websocketConn 自带的写锁串行化。
	if nc, ok2 := conn.(net.Conn); ok2 {
		s.mu.Lock()
		writeMu, ok3 := s.connStates[nc]
		s.mu.Unlock()
		if ok3 {
			writeMu.Lock()
			defer writeMu.Unlock()
		}
		_, _ = nc.Write(data)
		_, _ = nc.Write([]byte("\n"))
		return
	}
	_, _ = conn.Write(data)
}
