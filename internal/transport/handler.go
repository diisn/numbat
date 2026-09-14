package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/youngyangyang04/numbat/internal/agents"
	"github.com/youngyangyang04/numbat/internal/compact"
	execctx "github.com/youngyangyang04/numbat/internal/context"
	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/llm"
	"github.com/youngyangyang04/numbat/internal/loop"
	"github.com/youngyangyang04/numbat/internal/memory"
	"github.com/youngyangyang04/numbat/internal/permissions"
	"github.com/youngyangyang04/numbat/internal/session"
	"github.com/youngyangyang04/numbat/internal/skills"
	"github.com/youngyangyang04/numbat/internal/subagent"
	"github.com/youngyangyang04/numbat/internal/task"
	"github.com/youngyangyang04/numbat/internal/tools"
	"github.com/youngyangyang04/numbat/internal/tools/builtin"
	"github.com/youngyangyang04/numbat/internal/trace"
	"github.com/youngyangyang04/numbat/internal/util"
)

// HandlerDeps 是创建 RPC handler 所需的依赖。
type HandlerDeps struct {
	Provider    llm.Provider
	MaxSteps    int
	Invoker     *tools.Invoker // 基础工具注册表，run 级工具由此派生（见 runToolInvoker）
	Bus         *events.Bus
	Perm        *permissions.Manager
	Session     *session.Manager
	ToolTimeout time.Duration // 工具执行超时；skill 白名单构建临时 invoker 时沿用
	RunsDir     string        // run 轨迹根目录；每个 run 的任务目录为 <RunsDir>/<runID>/.tasks
	RunCanceler *runCanceler  // run 级取消注册表，供 agent.abort 使用
	// CompactThreshold 是 run 中途自动压缩的 context_pct 阈值（0 = 禁用）。
	CompactThreshold float64
	// 子 Agent 依赖（跨 run 共享），非 nil 时 runToolInvoker 按当前 runID 注册 spawn_agent/agent_result。
	SubagentTasks  *subagent.TaskRegistry
	SubagentLoader *agents.Loader
}

// AgentRunParams 是 agent.run 的参数。
type AgentRunParams struct {
	Goal string `json:"goal"`
}

// AgentAbortParams 是 agent.abort 的参数。
type AgentAbortParams struct {
	RunID string `json:"run_id"`
}

// PermissionRespondParams 是 permission.respond 的参数。
type PermissionRespondParams struct {
	ToolUseID string `json:"tool_use_id"`
	Decision  string `json:"decision"`
}

// SessionCreateParams 是 session.create 的参数。
type SessionCreateParams struct {
	Mode  string `json:"mode"`
	Title string `json:"title"`
}

// RunListParams 是 run.list 的参数。
type RunListParams struct {
	// Limit 为返回的 run 数量上限；<=0 时使用 trace.DefaultRunLimit。
	Limit int `json:"limit"`
}

// SessionSendMessageParams 是 session.send_message 的参数。
type SessionSendMessageParams struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
}

// SessionCompactParams 是 session.compact 的参数。
type SessionCompactParams struct {
	SessionID string `json:"session_id"`
}

// SkillInfo 是 skill.list 返回的单条技能摘要（JSON 字段与 WebUI SkillMeta 对齐）。
type SkillInfo struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	AllowedTools []string `json:"allowed_tools"`
}

