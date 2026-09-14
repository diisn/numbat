package loop

import (
	"context"
	"sync"

	"github.com/youngyangyang04/numbat/internal/compact"
	execctx "github.com/youngyangyang04/numbat/internal/context"
	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/llm"
	"github.com/youngyangyang04/numbat/internal/tools"
)

// AgentLoop 是 ReAct 循环实现。
// 轮数上限由 execCtx.MaxSteps 携带，不在本结构内重复保存。
type AgentLoop struct {
	provider llm.Provider
	invoker  *tools.Invoker
	bus      *events.Bus

	compactor        *compact.Compactor
	compactThreshold float64
}

// Option 配置 AgentLoop 的可选行为。
type Option func(*AgentLoop)

// WithCompaction 启用 run 中途的上下文压缩：单次 LLM 调用的 context_pct 达到
// threshold 时，把历史替换为一份交接摘要。threshold <= 0 表示禁用（默认）。
func WithCompaction(compactor *compact.Compactor, threshold float64) Option {
	return func(l *AgentLoop) {
		l.compactor = compactor
		l.compactThreshold = threshold
	}
}

// New 创建 AgentLoop。
func New(provider llm.Provider, invoker *tools.Invoker, bus *events.Bus, opts ...Option) *AgentLoop {
	l := &AgentLoop{
		provider: provider,
		invoker:  invoker,
		bus:      bus,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Run 执行 ReAct 循环。
//
// 收尾语义：正常终止（end_turn / 超过最大轮数 / LLM 调用失败）都只写入
// execCtx 的 Status 与 Reason，不返回错误；仅当上下文被取消（用户中止）时才返回错误，
// 以便调用方区分「失败的 run」与「被中止的 run」。
func (l *AgentLoop) Run(ctx context.Context, execCtx *execctx.ExecutionContext, system string) error {
	toolDefs := l.invoker.Registry().ToolDefinitions()

	for !execCtx.IsDone() && execCtx.Step < execCtx.MaxSteps {
		execCtx.Step++

		// LLM 请求前对超长 tool_result 做内存截断（原始内容保留在事件流中）
		requestMessages := compact.TruncateToolResults(execCtx.Messages)

		_ = l.bus.Publish(ctx, events.LLMRequest{
			RunID:    execCtx.RunID,
			Messages: requestMessages,
			System:   system,
		})

		var lastUsage llm.UsageStats
		stream := &llm.StreamCallbacks{
			OnToken: func(token string) {
				_ = l.bus.Publish(ctx, events.LLMTokens{RunID: execCtx.RunID, Token: token})
			},
			OnUsage: func(usage llm.UsageStats) {
				lastUsage = usage
				_ = l.bus.Publish(ctx, events.LLMUsage{
					RunID:                    execCtx.RunID,
					InputTokens:              usage.InputTokens,
					OutputTokens:             usage.OutputTokens,
					ContextPct:               usage.ContextPct,
					CacheReadInputTokens:     usage.CacheReadInputTokens,
					CacheCreationInputTokens: usage.CacheCreationInputTokens,
				})
			},
		}

		resp, err := l.provider.Chat(ctx, requestMessages, system, toolDefs, stream)
		if err != nil {
			if ctx.Err() != nil {
				execCtx.MarkFailed("cancelled", err.Error())
				return err
			}
			execCtx.MarkFailed("llm_error", err.Error())
			return nil
		}

		_ = l.bus.Publish(ctx, events.LLMResponse{
			RunID: execCtx.RunID,
			Text:  resp.Text,
		})

		execCtx.Messages = append(execCtx.Messages, llm.Message{
			Role:    "assistant",
			Content: resp.Content,
		})
		execCtx.FinalAssistant = resp.Content

		l.act(ctx, execCtx, resp)

		// 终止判断：end_turn 优先于 max_steps（同一步同时命中时以 end_turn 为准）
		switch {
		case resp.StopReason == "end_turn" || resp.StopReason == "":
			execCtx.MarkSuccess(resp.Text)
		case execCtx.Step >= execCtx.MaxSteps:
			execCtx.MarkFailed("exceeded_max_steps", "exceeded max steps")
		}

		// 压缩检查放在工具结果追加之后：此时历史以 user 消息收尾，
		// 替换成「摘要 + 确认」对下一次 LLM 调用是合法输入。
		l.compactIfNeeded(ctx, execCtx, resp.StopReason, lastUsage)
	}

	return nil
}

// act 执行本轮请求的工具调用。
// - max_tokens 截断且已产生不完整 tool_use：补写合成错误 tool_result 保持消息平衡；
// - max_tokens 截断但没有 tool_use：不追加任何内容，交由下一轮让模型补救；
// - tool_use：并行执行同一轮的多个工具，结果按原顺序回填。
func (l *AgentLoop) act(ctx context.Context, execCtx *execctx.ExecutionContext, resp *llm.Response) {
	switch resp.StopReason {
	case "tool_use":
		toolUses := toolUseBlocks(resp.Content)
		if len(toolUses) == 0 {
			return
		}
		results := make([]llm.ContentBlock, len(toolUses))
		var wg sync.WaitGroup
		for i, block := range toolUses {
			wg.Add(1)
			go func(i int, block llm.ContentBlock) {
				defer wg.Done()
				res := l.invoker.Invoke(ctx, execCtx.RunID, block.ID, block.Name, block.Input)
				results[i] = llm.ContentBlock{
					Type:      "tool_result",
					ToolUseID: block.ID,
					Content:   res.Content,
					IsError:   res.IsError,
				}
			}(i, block)
		}
		wg.Wait()
		execCtx.Messages = append(execCtx.Messages, llm.Message{Role: "user", Content: results})

	case "max_tokens":
		toolUses := toolUseBlocks(resp.Content)
		if len(toolUses) == 0 {
			return
		}
		results := make([]llm.ContentBlock, 0, len(toolUses))
		for _, block := range toolUses {
			results = append(results, llm.ContentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content: "Error: output token limit reached before this tool call could be completed. " +
					"Please break the task into smaller steps and try again.",
				IsError: true,
			})
		}
		execCtx.Messages = append(execCtx.Messages, llm.Message{Role: "user", Content: results})
	}
}

// compactIfNeeded 在 run 继续、且本轮以工具调用收尾时，按 context_pct 决定是否压缩。
// 只有 tool_use 收尾才压缩：此时历史末尾是配对的 tool_result，整体替换为
// [摘要, 确认] 后对下一次 LLM 调用才是合法输入。
func (l *AgentLoop) compactIfNeeded(ctx context.Context, execCtx *execctx.ExecutionContext, stopReason string, usage llm.UsageStats) {
	if l.compactor == nil || l.compactThreshold <= 0 || execCtx.IsDone() {
		return
	}
	if stopReason != "tool_use" {
		return
	}
	if usage.ContextPct < l.compactThreshold {
		return
	}
	if _, err := l.compactor.Compact(ctx, execCtx, l.provider, ""); err != nil {
		// 压缩失败不应中断 run：保留原历史继续。
		return
	}
}

// toolUseBlocks 提取响应中的 tool_use 块。
func toolUseBlocks(content []llm.ContentBlock) []llm.ContentBlock {
	var blocks []llm.ContentBlock
	for _, block := range content {
		if block.Type == "tool_use" {
			blocks = append(blocks, block)
		}
	}
	return blocks
}
