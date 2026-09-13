package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/youngyangyang04/numbat/internal/tools"
)

// writeFileMaxBytes 是单次写入的内容上限。
const writeFileMaxBytes = 1 * 1024 * 1024

// WriteFileTool 写入文件内容。
type WriteFileTool struct{}

// Name 返回工具名。
func (WriteFileTool) Name() string { return "write_file" }

// Description 返回工具描述。
func (WriteFileTool) Description() string {
	return "Write text content to a file, creating it (and any parent directories) if it " +
		"does not exist, or overwriting it if it does. " +
		"Path must be relative to the current working directory. " +
		"Content size is limited to 1 MB."
}

// InputSchema 返回参数 schema。
func (WriteFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative path to the file (relative to current working directory).",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Text content to write.",
			},
		},
		"required": []string{"path", "content"},
	}
}

// Invoke 执行工具：超 1MB 拒绝，自动创建父目录。
func (WriteFileTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	pathStr, _ := params["path"].(string)
	if pathStr == "" {
		return tools.Result{Content: "path is required", IsError: true, ErrorType: "schema_error"}, nil
	}
	// content 缺失或类型不符都按参数错误处理：静默转成 "" 会写出一个空文件并
	// 汇报成功，属于把错误伪装成有效结果。
	content, ok := params["content"].(string)
	if !ok {
		return tools.Result{Content: "content must be a string", IsError: true, ErrorType: "schema_error"}, nil
	}

	if hasParentRef(pathStr) {
		return tools.Result{Content: fmt.Sprintf("path traversal not allowed: %s", pathStr), IsError: true, ErrorType: "runtime_error"}, nil
	}

	if len(content) > writeFileMaxBytes {
		return tools.Result{
			Content:   fmt.Sprintf("content too large: %d bytes (limit 1 MB)", len(content)),
			IsError:   true,
			ErrorType: "runtime_error",
		}, nil
	}

	path := filepath.FromSlash(pathStr)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}
	return tools.Result{Content: fmt.Sprintf("wrote %d bytes to %s", len(content), pathStr)}, nil
}
