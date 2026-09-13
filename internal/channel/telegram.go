package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Telegram Bot API 常量。
const (
	tgDefaultBase     = "https://api.telegram.org"
	tgLongPollTimeout = 30                     // getUpdates 长轮询秒数
	tgPollInterval    = 500 * time.Millisecond // 两次轮询间的最小间隔（出错时防忙轮询）
	tgRecvBufSize     = 64
)

// telegramUpdate 对应 getUpdates 返回的单个 update。
type telegramUpdate struct {
	UpdateID int              `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

// telegramMessage 对应 update 内的消息体（仅取文本消息所需字段）。
type telegramMessage struct {
	MessageID int `json:"message_id"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text string `json:"text"`
}

// telegramAPI 抽象 Telegram Bot API 网络调用，便于测试注入 mock。
type telegramAPI interface {
	getUpdates(ctx context.Context, offset int) ([]telegramUpdate, error)
	sendMessage(ctx context.Context, chatID int64, text string) error
}

// httpTelegramAPI 是基于 net/http 的真实实现（不依赖外部 SDK）。
type httpTelegramAPI struct {
	token  string
	base   string
	client *http.Client
}

func newHTTPTelegramAPI(token string) *httpTelegramAPI {
	return &httpTelegramAPI{
		token:  token,
		base:   tgDefaultBase,
		client: &http.Client{Timeout: time.Duration(tgLongPollTimeout+10) * time.Second},
	}
}

func (a *httpTelegramAPI) getUpdates(ctx context.Context, offset int) ([]telegramUpdate, error) {
	url := fmt.Sprintf("%s/bot%s/getUpdates?offset=%d&timeout=%d", a.base, a.token, offset, tgLongPollTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK          bool             `json:"ok"`
		Result      []telegramUpdate `json:"result"`
		Description string           `json:"description"`
	}
	if err := doTelegramJSON(a.client, req, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("telegram getUpdates error: %s", resp.Description)
	}
	return resp.Result, nil
}

func (a *httpTelegramAPI) sendMessage(ctx context.Context, chatID int64, text string) error {
	url := fmt.Sprintf("%s/bot%s/sendMessage", a.base, a.token)
	body, err := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := doTelegramJSON(a.client, req, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("telegram sendMessage error: %s", resp.Description)
	}
	return nil
}

// doTelegramJSON 发起请求并把 JSON 响应解码到 out；非 200 状态返回错误。
func doTelegramJSON(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("telegram http status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// TelegramChannel 通过 Bot API 长轮询接入 Telegram。
type TelegramChannel struct {
	name   string
	token  string
	api    telegramAPI
	recv   chan InboundMessage
	cancel context.CancelFunc
	once   sync.Once
	mu     sync.Mutex
	status ChannelStatus
}

// NewTelegramChannel 创建 Telegram 通道适配器。
func NewTelegramChannel(name, token string) *TelegramChannel {
	return newTelegramChannelWithAPI(name, token, newHTTPTelegramAPI(token))
}

// newTelegramChannelWithAPI 使用指定 API 实现创建通道（测试注入 mock 用）。
func newTelegramChannelWithAPI(name, token string, api telegramAPI) *TelegramChannel {
	return &TelegramChannel{
		name:   name,
		token:  token,
		api:    api,
		recv:   make(chan InboundMessage, tgRecvBufSize),
		status: ChannelStatusDisconnected,
	}
}

// Name 返回通道名。
func (t *TelegramChannel) Name() string { return t.name }

// Status 返回当前连接状态。
func (t *TelegramChannel) Status() ChannelStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *TelegramChannel) setStatus(s ChannelStatus) {
	t.mu.Lock()
	t.status = s
	t.mu.Unlock()
}

// Connect 建立连接：校验 name/token 后启动 getUpdates 长轮询 goroutine。
func (t *TelegramChannel) Connect(ctx context.Context) error {
	if t.name == "" || t.token == "" {
		t.setStatus(ChannelStatusError)
		return fmt.Errorf("telegram: name and token are required")
	}
	pollCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.setStatus(ChannelStatusConnected)
	go t.pollLoop(pollCtx)
	return nil
}

// Disconnect 断开连接并关闭入站消息通道。
func (t *TelegramChannel) Disconnect() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.once.Do(func() { close(t.recv) })
	t.setStatus(ChannelStatusDisconnected)
	return nil
}

// Send 通过 sendMessage 回发消息。
func (t *TelegramChannel) Send(ctx context.Context, msg OutboundMessage) error {
	chatID, err := strconv.ParseInt(msg.RecipientID, 10, 64)
	if err != nil {
		return fmt.Errorf("telegram: invalid recipient chat id %q: %w", msg.RecipientID, err)
	}
	return t.api.sendMessage(ctx, chatID, msg.Content)
}

// Receive 返回入站消息流。
func (t *TelegramChannel) Receive() <-chan InboundMessage { return t.recv }

// pollLoop 长轮询循环：拉取 updates、推进 offset、投递文本消息。
func (t *TelegramChannel) pollLoop(ctx context.Context) {
	offset := 0
	for {
		if ctx.Err() != nil {
			return
		}
		offset = t.pollOnce(ctx, offset)
		select {
		case <-ctx.Done():
			return
		case <-time.After(tgPollInterval):
		}
	}
}

// pollOnce 执行一次 getUpdates 并把其中的文本消息投递到接收通道，
// 返回下次轮询的 offset（按已返回的 update_id 推进，保证不重不漏）。
func (t *TelegramChannel) pollOnce(ctx context.Context, offset int) int {
	updates, err := t.api.getUpdates(ctx, offset)
	if err != nil {
		if ctx.Err() == nil {
			t.setStatus(ChannelStatusError)
			slog.Warn("telegram getUpdates failed", "channel", t.name, "error", err)
		}
		return offset
	}
	t.setStatus(ChannelStatusConnected)

	next := offset
	for _, u := range updates {
		if u.UpdateID+1 > next {
			next = u.UpdateID + 1
		}
		msg, ok := toTelegramInbound(t.name, u)
		if !ok {
			continue // 非文本消息（如图片、编辑等）本期忽略
		}
		select {
		case t.recv <- msg:
		case <-ctx.Done():
			return next
		}
	}
	return next
}

// toTelegramInbound 把 Telegram update 转换为统一入站消息；
// 仅处理带文本的消息，无文本返回 false。
func toTelegramInbound(channelName string, u telegramUpdate) (InboundMessage, bool) {
	if u.Message == nil || u.Message.Text == "" {
		return InboundMessage{}, false
	}
	return InboundMessage{
		// 私聊场景 chat.id 即用户标识，回发时作为 RecipientID。
		SenderID:    strconv.FormatInt(u.Message.Chat.ID, 10),
		ChannelName: channelName,
		Content:     u.Message.Text,
		MsgID:       fmt.Sprintf("%d:%d", u.Message.Chat.ID, u.Message.MessageID),
	}, true
}
