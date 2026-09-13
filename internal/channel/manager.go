package channel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// senderQueueSize 是单个发送者串行队列的缓冲上限。
// 队列满时入队会阻塞（背压），保证同一发送者的消息不丢失且严格有序。
const senderQueueSize = 16

// MessageHandler 处理一条已路由到目标 Agent 的入站消息，返回要回发给发送者的文本。
// 由组装层注入真正的 Agent 执行逻辑（可复用 transport 的 agent.run/session 流程），
// channel 包只依赖该回调，实现模块间解耦。
type MessageHandler func(ctx context.Context, msg InboundMessage, agent *AgentConfig) (string, error)

// Manager 管理所有已注册 Channel 的生命周期与消息调度：
// 每个 Channel 一个 goroutine 监听 Receive()；同一发送者的消息进入独立串行队列
// 顺序执行，Agent 输出经原 Channel.Send 回发。
type Manager struct {
	router   *Router
	handler  MessageHandler
	mu       sync.Mutex
	channels map[string]Channel
	queues   map[string]chan InboundMessage // key: "<channelName>/<senderID>"
	wg       sync.WaitGroup                 // 追踪每 Channel 的读循环 goroutine
}

// NewManager 创建通道管理器；handler 不能为 nil。
func NewManager(router *Router, handler MessageHandler) *Manager {
	return &Manager{
		router:   router,
		handler:  handler,
		channels: make(map[string]Channel),
		queues:   make(map[string]chan InboundMessage),
	}
}

// Register 注册一个通道；通道名重复时返回错误。
func (m *Manager) Register(ch Channel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.channels[ch.Name()]; ok {
		return fmt.Errorf("channel %q already registered", ch.Name())
	}
	m.channels[ch.Name()] = ch
	return nil
}

// Start 启动所有已注册通道：逐个 Connect 并为其启动读循环 goroutine，
// 然后阻塞直到 ctx 取消（此时断开所有通道并等待读循环退出）。
// 连接失败的通道被跳过并记录日志。
func (m *Manager) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m.mu.Lock()
	m.queues = make(map[string]chan InboundMessage) // 支持再次 Start
	m.mu.Unlock()

	started := 0
	for _, ch := range m.channels {
		if ch.Status() != ChannelStatusConnected {
			if err := ch.Connect(ctx); err != nil {
				slog.Warn("channel connect failed", "channel", ch.Name(), "error", err)
				continue
			}
		}
		started++
		m.wg.Add(1)
		go func(ch Channel) {
			defer m.wg.Done()
			m.readLoop(ctx, ch)
		}(ch)
	}
	if started == 0 {
		slog.Warn("channel manager: no channel connected, skip")
		return nil
	}

	<-ctx.Done()
	for _, ch := range m.channels {
		_ = ch.Disconnect()
	}
	m.wg.Wait()
	return nil
}

// readLoop 持续读取通道入站消息并按发送者投递到串行队列。
func (m *Manager) readLoop(ctx context.Context, ch Channel) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch.Receive():
			if !ok {
				return // 通道已断开
			}
			if msg.ChannelName == "" {
				msg.ChannelName = ch.Name()
			}
			// 同步投递以保持同一发送者的消息入队顺序（队列满时形成背压）。
			m.dispatch(ctx, ch, msg)
		}
	}
}

// dispatch 把消息投入对应发送者的串行队列，首次出现时启动该队列的 worker goroutine。
func (m *Manager) dispatch(ctx context.Context, ch Channel, msg InboundMessage) {
	key := ch.Name() + "/" + msg.SenderID

	m.mu.Lock()
	q, ok := m.queues[key]
	if !ok {
		q = make(chan InboundMessage, senderQueueSize)
		m.queues[key] = q
		go m.senderLoop(ctx, ch, q)
	}
	m.mu.Unlock()

	select {
	case q <- msg:
	case <-ctx.Done():
	}
}

// senderLoop 串行消费同一发送者的消息队列，保证该发送者的消息顺序执行、
// 不会并发触发多个 Agent 运行导致会话冲突。
func (m *Manager) senderLoop(ctx context.Context, ch Channel, q chan InboundMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-q:
			if !ok {
				return
			}
			m.process(ctx, ch, msg)
		}
	}
}

// process 完成单条消息的完整处理链：
// Router 选 Agent -> handler 回调执行 -> 结果经原通道 Send 回发。
func (m *Manager) process(ctx context.Context, ch Channel, msg InboundMessage) {
	agent := m.router.Route(msg.ChannelName, msg.SenderID)
	if agent == nil {
		// 无默认 Agent 且所有规则均未命中：拒绝该消息（不回复）。
		slog.Warn("channel: no agent matched, message dropped",
			"channel", msg.ChannelName, "sender", msg.SenderID)
		return
	}

	out, err := m.handler(ctx, msg, agent)
	if err != nil {
		slog.Error("channel: agent handler failed",
			"channel", msg.ChannelName, "sender", msg.SenderID, "agent", agent.Name, "error", err)
		return
	}
	if out == "" {
		return // 无输出（如 handler 已自行处置），无需回发
	}
	if err := ch.Send(ctx, OutboundMessage{RecipientID: msg.SenderID, Content: out}); err != nil {
		slog.Warn("channel: send failed",
			"channel", ch.Name(), "recipient", msg.SenderID, "error", err)
	}
}
