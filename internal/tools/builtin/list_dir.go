package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/youngyangyang04/numbat/internal/tools"
)

const (
	// listDirMaxDepth 是允许的最大递归深度。
	listDirMaxDepth = 4
	// listDirDefaultDepth 是默认递归深度。
	listDirDefaultDepth = 2
	// listDirMaxEntries 是单次列出的条目上限。
	listDirMaxEntries = 200
)

// ListDirTool 以树状格式列出目录内容。
type ListDirTool struct{}

// Name 返回工具名。
func (ListDirTool) Name() string { return "list_dir" }

// Description 返回工具描述。
func (ListDirTool) Description() string {
	return "List the contents of a directory as a tree. " +
		"Path must be relative to the current working directory. " +
		"Hidden entries (starting with .) are included. " +
		fmt.Sprintf("Maximum depth is %d, maximum total entries is %d.", listDirMaxDepth, listDirMaxEntries)
}

// InputSchema 返回参数 schema。
func (ListDirTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative path to the directory (default '.').",
			},
			"max_depth": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("How many levels deep to recurse (default %d, max %d).", listDirDefaultDepth, listDirMaxDepth),
				"minimum":     1,
				"maximum":     listDirMaxDepth,
			},
		},
		"required": []string{},
	}
}

// Invoke 执行工具：目录在前、按名排序，深度与条目数均有上限。
func (ListDirTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	pathStr, _ := params["path"].(string)
	if pathStr == "" {
		pathStr = "."
	}
	maxDepth := listDirDefaultDepth
	if n, ok := params["max_depth"].(float64); ok && n > 0 {
		maxDepth = min(int(n), listDirMaxDepth)
	}

	if hasParentRef(pathStr) {
		return tools.Result{Content: fmt.Sprintf("path traversal not allowed: %s", pathStr), IsError: true, ErrorType: "runtime_error"}, nil
	}

	root := filepath.FromSlash(pathStr)
	info, err := os.Stat(root)
	if err != nil {
		return tools.Result{Content: fmt.Sprintf("no such directory: %s", pathStr), IsError: true, ErrorType: "runtime_error"}, nil
	}
	if !info.IsDir() {
		return tools.Result{Content: fmt.Sprintf("not a directory: %s", pathStr), IsError: true, ErrorType: "runtime_error"}, nil
	}

	lines := []string{filepath.ToSlash(root) + "/"}
	count := 0

	var walk func(dir string, depth int, prefix string)
	walk = func(dir string, depth int, prefix string) {
		if depth > maxDepth || count >= listDirMaxEntries {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// 不能静默 return：否则「无权限目录」与「空目录」在输出里无法区分。
			lines = append(lines, prefix+"[error: "+err.Error()+"]")
			return
		}
		sortEntries(entries)

		for i, entry := range entries {
			if count >= listDirMaxEntries {
				lines = append(lines, prefix+"... (truncated)")
				return
			}
			connector := "├── "
			if i == len(entries)-1 {
				connector = "└── "
			}
			suffix := ""
			if entry.IsDir() {
				suffix = "/"
			}
			lines = append(lines, prefix+connector+entry.Name()+suffix)
			count++

			if entry.IsDir() && depth < maxDepth {
				extension := "│   "
				if i == len(entries)-1 {
					extension = "    "
				}
				walk(filepath.Join(dir, entry.Name()), depth+1, prefix+extension)
			}
		}
	}
	walk(root, 1, "")

	return tools.Result{Content: strings.Join(lines, "\n")}, nil
}

// sortEntries 按「目录优先，其次名称」排序，保证树状输出稳定可读。
func sortEntries(entries []os.DirEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
}
