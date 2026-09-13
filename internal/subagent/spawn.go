package subagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/youngyangyang04/numbat/internal/agents"
	execctx "github.com/youngyangyang04/numbat/internal/context"
	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/llm"
	"github.com/youngyangyang04/numbat/internal/loop"
	"github.com/youngyangyang04/numbat/internal/permissions"
	"github.com/youngyangyang04/numbat/internal/task"
	"github.com/youngyangyang04/numbat/internal/tools"
	"github.com/youngyangyang04/numbat/internal/tools/builtin"
	"github.com/youngyangyang04/numbat/internal/util"
)

// maxSubagentDepth 是子 Agent 最大嵌套深度（根为 0），可通过 SetMaxDepth 配置。
var maxSubagentDepth = 2

// SetMaxDepth 配置子 Agent 最大嵌套深度，由 app.go 启动时调用。
func SetMaxDepth(depth int) {
	if depth > 0 {
		maxSubagentDepth = depth
	}
}

// backgroundTask 表示一个后台运行中的子 Agent 任务。
// err 记录循环错误或 panic，供 agent_result 区分「失败」与「完成但无输出」。
type backgroundTask struct {
	done chan struct{}
	ctx  *execctx.ExecutionContext
	err  error
}

// TaskRegistry 管理后台子 Agent 任务的生命周期。
type TaskRegistry struct {
	mu    sync.Mutex
	tasks map[string]*backgroundTask
}

// NewTaskRegistry 创建任务注册表。
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{tasks: make(map[string]*backgroundTask)}
}

// Register 注册一个后台任务。
func (r *TaskRegistry) Register(runID string, t *backgroundTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[runID] = t
}

// Get 查询后台任务。
func (r *TaskRegistry) Get(runID string) (*backgroundTask, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[runID]
	return t, ok
}

// SpawnAgentTool 在隔离的冷启动上下文中派生子 Agent，支持前台阻塞和后台并行两种模式。
type SpawnAgentTool struct {
	provider      llm.Provider
	bus           *events.Bus
	perm          *permissions.Manager
	parentRunID   string
	sessionID     string
	maxSteps      int
	timeout       time.Duration
	tasks         *TaskRegistry
	depth         int
	profileLoader *agents.Loader // 角色配置加载器（nil 时不启用 subagent_type）
	runsDir       string         // per-run 目录根；子 agent 的任务存 <runsDir>/<runID>/.tasks
}

// Option 是 SpawnAgentTool 的构造选项。
type Option func(*SpawnAgentTool)

// WithProfileLoader 注入 Agent Profile 加载器，启用 subagent_type 角色派生。
func WithProfileLoader(loader *agents.Loader) Option {
	return func(t *SpawnAgentTool) { t.profileLoader = loader }
}

// WithRunsDir 设置 per-run 目录根，子 agent 在此创建独立的 .tasks 目录。
func WithRunsDir(dir string) Option {
	return func(t *SpawnAgentTool) { t.runsDir = dir }
}

// WithSessionID 设置所属会话 ID，随子 Agent 的权限事件下发（无会话时为空）。
func WithSessionID(sessionID string) Option {
	return func(t *SpawnAgentTool) { t.sessionID = sessionID }
}

// NewSpawnAgentTool 创建 spawn_agent 工具。
func NewSpawnAgentTool(provider llm.Provider, bus *events.Bus, perm *permissions.Manager, parentRunID string, maxSteps int, timeout time.Duration, tasks *TaskRegistry, depth int, opts ...Option) *SpawnAgentTool {
	t := &SpawnAgentTool{
		provider:    provider,
		bus:         bus,
		perm:        perm,
		parentRunID: parentRunID,
		maxSteps:    maxSteps,
		timeout:     timeout,
		tasks:       tasks,
		depth:       depth,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Name 返回工具名。
func (t *SpawnAgentTool) Name() string { return "spawn_agent" }

// Description 返回工具描述。
func (t *SpawnAgentTool) Description() string {
	return "Spawn an isolated sub-agent to handle a self-contained sub-task. " +
		"The sub-agent starts with a clean context containing only the provided prompt — " +
		"it does not inherit the current conversation history. " +
		"Use run_in_background=true to run in parallel; retrieve result later with agent_result."
}

// InputSchema 返回参数 schema。
func (t *SpawnAgentTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"description": map[string]any{
				"type":        "string",
				"description": "3-5 word task description shown in progress display",
			},
			"prompt": map[string]any{
				"type": "string",
				"description": "Complete task description including all context the sub-agent needs. " +
					"The sub-agent cannot see the parent conversation, so be explicit.",
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "When true, returns immediately with a run_id; use agent_result to poll.",
			},
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "Agent role profile (planner/executor/reviewer). Leave empty for default.",
			},
		},
		"required": []string{"description", "prompt"},
	}
}