// NewHandlers 创建所有 RPC 方法处理函数。
func NewHandlers(deps HandlerDeps) map[string]Handler {
	handlers := make(map[string]Handler)
	skillLoader := skills.NewLoader()

	handlers["core.ping"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		return "pong", nil
	}

	// skill.list 列出全部可用技能（内建 + 用户全局 + 项目本地），
	// 供前端斜杠命令补全与侧栏 Skills 列表使用。
	handlers["skill.list"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		all := skillLoader.ListAllSkills()
		out := make([]SkillInfo, 0, len(all))
		for _, s := range all {
			out = append(out, SkillInfo{Name: s.Name, Description: s.Description, AllowedTools: s.AllowedTools})
		}
		return out, nil
	}

	handlers["agent.run"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p AgentRunParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}

		runID := util.GenerateRunID()
		execCtx := execctx.NewExecutionContext(runID, p.Goal, deps.MaxSteps)
		execCtx.AddUserMessage(p.Goal)

		// 无会话的 run 同样注入记忆层。
		execCtx.GlobalContext, execCtx.ProjectContext = memory.LoadAll()
		system := execCtx.SystemPrompt(llm.DefaultSystemPrompt)

		_ = deps.Bus.Publish(ctx, events.RunStarted{
			RunID: runID,
			Goal:  p.Goal,
		})

		runDir := filepath.Join(deps.RunsDir, runID)
		agentLoop := newAgentLoop(deps, runToolInvoker(deps, runID, "", nil, nil), runDir, "")
		err := runWithCancellation(ctx, deps, execCtx, agentLoop, system)

		if err != nil {
			return nil, err
		}

		return map[string]any{
			"run_id": runID,
			"status": execCtx.Status,
			"result": execCtx.Result,
		}, nil
	}

	handlers["agent.abort"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p AgentAbortParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if p.RunID == "" {
			return nil, fmt.Errorf("run_id is required")
		}
		if deps.RunCanceler == nil {
			return false, nil
		}
		ok := deps.RunCanceler.Abort(p.RunID)
		return ok, nil
	}

	handlers["permission.respond"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p PermissionRespondParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if p.ToolUseID == "" || p.Decision == "" {
			return nil, fmt.Errorf("tool_use_id and decision are required")
		}
		ok := deps.Perm.Respond(p.ToolUseID, p.Decision)
		return ok, nil
	}

	handlers["session.create"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p SessionCreateParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		mode := session.Mode(p.Mode)
		if mode != session.ModeOneShot && mode != session.ModeChat {
			mode = session.ModeChat
		}
		sess, err := deps.Session.Create(mode, p.Title)
		if err != nil {
			return nil, err
		}
		return sess, nil
	}

	handlers["session.send_message"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p SessionSendMessageParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}

		sess, err := deps.Session.Get(p.SessionID)
		if err != nil {
			return nil, fmt.Errorf("session not found: %w", err)
		}
		if sess.Status == session.StatusClosed {
			return nil, fmt.Errorf("session is closed")
		}

		runID := util.GenerateRunID()

		history, err := deps.Session.Store().ReadMessages(sess.ID)
		if err != nil {
			return nil, err
		}

		if err := deps.Session.Store().AppendMessage(sess.ID, "user", []llm.ContentBlock{{Type: "text", Text: p.Content}}); err != nil {
			return nil, err
		}

		msgs := append(history, llm.NewTextMessage("user", p.Content))
		// prefillLen 标出本轮 run 的起点：循环之后追加的消息（assistant 的 tool_use、
		// user 的 tool_result）都是本次 run 的产物，收尾时一并落库。
		prefillLen := len(msgs)

		// Skill 解析："/name args" 展开为 skill 模板渲染后的目标。
		// 命中 skill 后：skill 的系统提示词作为 override，
		// allowed_tools 作为本次 run 的工具白名单。
		goal := p.Content
		var allowedTools []string
		systemOverride := ""
		if strings.HasPrefix(goal, "/") {
			skillName, args := splitSkillInvocation(goal)
			if skill, err := skillLoader.Resolve(skillName); err == nil {
				goal = skillLoader.RenderPrompt(skill, args)
				systemOverride = skill.SystemPromptTemplate
				allowedTools = skill.AllowedTools
				_ = deps.Bus.Publish(ctx, events.SkillInvoked{SkillName: skillName, Arguments: args, RunID: runID})
			}
		}

		execCtx := execctx.NewExecutionContext(runID, goal, deps.MaxSteps)
		execCtx.Messages = msgs
		execCtx.SystemPromptOverride = systemOverride
		execCtx.GlobalContext, execCtx.ProjectContext = memory.LoadAll()
		if notes, err := deps.Session.Store().ReadNotes(sess.ID); err == nil {
			execCtx.SessionNotes = notes
		} else {
			slog.Warn("session: failed to read notes", "session_id", sess.ID, "error", err)
		}
		system := execCtx.SystemPrompt(llm.DefaultSystemPrompt)

		noteSaver := &builtin.NoteSaveTool{Store: deps.Session.Store(), SessionID: sess.ID, RunID: runID}
		runInvoker := runToolInvoker(deps, runID, sess.ID, allowedTools, noteSaver)

		_ = deps.Bus.Publish(ctx, events.RunStarted{RunID: runID, Goal: goal})

		agentLoop := newAgentLoop(deps, runInvoker, deps.Session.Store().SessionDir(sess.ID), sess.ID)
		runErr := runWithCancellation(ctx, deps, execCtx, agentLoop, system)

		// 无论 run 成功、失败还是被中止，都要落库并更新会话状态。
		// 落整段新增消息而非只落最终答复，否则下一轮会丢失工具调用上下文。
		var newMessages []llm.Message
		if !execCtx.Compacted {
			newMessages = execCtx.Messages[prefillLen:]
		}
		if len(newMessages) == 0 && len(execCtx.FinalAssistant) > 0 {
			// 中途压缩后 Messages 已被换成「摘要 + 确认」，不是真实对话轮次；
			// 本轮也没有新消息时，退回只落最终答复。
			newMessages = []llm.Message{{Role: "assistant", Content: execCtx.FinalAssistant}}
		}
		if err := deps.Session.Store().AppendMessages(sess.ID, newMessages); err != nil {
			return nil, err
		}
		sess.RunIDs = append(sess.RunIDs, runID)
		sess.UpdatedAt = nowISO()
		if sess.Mode == session.ModeOneShot {
			sess.Status = session.StatusClosed
		} else {
			sess.Status = session.StatusWaitingForInput
		}
		if err := deps.Session.Update(sess); err != nil {
			slog.Warn("session: failed to update status", "session_id", sess.ID, "error", err)
		}
		// one_shot 会话到此终止，需要通知订阅者刷新侧栏。
		if sess.Status == session.StatusClosed {
			_ = deps.Bus.Publish(ctx, events.SessionClosed{SessionID: sess.ID})
		}

		if runErr != nil {
			return nil, runErr
		}

		return map[string]any{
			"run_id": runID,
			"status": execCtx.Status,
			"result": execCtx.Result,
		}, nil
	}

	handlers["session.get_history"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p SessionSendMessageParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		msgs, err := deps.Session.Store().ReadMessages(p.SessionID)
		if err != nil {
			return nil, err
		}
		return msgs, nil
	}

	handlers["session.list"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		return deps.Session.List(), nil
	}

	handlers["run.list"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p RunListParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}
		}
		limit := trace.DefaultRunLimit
		if p.Limit > 0 {
			limit = p.Limit
		}
		runs, total, err := trace.ListRuns(deps.RunsDir, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"runs": runs, "total": total}, nil
	}

	handlers["session.clear"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p SessionSendMessageParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if p.SessionID == "" {
			return nil, fmt.Errorf("session_id is required")
		}
		if err := deps.Session.Clear(p.SessionID); err != nil {
			return nil, err
		}
		return "cleared", nil
	}

	handlers["session.close"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p SessionSendMessageParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		if err := deps.Session.Close(p.SessionID); err != nil {
			return nil, err
		}
		return "closed", nil
	}

	handlers["session.compact"] = func(ctx context.Context, params json.RawMessage) (any, error) {
		var p SessionCompactParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("invalid params: %w", err)
		}
		msgs, err := deps.Session.Store().ReadMessages(p.SessionID)
		if err != nil {
			return nil, err
		}
		compactor := compact.New(deps.Bus, deps.Session.Store().SessionDir(p.SessionID), p.SessionID)
		result, err := compactor.CompactMessages(ctx, msgs, deps.Provider, "")
		if err != nil {
			return nil, err
		}
		if err := deps.Session.Store().WriteCompacted(p.SessionID, []llm.Message{
			llm.NewTextMessage("user", result.SummaryText),
			llm.NewTextMessage("assistant", "Understood, I'll continue from this summary."),
		}); err != nil {
			return nil, err
		}
		return map[string]any{
			"original_tokens": result.OriginalTokenEstimate,
			"summary_tokens":  result.SummaryTokens,
		}, nil
	}

	return handlers
}

