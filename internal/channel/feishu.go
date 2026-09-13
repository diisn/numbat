package channel

import (
	"context"
	"fmt"
	"sync"
)

// FeishuChannel 是飞书通道适配器骨架。
// 本阶段仅提供可编译的接入框架与配置校验，尚未接入官方 SDK；
// 接入方式（见 gateway-layer-spec 4.3）：官方 SDK 的 WebSocket 长连接
// （无需公网 Webhook），收到事件后按 MsgID 去重再转换为 InboundMessage。
type FeishuChannel struct {
	name      string
	appID     string
	appSecret string
	recv      chan InboundMessage
	cancel    context.CancelFunc
	once      sync.Once
	mu        sync.Mutex
	status    ChannelStatus
}

// NewFeishuChannel 创建飞书通道适配器（app_id/app_secret 由 config 注入）。
func NewFeishuChannel(name, appID, appSecret string) *FeishuChannel {
	return &FeishuChannel{
		name:      name,
		appID:     appID,
		appSecret: appSecret,
		recv:      make(chan InboundMessage, 64),
		status:    ChannelStatusDisconnected,
	}
}

// Name 返回通道名。
func (f *FeishuChannel) Name() string { return f.name }

// Status 返回当前连接状态。
func (f *FeishuChannel) Status() ChannelStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *FeishuChannel) setStatus(s ChannelStatus) {
	f.mu.Lock()
	f.status = s
	f.mu.Unlock()
}

// Connect 校验接入配置并建立连接。
// TODO(真实 SDK)：当前为占位——校验通过后仅启动等待退出的接收循环，
// 不产生入站消息。后续替换为官方 SDK WebSocket 长连接事件驱动。
func (f *FeishuChannel) Connect(ctx context.Context) error {
	if f.name == "" || f.appID == "" || f.appSecret == "" {
		f.setStatus(ChannelStatusError)
		return fmt.Errorf("feishu: name, app_id and app_secret are required")
	}
	pollCtx, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	f.setStatus(ChannelStatusConnected)
	go f.receiveLoop(pollCtx)
	return nil
}

// Disconnect 断开连接并关闭入站消息通道。
func (f *FeishuChannel) Disconnect() error {
	if f.cancel != nil {
		f.cancel()
	}
	f.once.Do(func() { close(f.recv) })
	f.setStatus(ChannelStatusDisconnected)
	return nil
}

// Send 把消息回发给飞书用户。
// TODO(真实 SDK)：先用 app_id/app_secret 换取 tenant_access_token
// （POST /open-apis/auth/v3/tenant_access_token/internal），再调用
// POST /open-apis/im/v1/messages 发送文本消息。本阶段未实现，返回占位错误。
func (f *FeishuChannel) Send(ctx context.Context, msg OutboundMessage) error {
	return fmt.Errorf("feishu send: not implemented yet (TODO: official SDK integration)")
}

// Receive 返回入站消息流。
func (f *FeishuChannel) Receive() <-chan InboundMessage { return f.recv }

// receiveLoop 是占位的接收循环。
// TODO(真实 SDK)：应改为飞书开放平台 WebSocket 长连接事件循环——
// 事件到达后做 MsgID 去重、媒体可选下载（本期文本优先），再投递到 recv。
// 骨架阶段不产生入站消息，仅等待 ctx 取消后退出。
func (f *FeishuChannel) receiveLoop(ctx context.Context) {
	<-ctx.Done()
}
