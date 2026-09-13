package builtin

import (
	"context"
	"encoding/json"

	"github.com/youngyangyang04/numbat/internal/task"
	"github.com/youngyangyang04/numbat/internal/tools"
)

// TaskCreateTool 创建任务。
type TaskCreateTool struct {
	Manager *task.Manager
}

// Name 返回工具名。
func (t TaskCreateTool) Name() string { return "task_create" }

// Description 返回工具描述。
func (t TaskCreateTool) Description() string {
	return "Create a new task to track a unit of work. " +
		"Use this to break down a complex goal into smaller, trackable steps. " +
		"Returns the created task as JSON."
}

// InputSchema 返回参数 schema。
func (t TaskCreateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject": map[string]any{
				"type":        "string",
				"description": "Short title for the task.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional longer description of what needs to be done.",
			},
			"blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "IDs of tasks that must be completed before this one.",
			},
		},
		"required": []string{"subject"},
	}
}

// Invoke 执行工具。
func (t TaskCreateTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	subject, _ := params["subject"].(string)
	if subject == "" {
		return tools.Result{Content: "subject is required", IsError: true, ErrorType: "schema_error"}, nil
	}
	description, _ := params["description"].(string)
	var blockedBy []int
	if raw, ok := params["blocked_by"].([]any); ok {
		for _, v := range raw {
			if f, ok := v.(float64); ok {
				blockedBy = append(blockedBy, int(f))
			}
		}
	}
	taskObj, err := t.Manager.Create(subject, description, blockedBy)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}
	data, _ := json.Marshal(taskObj)
	return tools.Result{Content: string(data)}, nil
}

// TaskUpdateTool 更新任务状态或依赖。
type TaskUpdateTool struct {
	Manager *task.Manager
}

// Name 返回工具名。
func (t TaskUpdateTool) Name() string { return "task_update" }

// Description 返回工具描述。
func (t TaskUpdateTool) Description() string {
	return "Update a task's status or dependency list. " +
		"Set status to 'in_progress' when starting work on a task, " +
		"'completed' when finished (automatically clears it from other tasks' blocked_by). " +
		"Returns the updated task as JSON."
}

// InputSchema 返回参数 schema。
func (t TaskUpdateTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "integer",
				"description": "ID of the task to update.",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"pending", "in_progress", "completed"},
				"description": "New status for the task.",
			},
			"add_blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Task IDs to add to blocked_by.",
			},
			"remove_blocked_by": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Task IDs to remove from blocked_by.",
			},
		},
		"required": []string{"task_id"},
	}
}

// Invoke 执行工具。
func (t TaskUpdateTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	taskID := toInt(params["task_id"])
	if taskID == 0 {
		return tools.Result{Content: "task_id is required", IsError: true, ErrorType: "schema_error"}, nil
	}
	status, _ := params["status"].(string)
	addBlocked := toIntSlice(params["add_blocked_by"])
	removeBlocked := toIntSlice(params["remove_blocked_by"])

	taskObj, err := t.Manager.Update(taskID, status, addBlocked, removeBlocked)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}
	data, _ := json.Marshal(taskObj)
	return tools.Result{Content: string(data)}, nil
}

// TaskListTool 列出所有任务。
type TaskListTool struct {
	Manager *task.Manager
}

// Name 返回工具名。
func (t TaskListTool) Name() string { return "task_list" }

// Description 返回工具描述。
func (t TaskListTool) Description() string {
	return "List all tasks with their current status and blocking dependencies. " +
		"Use this to check what work remains and what can be started next."
}

// InputSchema 返回参数 schema。
func (t TaskListTool) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

// Invoke 执行工具。
func (t TaskListTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	return tools.Result{Content: t.Manager.FormatList()}, nil
}

// TaskGetTool 查询单个任务详情。
type TaskGetTool struct {
	Manager *task.Manager
}

// Name 返回工具名。
func (t TaskGetTool) Name() string { return "task_get" }

// Description 返回工具描述。
func (t TaskGetTool) Description() string {
	return "Get full details of a task by its integer ID. Returns the task as JSON."
}

// InputSchema 返回参数 schema。
func (t TaskGetTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "integer",
				"description": "ID of the task to retrieve.",
			},
		},
		"required": []string{"task_id"},
	}
}

// Invoke 执行工具。
func (t TaskGetTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	taskID := toInt(params["task_id"])
	if taskID == 0 {
		return tools.Result{Content: "task_id is required", IsError: true, ErrorType: "schema_error"}, nil
	}
	taskObj, err := t.Manager.Get(taskID)
	if err != nil {
		return tools.Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}, nil
	}
	data, _ := json.Marshal(taskObj)
	return tools.Result{Content: string(data)}, nil
}

// toInt 从 any 参数提取整数（JSON 数字反序列化为 float64）。
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// toIntSlice 从 any 参数提取整数切片。
func toIntSlice(v any) []int {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []int
	for _, item := range arr {
		if n := toInt(item); n != 0 {
			out = append(out, n)
		}
	}
	return out
}
