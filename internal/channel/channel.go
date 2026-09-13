// Package channel 定义网关层的通道接入抽象：把不同 IM 平台
// （飞书、Telegram 等）统一成 Channel 接口，供通道管理器调度。
package channel

import "context"

// Channel 抽象一个外部 IM 通道。适配器内部负责：
// 事件去重（按 MsgID 跳过已处理消息）、平台消息与 Inbound/OutboundMessage 的格式转换。
type Channel interface {
	// Name 返回通道名，须全局唯一。
	Name() string
	// Connect 建立与平台的连接（长轮询或长连接）；ctx 取消时应断开并退出。
	Connect(ctx context.Context) error
	// Disconnect 主动断开连接，并关闭 Receive() 返回的通道。
	Disconnect() error
	// Send 把一条出站消息发给平台侧用户。
	Send(ctx context.Context, msg OutboundMessage) error
	// Receive 返回入站消息流；连接断开时通道被关闭。
	Receive() <-chan InboundMessage
	// Status 返回当前连接状态。
	Status() ChannelStatus
}

// InboundMessage 是平台消息转换后的统一入站消息。
type InboundMessage struct {
	SenderID    string // 平台侧用户标识
	ChannelName string // 所属通道名
	Content     string // 文本内容
	MsgID       string // 平台消息 ID，用于去重
}

// OutboundMessage 是统一出站消息。
type OutboundMessage struct {
	RecipientID string // 平台侧接收者标识（与 InboundMessage.SenderID 对应）
	Content     string // 文本内容
}

// ChannelStatus 描述通道连接状态。
type ChannelStatus string

const (
	ChannelStatusConnected    ChannelStatus = "connected"
	ChannelStatusDisconnected ChannelStatus = "disconnected"
	ChannelStatusError        ChannelStatus = "error"
)
