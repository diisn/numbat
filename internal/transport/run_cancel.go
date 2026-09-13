package transport

import (
	"context"
	"sync"
)

// runCanceler 管理每个 runID 对应的 context.CancelFunc，
// 用于 agent.abort 中断正在执行的 Agent 运行。
type runCanceler struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}

// NewRunCanceler 创建 run 级取消注册表。
func NewRunCanceler() *runCanceler {
	return &runCanceler{m: make(map[string]context.CancelFunc)}
}

// Register 注册一个 run 的取消函数。
func (rc *runCanceler) Register(runID string, cancel context.CancelFunc) {
	rc.mu.Lock()
	rc.m[runID] = cancel
	rc.mu.Unlock()
}

// Done 标记 run 已结束，从注册表中移除。
func (rc *runCanceler) Done(runID string) {
	rc.mu.Lock()
	delete(rc.m, runID)
	rc.mu.Unlock()
}

// Abort 按 runID 取消运行；返回是否成功找到并触发取消。
func (rc *runCanceler) Abort(runID string) bool {
	rc.mu.Lock()
	cancel, ok := rc.m[runID]
	delete(rc.m, runID)
	rc.mu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
	return ok
}
