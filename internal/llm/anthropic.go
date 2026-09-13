package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// maxStreamRetries 是流式调用网络中断的最大重试次数。
const maxStreamRetries = 3

// retryBackoff 是重试退避序列（秒）。
var retryBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// DefaultSystemPrompt 是角色基础提示词。调用方通常显式传入（与记忆层拼装后作为
// system 的前缀）；仅当 system 为空时作为兜底。
const DefaultSystemPrompt = "You are a helpful AI assistant. " +
	"Use the available tools to complete the user's goal. " +
	"When the goal is fully achieved, respond with a final answer and do not call any more tools."

// permanentError 包装不应重试的错误：HTTP 非 2xx 响应、上游在流内返回的协议错误。
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// isPermanent 判断错误是否属于永久失败（不应重试）。
func isPermanent(err error) bool {
	var pe *permanentError
	return errors.As(err, &pe)
}

// AnthropicProvider 实现了 Anthropic Messages API 的流式调用。
type AnthropicProvider struct {
	client        *http.Client
	apiKey        string
	model         string
	baseURL       string
	maxTokens     int
	contextWindow int
}

// NewAnthropicProvider 创建 Anthropic 兼容端点的 provider。
func NewAnthropicProvider(apiKey, model, baseURL string, maxTokens, contextWindow int) *AnthropicProvider {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1/messages"
	}
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	if contextWindow <= 0 {
		contextWindow = 200000
	}
	return &AnthropicProvider{
		client:        &http.Client{Timeout: 300 * time.Second},
		apiKey:        apiKey,
		model:         model,
		baseURL:       baseURL,
		maxTokens:     maxTokens,
		contextWindow: contextWindow,
	}
}

// cacheControl 是 Anthropic prompt cache 标记。
type cacheControl struct {
	Type string `json:"type"`
}

// systemBlock 是 system prompt 的 blocks 形式（支持 cache_control）。
type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// toolDefWithCache 在 ToolDefinition 基础上支持 cache_control。
type toolDefWithCache struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *cacheControl  `json:"cache_control,omitempty"`
}

// Model 返回当前 provider 使用的模型名称。
func (p *AnthropicProvider) Model() string {
	return p.model
}

