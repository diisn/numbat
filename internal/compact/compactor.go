package compact

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	execctx "github.com/youngyangyang04/numbat/internal/context"
	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/llm"
)

const compactPrompt = `You are compressing an agent conversation into a handoff summary.
Another LLM instance will continue this task from your summary alone — make it complete.

Structure your response with exactly these six sections:

## 1. Original Goal
One sentence describing what the user asked the agent to accomplish.

## 2. Completed Steps
Bullet list of what has been done. Be specific (file paths, commands run, decisions made).

## 3. Key Constraints & Discoveries
Facts learned during the run that affect future decisions
(e.g., API limitations, file formats, user preferences stated mid-conversation).

## 4. Current File State
For each file that was created or modified: path, a one-line description of its current state.

## 5. Remaining TODOs
Ordered list of what still needs to be done to complete the original goal.

## 6. Critical Data
Any values the next LLM needs verbatim: IDs, tokens, exact error messages, config values
discovered during the run.

Be concise. Omit reasoning steps and intermediate attempts. Keep conclusions.`

// Result 表示压缩结果。
type Result struct {
	SummaryText           string
	OriginalTokenEstimate int
	SummaryTokens         int
}

// Compactor 把过长的对话历史压缩成一份交接摘要。
// sessionDir 用于落盘 summary_<ts>.md，bus 用于发布 context.compacted。
type Compactor struct {
	bus        *events.Bus
	sessionDir string
	sessionID  string
}

// New 创建压缩器。
func New(bus *events.Bus, sessionDir, sessionID string) *Compactor {
	return &Compactor{bus: bus, sessionDir: sessionDir, sessionID: sessionID}
}

// Compact 就地压缩 execCtx 的历史：把 Messages 替换为「摘要 + 确认」两条，
// 落盘摘要文件并发布 context.compacted。
// 调用点必须保证替换后的序列对下一次 LLM 调用合法——历史以工具结果收尾时才是安全的。
func (c *Compactor) Compact(ctx context.Context, execCtx *execctx.ExecutionContext, provider llm.Provider, focus string) (*Result, error) {
	result, err := c.CompactMessages(ctx, execCtx.Messages, provider, focus)
	if err != nil {
		return nil, err
	}

	execCtx.Messages = []llm.Message{
		llm.NewTextMessage("user", result.SummaryText),
		llm.NewTextMessage("assistant", "Understood, I'll continue from this summary."),
	}
	execCtx.Compacted = true
	c.writeSummary(result.SummaryText)

	if c.bus != nil {
		_ = c.bus.Publish(ctx, events.ContextCompacted{
			RunID:          execCtx.RunID,
			SessionID:      c.sessionID,
			OriginalTokens: result.OriginalTokenEstimate,
			SummaryTokens:  result.SummaryTokens,
		})
	}
	slog.Info("context compacted",
		"session_id", c.sessionID,
		"run_id", execCtx.RunID,
		"original_tokens", result.OriginalTokenEstimate,
		"summary_tokens", result.SummaryTokens,
	)
	return result, nil
}

// CompactMessages 只产出摘要，不改动消息列表（供 session.compact 手动压缩使用）。
func (c *Compactor) CompactMessages(ctx context.Context, messages []llm.Message, provider llm.Provider, focus string) (*Result, error) {
	originalEstimate := estimateTokens(messages)
	historyText := messagesToText(messages)

	prompt := compactPrompt
	if focus != "" {
		prompt += "\n\nIMPORTANT: Pay special attention to: " + focus
	}

	req := []llm.Message{
		{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: prompt + "\n\n---\n\n" + historyText}}},
	}

	resp, err := provider.Chat(ctx, req, "You are a helpful assistant that summarizes conversations.", nil, nil)
	if err != nil {
		return nil, err
	}

	summaryText := strings.TrimSpace(resp.Text)
	if summaryText == "" {
		return nil, fmt.Errorf("compactor returned empty summary")
	}

	summaryTokens := len(summaryText) / 4

	return &Result{
		SummaryText:           summaryText,
		OriginalTokenEstimate: originalEstimate,
		SummaryTokens:         summaryTokens,
	}, nil
}

// writeSummary 把摘要写入会话目录的 summary_<ts>.md，供事后回溯。
func (c *Compactor) writeSummary(text string) {
	if c.sessionDir == "" {
		return
	}
	if err := os.MkdirAll(c.sessionDir, 0755); err != nil {
		slog.Warn("compactor: failed to create session dir", "dir", c.sessionDir, "error", err)
		return
	}
	name := "summary_" + time.Now().UTC().Format("20060102_150405") + ".md"
	if err := os.WriteFile(filepath.Join(c.sessionDir, name), []byte(text), 0644); err != nil {
		slog.Warn("compactor: failed to write summary file", "error", err)
	}
}

func estimateTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		for _, c := range m.Content {
			switch c.Type {
			case "text":
				total += len(c.Text) / 4
			case "tool_result":
				total += len(c.Content) / 4
			}
		}
	}
	return total
}

func messagesToText(messages []llm.Message) string {
	var parts []string
	for _, msg := range messages {
		var blocks []string
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				blocks = append(blocks, block.Text)
			case "tool_use":
				blocks = append(blocks, fmt.Sprintf("<tool_call name=%s id=%s>\n%+v\n</tool_call>", block.Name, block.ID, block.Input))
			case "tool_result":
				blocks = append(blocks, fmt.Sprintf("<tool_result id=%s>\n%s\n</tool_result>", block.ToolUseID, block.Content))
			}
		}
		parts = append(parts, fmt.Sprintf("[%s]\n%s", strings.ToUpper(msg.Role), strings.Join(blocks, "\n")))
	}
	return strings.Join(parts, "\n\n")
}
