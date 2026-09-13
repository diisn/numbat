package builtin

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/youngyangyang04/numbat/internal/tools"
)

const maxReadBytes = 512 * 1024

// ReadFileTool 读取文件内容。
type ReadFileTool struct{}

// Name 返回工具名。
func (ReadFileTool) Name() string { return "read_file" }

// Description 返回工具描述。
func (ReadFileTool) Description() string {
	return "Read the text content of a file. Path must be relative to the current working directory. Files larger than 512 KB are truncated."
}

// InputSchema 返回参数 schema。
func (ReadFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative path to the file (relative to current working directory).",
			},
		},
		"required": []string{"path"},
	}
}

// Invoke 执行工具。
func (ReadFileTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	path, ok := params["path"].(string)
	if !ok || path == "" {
		return tools.Result{Content: "path is required", IsError: true, ErrorType: "schema_error"}, nil
	}
	if hasParentRef(path) {
		return tools.Result{Content: fmt.Sprintf("path traversal not allowed: %s", path), IsError: true, ErrorType: "runtime_error"}, nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}
	if info.IsDir() {
		return tools.Result{Content: "path is a directory", IsError: true, ErrorType: "runtime_error"}, nil
	}

	f, err := os.Open(absPath)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}
	defer f.Close()

	// 只读 上限+1 字节：既避免把超大文件整个载入内存，又能判断是否被截断。
	data, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}

	truncated := len(data) > maxReadBytes
	if truncated {
		data = data[:maxReadBytes]
	}
	content := string(data)
	if truncated {
		content += "\n[truncated]"
	}
	return tools.Result{Content: content}, nil
}