// Chat 流式调用 LLM：逐 token 回调 OnToken，完成后回调 OnUsage 并返回结构化响应。
func (p *AnthropicProvider) Chat(ctx context.Context, messages []Message, system string, tools []ToolDefinition, stream *StreamCallbacks) (*Response, error) {
	// 回传 thinking 块需要原样保留（thinking + signature）；空 thinking 块（旧数据）丢弃
	filtered := filterMessages(messages)

	sysText := system
	if sysText == "" {
		sysText = DefaultSystemPrompt
	}

	body := map[string]any{
		"model":      p.model,
		"max_tokens": p.maxTokens,
		"stream":     true,
		"system": []systemBlock{{
			Type:         "text",
			Text:         sysText,
			CacheControl: &cacheControl{Type: "ephemeral"},
		}},
		"messages": filtered,
	}

	if len(tools) > 0 {
		// 最后一个 tool 加 cache_control，使工具定义命中 prompt cache
		withCache := make([]toolDefWithCache, len(tools))
		for i, t := range tools {
			withCache[i] = toolDefWithCache{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
		}
		withCache[len(withCache)-1].CacheControl = &cacheControl{Type: "ephemeral"}
		body["tools"] = withCache
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 流式调用：仅对网络类中断做指数退避重试。
	// HTTP 非 2xx、上游协议错误等永久失败立即返回，避免 4xx/401 白等退避时间。
	var resp *Response
	for attempt := 1; attempt <= maxStreamRetries; attempt++ {
		resp, err = p.chatStream(ctx, data, stream, attempt == 1)
		if err == nil {
			break
		}
		if isPermanent(err) || ctx.Err() != nil {
			return nil, err
		}
		if attempt == maxStreamRetries {
			slog.Error("stream failed after retries", "attempts", maxStreamRetries, "error", err)
			return nil, err
		}
		delay := retryBackoff[attempt-1]
		slog.Warn("stream dropped, retrying", "attempt", attempt, "delay", delay, "error", err)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return resp, nil
}

// chatStream 发起一次流式请求并解析 SSE 事件流。publishTokens 控制是否回调 OnToken
// （重试时关闭，避免 TUI 重复显示）。
func (p *AnthropicProvider) chatStream(ctx context.Context, body []byte, stream *StreamCallbacks, publishTokens bool) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		slog.Error("LLM API error", "status", resp.StatusCode, "response", string(bodyBytes))
		return nil, &permanentError{fmt.Errorf("anthropic API returned %d: %s", resp.StatusCode, string(bodyBytes))}
	}

	result, err := parseSSE(resp.Body, stream, publishTokens, p.contextWindow)
	if err != nil {
		return nil, err
	}

	// usage 回调
	if stream != nil && stream.OnUsage != nil {
		stream.OnUsage(UsageStats{
			InputTokens:              result.inputTokens,
			OutputTokens:             result.outputTokens,
			ContextPct:               result.contextPct,
			CacheReadInputTokens:     result.cacheReadInputTokens,
			CacheCreationInputTokens: result.cacheCreationInputTokens,
		})
	}
	return &result.Response, nil
}

// streamResult 是 SSE 解析的累积结果。
type streamResult struct {
	Response
	inputTokens              int
	outputTokens             int
	cacheReadInputTokens     int
	cacheCreationInputTokens int
	contextPct               float64
}

// parseSSE 解析 Anthropic SSE 事件流，累积 content blocks。
func parseSSE(r io.Reader, stream *StreamCallbacks, publishTokens bool, contextWindow int) (*streamResult, error) {
	result := &streamResult{Response: Response{StopReason: "end_turn"}}
	var blocks []ContentBlock
	inputJSON := map[int]*strings.Builder{} // tool_use 块的 input JSON 累积

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			// content_block_start
			ContentBlock ContentBlock `json:"content_block"`
			// content_block_delta
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				StopReason  string `json:"stop_reason"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			// message_start
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			// message_delta
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line[6:]), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message_start":
			result.inputTokens = ev.Message.Usage.InputTokens
			result.cacheReadInputTokens = ev.Message.Usage.CacheReadInputTokens
			result.cacheCreationInputTokens = ev.Message.Usage.CacheCreationInputTokens
		case "content_block_start":
			blocks = append(blocks, ev.ContentBlock)
			if ev.ContentBlock.Type == "tool_use" {
				inputJSON[ev.Index] = &strings.Builder{}
			}
		case "content_block_delta":
			if ev.Index < 0 || ev.Index >= len(blocks) {
				continue
			}
			block := &blocks[ev.Index]
			switch ev.Delta.Type {
			case "text_delta":
				block.Text += ev.Delta.Text
				if publishTokens && stream != nil && stream.OnToken != nil && ev.Delta.Text != "" {
					stream.OnToken(ev.Delta.Text)
				}
			case "thinking_delta":
				block.Thinking += ev.Delta.Thinking
			case "signature_delta":
				block.Signature += ev.Delta.Signature
			case "input_json_delta":
				if b, ok := inputJSON[ev.Index]; ok {
					b.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				result.StopReason = ev.Delta.StopReason
			}
			if ev.Usage.OutputTokens > 0 {
				result.outputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			// 完成
		case "error":
			var raw struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal([]byte(line[6:]), &raw)
			return nil, &permanentError{fmt.Errorf("LLM stream error: %s", raw.Error.Message)}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	// tool_use input JSON 反序列化
	for i, block := range blocks {
		if block.Type == "tool_use" {
			if b, ok := inputJSON[i]; ok && b.Len() > 0 {
				var input map[string]any
				if err := json.Unmarshal([]byte(b.String()), &input); err == nil {
					blocks[i].Input = input
				}
			}
		}
	}

	// 汇总文本
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	result.Content = blocks
	result.Text = text.String()
	result.contextPct = float64(result.inputTokens) / float64(contextWindow)
	return result, nil
}

// filterMessages 过滤消息：丢弃空的 thinking 块（无 thinking 内容的旧格式数据）。
func filterMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		var content []ContentBlock
		for _, c := range msg.Content {
			if c.Type == "thinking" && c.Thinking == "" {
				continue
			}
			content = append(content, c)
		}
		if len(content) > 0 {
			out = append(out, Message{Role: msg.Role, Content: content})
		}
	}
	return out
}
