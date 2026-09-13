package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/permissions"
)

// maxRetries 是最大重试次数（1 次初始调用 + 2 次重试）。
const maxRetries = 2

// retryBase 是指数退避基准时长：attempt=1 失败后等 2s，attempt=2 失败后等 4s。
var retryBase = 2 * time.Second

// retryableErrors 是可触发重试的错误类型集合。
var retryableErrors = map[string]bool{
	"runtime_error": true,
	"rate_limited":  true,
}

// permissionDeniedMessage 是权限被拒时回给模型的提示，引导它换一条路径而不是原地重试。
const permissionDeniedMessage = "Permission denied by user. You may not execute this command. " +
	"Try an alternative approach or ask the user what to do."

// Invoker 负责执行工具调用并处理权限和超时。
type Invoker struct {
	registry  *Registry
	perm      *permissions.Manager
	bus       *events.Bus
	timeout   time.Duration
	sessionID string
}

// InvokerOption 配置 Invoker 的可选行为。
type InvokerOption func(*Invoker)

// WithSessionID 设置会话 ID。权限事件会携带它，会话级「总是允许」缓存也按它归档，
// 因此必须传真实的 session_id（而不是 run_id）。
func WithSessionID(sessionID string) InvokerOption {
	return func(i *Invoker) { i.sessionID = sessionID }
}

// NewInvoker 创建工具调用器。
func NewInvoker(registry *Registry, perm *permissions.Manager, bus *events.Bus, timeout time.Duration, opts ...InvokerOption) *Invoker {
	i := &Invoker{registry: registry, perm: perm, bus: bus, timeout: timeout}
	for _, opt := range opts {
		opt(i)
	}
	return i
}

// Registry 返回工具注册表。
func (i *Invoker) Registry() *Registry {
	return i.registry
}

// Invoke 调用单个工具，返回结果而不抛错——失败一律转成 is_error 的 tool_result 回给模型。
func (i *Invoker) Invoke(ctx context.Context, runID, toolUseID, toolName string, params map[string]any) Result {
	_ = i.bus.Publish(ctx, events.ToolCallStarted{
		RunID:     runID,
		ToolUseID: toolUseID,
		ToolName:  toolName,
		Params:    params,
	})

	tool := i.registry.Get(toolName)
	if tool == nil {
		return i.fail(ctx, runID, toolUseID, toolName, "runtime_error",
			fmt.Sprintf("unknown tool: %s", toolName), 0, 1)
	}

	// 参数校验先于权限检查：参数非法时直接回 schema_error，不必打扰用户审批。
	if err := validateParams(tool, params); err != nil {
		return i.fail(ctx, runID, toolUseID, toolName, "schema_error", err.Error(), 0, 1)
	}

	if !i.checkPermission(ctx, runID, toolUseID, toolName, params) {
		return i.fail(ctx, runID, toolUseID, toolName, "permission_denied", permissionDeniedMessage, 0, 1)
	}

	return i.executeWithRetry(ctx, runID, toolUseID, toolName, tool, params)
}

// checkPermission 检查权限并发布审批事件；被拒时返回 false。
// 仅在非自动判定时发布 granted/denied——auto_allow（如 read_file）每次都发会刷屏。
func (i *Invoker) checkPermission(ctx context.Context, runID, toolUseID, toolName string, params map[string]any) bool {
	allowed, decision, err := i.perm.CheckAndWait(ctx, toolUseID, toolName, params, i.sessionID, func() {
		_ = i.bus.Publish(ctx, events.PermissionRequested{
			RunID:     runID,
			ToolUseID: toolUseID,
			ToolName:  toolName,
			Params:    params,
			Preview:   permissions.ParamPreview(toolName, params),
			SessionID: i.sessionID,
		})
	})
	if err != nil || !allowed {
		if decision != "auto_deny" {
			_ = i.bus.Publish(ctx, events.PermissionDenied{
				RunID:     runID,
				ToolUseID: toolUseID,
				ToolName:  toolName,
				Decision:  decision,
			})
		}
		return false
	}
	if decision != "auto_allow" {
		_ = i.bus.Publish(ctx, events.PermissionGranted{
			RunID:     runID,
			ToolUseID: toolUseID,
			ToolName:  toolName,
			Decision:  decision,
		})
	}
	return true
}

// executeWithRetry 执行工具并按错误类型做指数退避重试。
func (i *Invoker) executeWithRetry(ctx context.Context, runID, toolUseID, toolName string, tool Tool, params map[string]any) Result {
	start := time.Now()
	for attempt := 1; attempt <= maxRetries+1; attempt++ {
		execCtx, cancel := i.withTimeout(ctx)
		res, err := tool.Invoke(execCtx, params)
		timedOut := execCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
		cancel()
		elapsed := int(time.Since(start).Milliseconds())

		if timedOut {
			return i.fail(ctx, runID, toolUseID, toolName, "timeout",
				fmt.Sprintf("tool timed out after %gs", i.timeout.Seconds()), elapsed, attempt)
		}
		if err != nil {
			res = Result{Content: err.Error(), IsError: true, ErrorType: "runtime_error"}
		}
		if res.ErrorType == "" && res.IsError {
			res.ErrorType = "runtime_error"
		}

		if !res.IsError {
			_ = i.bus.Publish(ctx, events.ToolCallFinished{
				RunID:     runID,
				ToolUseID: toolUseID,
				ToolName:  toolName,
				Output:    res.Content,
				ElapsedMs: elapsed,
			})
			return res
		}

		// 运行被取消（用户中止）：不再重试
		if ctx.Err() != nil {
			return i.fail(ctx, runID, toolUseID, toolName, res.ErrorType, res.Content, elapsed, attempt)
		}

		// 可重试且未耗尽次数：发一次失败事件（带 attempt 便于观测重试），退避后重试
		if retryableErrors[res.ErrorType] && attempt <= maxRetries {
			_ = i.bus.Publish(ctx, events.ToolCallFailed{
				RunID:     runID,
				ToolUseID: toolUseID,
				ToolName:  toolName,
				Error:     res.Content,
				ErrorType: res.ErrorType,
				ElapsedMs: elapsed,
				Attempt:   attempt,
			})
			select {
			case <-time.After(retryBase * time.Duration(1<<(attempt-1))):
			case <-ctx.Done():
			}
			continue
		}

		return i.fail(ctx, runID, toolUseID, toolName, res.ErrorType, res.Content, elapsed, attempt)
	}
	return Result{Content: "internal error", IsError: true, ErrorType: "runtime_error"}
}

// withTimeout 为一次工具调用加上超时；timeout<=0 表示不设超时。
func (i *Invoker) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if i.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, i.timeout)
}

// fail 发布 tool.call_failed 事件并返回失败结果。
func (i *Invoker) fail(ctx context.Context, runID, toolUseID, toolName, errorType, message string, elapsedMs, attempt int) Result {
	_ = i.bus.Publish(ctx, events.ToolCallFailed{
		RunID:     runID,
		ToolUseID: toolUseID,
		ToolName:  toolName,
		Error:     message,
		ErrorType: errorType,
		ElapsedMs: elapsedMs,
		Attempt:   attempt,
	})
	return Result{Content: message, IsError: true, ErrorType: errorType}
}

// SetRetryBaseForTest 设置退避基准时长（仅供测试使用）。
func SetRetryBaseForTest(d time.Duration) func() {
	original := retryBase
	retryBase = d
	return func() { retryBase = original }
}
