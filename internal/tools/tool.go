package tools

import (
	"context"
	"fmt"
	"sync"

	"github.com/youngyangyang04/numbat/internal/llm"
)

// Result 表示工具调用结果。
type Result struct {
	Content   string
	IsError   bool
	ErrorType string
}

// Tool 是工具接口。
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Invoke(ctx context.Context, params map[string]any) (Result, error)
}

// Registry 是工具注册表。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry 创建工具注册表。
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register 注册工具。
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tools[tool.Name()] = tool
}

// Get 按名称获取工具。
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.tools[name]
}

// Filtered 返回只包含 allowed 集合内工具的新注册表。
// allowed 为空表示不过滤（返回全量拷贝）；工具实例共享（无状态，可安全复用）。
func (r *Registry) Filtered(allowed []string) *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n := NewRegistry()
	if len(allowed) == 0 {
		for name, t := range r.tools {
			n.tools[name] = t
		}
		return n
	}
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	for name, t := range r.tools {
		if set[name] {
			n.tools[name] = t
		}
	}
	return n
}

// ToolDefinitions 返回所有工具的 LLM schema。
func (r *Registry) ToolDefinitions() []llm.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var defs []llm.ToolDefinition
	for _, t := range r.tools {
		defs = append(defs, llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

// validateParams 按工具的 input_schema 做轻量校验：只检查 required 字段是否齐备。
// 目的是在权限审批之前拦掉明显非法的调用，
// 不引入完整的 JSON Schema 实现——类型与取值范围仍由各工具自行兜底。
func validateParams(tool Tool, params map[string]any) error {
	for _, name := range requiredFields(tool.InputSchema()) {
		if value, ok := params[name]; !ok || value == nil {
			return fmt.Errorf("missing required parameter: %s", name)
		}
	}
	return nil
}

// requiredFields 从 input_schema 中取出 required 字段名。
// 兼容内建工具的 []string 与 MCP 工具经 JSON 反序列化得到的 []any。
func requiredFields(schema map[string]any) []string {
	switch raw := schema["required"].(type) {
	case []string:
		return raw
	case []any:
		names := make([]string, 0, len(raw))
		for _, item := range raw {
			if name, ok := item.(string); ok {
				names = append(names, name)
			}
		}
		return names
	default:
		return nil
	}
}