// Invoke 执行子 Agent 派生。
func (t *SpawnAgentTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	description, _ := params["description"].(string)
	prompt, _ := params["prompt"].(string)
	runInBackground, _ := params["run_in_background"].(bool)
	subagentType, _ := params["subagent_type"].(string)

	if description == "" || prompt == "" {
		return tools.Result{Content: "description and prompt are required", IsError: true, ErrorType: "schema_error"}, nil
	}

	// 角色 Profile：指定 subagent_type 时加载角色配置（system prompt + 工具白名单）；
	// 未指定或未找到（与 Python 版一致，静默降级）时按默认子 Agent 运行。
	var profile *agents.Profile
	if subagentType != "" && t.profileLoader != nil {
		profile = t.profileLoader.Load(subagentType)
	}

	if t.depth >= maxSubagentDepth {
		return tools.Result{
			Content:   fmt.Sprintf("Subagent nesting limit (%d) reached; cannot spawn further subagents.", maxSubagentDepth),
			IsError:   true,
			ErrorType: "runtime_error",
		}, nil
	}

	childRunID := util.GenerateRunID()
	childCtx := execctx.NewExecutionContext(childRunID, prompt, t.maxSteps)
	childCtx.AddUserMessage(prompt)

	// 角色 Profile 指定时覆盖基础 system prompt（与 Python 版 system_prompt_override 一致）。
	// 子 Agent 不注入记忆层——冷启动上下文只含 prompt 与角色设定。
	if profile != nil {
		childCtx.SystemPromptOverride = profile.SystemPrompt
	}
	childSystem := childCtx.SystemPrompt(llm.DefaultSystemPrompt)

	// 子 Agent 事件直接发到父 bus，自动广播给所有订阅者（TUI 等）。
	childInvoker := tools.NewInvoker(t.buildChildRegistry(childRunID, profile), t.perm, t.bus, t.timeout,
		tools.WithSessionID(t.sessionID))
	childLoop := loop.New(t.provider, childInvoker, t.bus)

	_ = t.bus.Publish(ctx, events.SubagentStarted{
		RunID:       childRunID,
		ParentRunID: t.parentRunID,
		Description: description,
	})

	if runInBackground {
		task := &backgroundTask{done: make(chan struct{}), ctx: childCtx}
		t.tasks.Register(childRunID, task)
		go func() {
			defer close(task.done)
			// 后台子 Agent 使用独立 context，不随父 run 的 ctx 取消而终止
			task.err = t.runChild(context.Background(), childLoop, childCtx, prompt, childRunID, childSystem)
		}()
		return tools.Result{
			Content: fmt.Sprintf("Subagent started in background. run_id=%s. "+
				"Use agent_result(run_id='%s') to retrieve result.", childRunID, childRunID),
		}, nil
	}

	// 前台调用的成败由 runChild 写入 childCtx；返回的 error 仅用于标识「被取消」，
	// 结果文案统一从 childCtx 派生，故此处无需再判 err。
	_ = t.runChild(ctx, childLoop, childCtx, prompt, childRunID, childSystem)

	if childCtx.Status == "success" {
		result := childCtx.Result
		if result == "" {
			result = "Subagent completed with no text output."
		}
		return tools.Result{Content: result}, nil
	}
	return tools.Result{Content: failedSubagentMessage(childCtx), IsError: true, ErrorType: "runtime_error"}, nil
}

// failedSubagentMessage 汇总子 Agent 的失败信息。
func failedSubagentMessage(childCtx *execctx.ExecutionContext) string {
	if childCtx.Result != "" {
		return childCtx.Result
	}
	if childCtx.Reason != "" {
		return fmt.Sprintf("Subagent failed (status=%s, reason=%s)", childCtx.Status, childCtx.Reason)
	}
	return fmt.Sprintf("Subagent failed (status=%s)", childCtx.Status)
}

// runChild 运行子 Agent 循环并发布收尾事件。
//
// 收尾事件在 defer 中发布：无论循环正常结束、被取消还是 panic，订阅者都不会
// 只看到 subagent.started 而等不到 finished。panic 被兜底转成 error，避免带崩进程。
func (t *SpawnAgentTool) runChild(ctx context.Context, childLoop *loop.AgentLoop, childCtx *execctx.ExecutionContext, prompt, childRunID, system string) (err error) {
	_ = t.bus.Publish(ctx, events.RunStarted{RunID: childRunID, Goal: prompt})

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("subagent panicked: %v", r)
			childCtx.MarkFailed("panic", err.Error())
		}
		_ = t.bus.Publish(ctx, events.RunFinished{
			RunID:  childRunID,
			Status: childCtx.Status,
			Result: childCtx.Result,
			Reason: childCtx.Reason,
			Steps:  childCtx.Step,
		})
		_ = t.bus.Publish(ctx, events.SubagentFinished{
			RunID:       childRunID,
			ParentRunID: t.parentRunID,
			Status:      childCtx.Status,
		})
	}()

	return childLoop.Run(ctx, childCtx, system)
}