// runToolInvoker 为一次 run 构造执行用的 Invoker：
// - 以基础注册表为模板全量拷贝（工具实例无状态，可安全共享）；
// - 任务工具重新绑定到 <RunsDir>/<runID>/.tasks（per-run 任务目录隔离）；
// - noteSaver 非 nil 时注册 note_save（会话场景绑定 Store/SessionID/RunID 以落库）；
// - allowedTools 非空时按白名单过滤（skill 触发场景，空白名单 = 全部放行）。
// sessionID 会随权限事件下发，也是会话级「总是允许」的缓存键，无会话时传空串。
func runToolInvoker(deps HandlerDeps, runID, sessionID string, allowedTools []string, noteSaver *builtin.NoteSaveTool) *tools.Invoker {
	reg := deps.Invoker.Registry().Filtered(nil)
	if deps.RunsDir != "" && runID != "" {
		if tm, err := task.NewManager(filepath.Join(deps.RunsDir, runID, ".tasks")); err == nil {
			reg.Register(builtin.TaskCreateTool{Manager: tm})
			reg.Register(builtin.TaskUpdateTool{Manager: tm})
			reg.Register(builtin.TaskListTool{Manager: tm})
			reg.Register(builtin.TaskGetTool{Manager: tm})
		}
	}
	// 子 Agent 工具按当前 runID 重注册（覆盖基础注册表里的占位实例），
	// 使 subagent.started/finished 携带正确的 parent_run_id。
	if deps.SubagentTasks != nil {
		reg.Register(subagent.NewSpawnAgentTool(deps.Provider, deps.Bus, deps.Perm, runID, deps.MaxSteps, deps.ToolTimeout, deps.SubagentTasks, 0,
			subagent.WithProfileLoader(deps.SubagentLoader), subagent.WithRunsDir(deps.RunsDir), subagent.WithSessionID(sessionID)))
		reg.Register(subagent.NewAgentResultTool(deps.SubagentTasks))
	}
	if noteSaver != nil {
		reg.Register(noteSaver)
	}
	if len(allowedTools) > 0 {
		reg = reg.Filtered(allowedTools)
	}
	return tools.NewInvoker(reg, deps.Perm, deps.Bus, deps.ToolTimeout, tools.WithSessionID(sessionID))
}

