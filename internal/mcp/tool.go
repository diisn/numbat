package mcp

import (
	"context"

	"github.com/youngyangyang04/numbat/internal/tools"
)

// Tool 把 MCP 工具适配为 tools.Tool 接口。
type Tool struct {
	def    ToolDef
	client *Client
}

// NewTool 创建 MCP 工具适配器。
func NewTool(client *Client, def ToolDef) *Tool {
	return &Tool{def: def, client: client}
}

// Name 返回工具名。
func (t *Tool) Name() string { return t.def.Name }

// Description 返回工具描述。
func (t *Tool) Description() string { return t.def.Description }

// InputSchema 返回参数 schema。
func (t *Tool) InputSchema() map[string]any { return t.def.InputSchema }

// Invoke 调用 MCP server 上的工具。
func (t *Tool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	out, err := t.client.CallTool(ctx, t.def.Name, params)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}
	return tools.Result{Content: out}, nil
}