// buildChildRegistry 构造子 Agent 可用的工具注册表。
// 有角色 Profile 时按 allowed_tools 白名单过滤工具（与 Python 版一致：空白名单 = 全部放行）。
// 深度允许时注册嵌套 spawn_agent / agent_result。
func (t *SpawnAgentTool) buildChildRegistry(childRunID string, profile *agents.Profile) *tools.Registry {
	allowed := make(map[string]bool)
	if profile != nil {
		for _, name := range profile.AllowedTools {
			allowed[name] = true
		}
	}
	ok := func(name string) bool {
		return len(allowed) == 0 || allowed[name]
	}

	registry := tools.NewRegistry()
	if ok("read_file") {
		registry.Register(builtin.ReadFileTool{})
	}
	if ok("list_dir") {
		registry.Register(builtin.ListDirTool{})
	}
	if ok("write_file") {
		registry.Register(builtin.WriteFileTool{})
	}
	if ok("bash") {
		registry.Register(builtin.BashTool{})
	}

	// 任务工具：子 Agent 独立 <runsDir>/<childRunID>/.tasks 目录（与 Python 版 per-run 隔离一致）
	if t.runsDir != "" {
		if taskManager, err := task.NewManager(filepath.Join(t.runsDir, childRunID, ".tasks")); err == nil {
			if ok("task_create") {
				registry.Register(builtin.TaskCreateTool{Manager: taskManager})
			}
			if ok("task_update") {
				registry.Register(builtin.TaskUpdateTool{Manager: taskManager})
			}
			if ok("task_list") {
				registry.Register(builtin.TaskListTool{Manager: taskManager})
			}
			if ok("task_get") {
				registry.Register(builtin.TaskGetTool{Manager: taskManager})
			}
		} else {
			// 任务工具缺失不阻断子 Agent，但必须留痕：否则会静默失去全部 task_* 能力。
			slog.Warn("subagent: task manager unavailable", "run_id", childRunID, "err", err)
		}
	}

	if t.depth+1 < maxSubagentDepth {
		if ok("spawn_agent") {
			registry.Register(NewSpawnAgentTool(t.provider, t.bus, t.perm, childRunID, t.maxSteps, t.timeout, t.tasks, t.depth+1,
				WithProfileLoader(t.profileLoader), WithRunsDir(t.runsDir), WithSessionID(t.sessionID)))
		}
		if ok("agent_result") {
			registry.Register(NewAgentResultTool(t.tasks))
		}
	}
	return registry
}

// AgentResultTool 查询后台子 Agent 的执行状态和最终结果。
type AgentResultTool struct {
	tasks *TaskRegistry
}

// NewAgentResultTool 创建 agent_result 工具。
func NewAgentResultTool(tasks *TaskRegistry) *AgentResultTool {
	return &AgentResultTool{tasks: tasks}
}

// Name 返回工具名。
func (t *AgentResultTool) Name() string { return "agent_result" }

// Description 返回工具描述。
func (t *AgentResultTool) Description() string {
	return "Retrieve the result of a background sub-agent previously started with spawn_agent. " +
		"Returns 'still running' if the sub-agent has not yet completed."
}

// InputSchema 返回参数 schema。
func (t *AgentResultTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"run_id": map[string]any{
				"type":        "string",
				"description": "The run_id returned by spawn_agent(run_in_background=true)",
			},
		},
		"required": []string{"run_id"},
	}
}

// Invoke 查询后台任务结果：区分「仍在运行」「被取消」「异常」「已完成」四态。
func (t *AgentResultTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	runID, _ := params["run_id"].(string)
	task, ok := t.tasks.Get(runID)
	if !ok {
		return tools.Result{
			Content:   fmt.Sprintf("Unknown run_id: %s. Only background subagents can be queried.", runID),
			IsError:   true,
			ErrorType: "runtime_error",
		}, nil
	}
	select {
	case <-task.done:
	default:
		return tools.Result{Content: "still running"}, nil
	}

	res := tools.Result{Content: task.ctx.Result}
	if res.Content == "" {
		res.Content = "Subagent completed with no text result."
	}
	switch {
	case errors.Is(task.err, context.Canceled):
		return tools.Result{Content: "Subagent was cancelled.", IsError: true, ErrorType: "runtime_error"}, nil
	case task.err != nil:
		return tools.Result{
			Content:   fmt.Sprintf("Subagent failed: %v", task.err),
			IsError:   true,
			ErrorType: "runtime_error",
		}, nil
	case task.ctx.Status == "failed":
		return tools.Result{Content: failedSubagentMessage(task.ctx), IsError: true, ErrorType: "runtime_error"}, nil
	}
	return res, nil
}