// newAgentLoop 构造一次 run 的 AgentLoop。
// sessionDir 用于压缩时落盘摘要；sessionID 为空表示无会话的 run（如 agent.run）。
func newAgentLoop(deps HandlerDeps, invoker *tools.Invoker, sessionDir, sessionID string) *loop.AgentLoop {
	compactor := compact.New(deps.Bus, sessionDir, sessionID)
	return loop.New(deps.Provider, invoker, deps.Bus,
		loop.WithCompaction(compactor, deps.CompactThreshold))
}

// runWithCancellation 在可取消的 ctx 中执行 run，并发布 run.finished。
// 两个 run 入口（agent.run / session.send_message）共用同一套生命周期，
// 保证 run_id 注册、注销与收尾事件的顺序一致。
func runWithCancellation(ctx context.Context, deps HandlerDeps, execCtx *execctx.ExecutionContext, agentLoop *loop.AgentLoop, system string) error {
	runCtx, cancel := context.WithCancel(ctx)
	if deps.RunCanceler != nil {
		deps.RunCanceler.Register(execCtx.RunID, cancel)
	}
	err := agentLoop.Run(runCtx, execCtx, system)
	if deps.RunCanceler != nil {
		deps.RunCanceler.Done(execCtx.RunID)
	}
	cancel()

	_ = deps.Bus.Publish(ctx, events.RunFinished{
		RunID:  execCtx.RunID,
		Status: execCtx.Status,
		Result: execCtx.Result,
		Reason: execCtx.Reason,
		Steps:  execCtx.Step,
	})
	return err
}

// splitSkillInvocation 把 "/name args" 拆成 skill 名与参数。
// 按任意空白分隔，最多切一刀，
// 参数侧保留内部空白（例如 "/skill   a  b" → skill="skill", args="a  b"）。
func splitSkillInvocation(content string) (name, args string) {
	rest := strings.TrimLeft(content[1:], " \t\r\n")
	idx := strings.IndexFunc(rest, unicode.IsSpace)
	if idx < 0 {
		return rest, ""
	}
	return rest[:idx], strings.TrimLeft(rest[idx:], " \t\r\n")
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}
