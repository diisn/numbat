package compact

import (
	"fmt"

	"github.com/youngyangyang04/numbat/internal/llm"
)

// toolResultLimit 是触发截断的长度阈值。
var toolResultLimit = 8000

// toolResultKeep 是截断后保留的前缀长度。
var toolResultKeep = 4000

// SetBudget 配置截断阈值，由 app.go 启动时调用。
// keep 会被钳到不超过 limit：否则「保留前缀」长于「触发阈值」，
// 截断时 text[:keep] 会越界 panic。
func SetBudget(limit, keep int) {
	if limit > 0 {
		toolResultLimit = limit
	}
	if keep > 0 {
		toolResultKeep = keep
	}
	if toolResultKeep > toolResultLimit {
		toolResultKeep = toolResultLimit
	}
}

// TruncateToolResults 对消息列表中超长的 tool_result 内容做内存截断，返回处理后的新列表。
func TruncateToolResults(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "user" {
			out = append(out, msg)
			continue
		}
		changed := false
		blocks := make([]llm.ContentBlock, len(msg.Content))
		copy(blocks, msg.Content)
		for i, block := range blocks {
			if block.Type != "tool_result" {
				continue
			}
			text := block.Content
			if len(text) > toolResultLimit {
				omitted := len(text) - toolResultKeep
				blocks[i].Content = fmt.Sprintf("%s\n[... %d chars omitted. Full output in run events.]", text[:toolResultKeep], omitted)
				changed = true
			}
		}
		if !changed {
			out = append(out, msg)
			continue
		}
		out = append(out, llm.Message{Role: msg.Role, Content: blocks})
	}
	return out
}
