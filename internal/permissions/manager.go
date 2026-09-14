package permissions

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// Manager 管理工具调用权限。
type Manager struct {
	policies         map[string]ToolPolicy
	pending          map[string]chan string
	sessionAlways    map[string]string
	persistentAlways map[string]string
	policyFile       string
	timeout          time.Duration
	patternCache     map[string]*regexp.Regexp // 懒加载正则缓存
	mu               sync.RWMutex
}

// NewManager 创建权限管理器。
func NewManager(policyFile string, timeout time.Duration) *Manager {
	m := &Manager{
		policies:         DefaultPolicies(),
		pending:          make(map[string]chan string),
		sessionAlways:    make(map[string]string),
		persistentAlways: make(map[string]string),
		policyFile:       policyFile,
		timeout:          timeout,
		patternCache:     make(map[string]*regexp.Regexp),
	}
	if policyFile != "" {
		if err := m.loadPolicyFile(policyFile); err != nil {
			slog.Warn("permissions: failed to load policy file", "path", policyFile, "error", err)
		}
	}
	return m
}

// neverMatch 是非法正则的缓存哨兵：[\s\S] 覆盖全部字符，取反后不匹配任何输入。
var neverMatch = regexp.MustCompile(`[^\s\S]`)

// matchPattern 匹配正则模式，pattern 编译结果缓存避免重复编译。
func (m *Manager) matchPattern(pattern, command string) bool {
	m.mu.RLock()
	re, ok := m.patternCache[pattern]
	m.mu.RUnlock()
	if !ok {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			// 非法 pattern 会被当作「不匹配」而静默失效，必须留痕。
			slog.Warn("permissions: invalid pattern", "pattern", pattern, "error", err)
			compiled = neverMatch
		}
		m.mu.Lock()
		m.patternCache[pattern] = compiled
		m.mu.Unlock()
		re = compiled
	}
	return re.MatchString(command)
}

// CheckAndWait 检查权限并在需要时等待用户审批。onAsk 在需要询问时被调用。
// ctx 取消（例如 agent.abort）会立即结束等待，返回 "cancelled"，避免 run 被审批阻塞到超时。
//
// 检查顺序（deny_patterns 和 outside-cwd 不可被缓存绕过）：
//  1. deny_patterns 命中 → auto_deny
//  2. outside-cwd 命中 → forceAsk
//  3. 缓存查找（sessionAlways → persistentAlways），仅当非 forceAsk
//  4. allow_patterns 命中 → auto_allow，仅当非 forceAsk
//  5. tool default（Allow → auto_allow; Deny → auto_deny），仅当非 forceAsk
//  6. ASK 路径（forceAsk 或 default=Ask）
func (m *Manager) CheckAndWait(ctx context.Context, toolUseID, toolName string, params map[string]any, sessionID string, onAsk func()) (bool, string, error) {
	policy, ok := m.policies[toolName]
	if !ok {
		policy = ToolPolicy{Default: Ask}
	}

	sessionKey := sessionID + "::" + toolName

	// Phase 0: 提取 bash command
	command := ""
	if toolName == "bash" {
		command, _ = params["command"].(string)
	}

	// Phase 1: deny_patterns 检查 — 不可被缓存绕过
	for _, pat := range policy.DenyPatterns {
		if m.matchPattern(pat, command) {
			return false, "auto_deny", nil
		}
	}

	// Phase 2: outside-cwd 检查 — 不可被缓存绕过
	forceAsk := false
	if command != "" && MatchesOutsideCWD(command) {
		forceAsk = true
	}

	// Phase 3: 缓存查找（仅当非 forceAsk）
	if !forceAsk {
		m.mu.RLock()
		if val, ok := m.sessionAlways[sessionKey]; ok {
			allowed := val == "allow"
			reason := "auto_" + val
			m.mu.RUnlock()
			return allowed, reason, nil
		}
		if val, ok := m.persistentAlways[toolName]; ok {
			allowed := val == "allow"
			reason := "auto_" + val
			m.mu.RUnlock()
			return allowed, reason, nil
		}
		m.mu.RUnlock()
	}

	// Phase 4: allow_patterns 检查（仅当非 forceAsk）
	if !forceAsk {
		for _, pat := range policy.AllowPatterns {
			if m.matchPattern(pat, command) {
				return true, "auto_allow", nil
			}
		}
	}

	// Phase 5: tool default（仅当非 forceAsk）
	if !forceAsk {
		switch policy.Default {
		case Allow:
			return true, "auto_allow", nil
		case Deny:
			return false, "auto_deny", nil
		}
	}

	// Phase 6: ASK 路径（forceAsk 或 default=Ask）
	ch := make(chan string, 1)
	m.mu.Lock()
	m.pending[toolUseID] = ch
	m.mu.Unlock()

	// 先注册 pending 再发布事件：同步订阅者可能在事件回调中立即 Respond
	if onAsk != nil {
		onAsk()
	}

	// cancelWait 清理 pending 并返回取消结果。
	cancelWait := func() (bool, string, error) {
		m.mu.Lock()
		delete(m.pending, toolUseID)
		m.mu.Unlock()
		return false, "cancelled", ctx.Err()
	}

	if m.timeout == 0 {
		select {
		case decision := <-ch:
			return m.applyResponse(decision, sessionID, toolName, sessionKey)
		case <-ctx.Done():
			return cancelWait()
		}
	}

	timer := time.NewTimer(m.timeout)
	defer timer.Stop()

	select {
	case decision := <-ch:
		return m.applyResponse(decision, sessionID, toolName, sessionKey)
	case <-ctx.Done():
		return cancelWait()
	case <-timer.C:
		m.mu.Lock()
		delete(m.pending, toolUseID)
		m.mu.Unlock()
		return false, "timeout", fmt.Errorf("permission request timed out")
	}
}

// Respond 响应权限审批。
func (m *Manager) Respond(toolUseID, decision string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.pending[toolUseID]
	if !ok {
		return false
	}
	delete(m.pending, toolUseID)
	ch <- decision
	return true
}

func (m *Manager) applyResponse(decision, sessionID, toolName, sessionKey string) (bool, string, error) {
	allow := decision == "allow_once" || decision == "always_allow"

	m.mu.Lock()
	defer m.mu.Unlock()

	if decision == "always_allow" {
		m.sessionAlways[sessionKey] = "allow"
		m.persistentAlways[toolName] = "allow"
		if err := m.savePolicyFile(); err != nil {
			slog.Warn("permissions: failed to save policy file", "error", err)
		}
	} else if decision == "always_deny" {
		m.sessionAlways[sessionKey] = "deny"
		m.persistentAlways[toolName] = "deny"
		if err := m.savePolicyFile(); err != nil {
			slog.Warn("permissions: failed to save policy file", "error", err)
		}
	}

	return allow, decision, nil
}

// policyFile 是 policy.toml 的结构：持久化的「总是允许/拒绝」决策放在 [always] 节下，
// 保存路径为 ~/.numbat/policy.toml。
type policyFile struct {
	Always map[string]string `toml:"always"`
}

func (m *Manager) loadPolicyFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var parsed policyFile
	if err := toml.Unmarshal(data, &parsed); err != nil {
		return err
	}
	for tool, decision := range parsed.Always {
		if validDecision(decision) {
			m.persistentAlways[tool] = decision
		}
	}

	// 兼容早期 Go 版写出的扁平顶层键（bash = "allow"）。
	// 含 [always] 节时这里会因类型不匹配而失败，属预期。
	var legacy map[string]string
	if err := toml.Unmarshal(data, &legacy); err == nil {
		for tool, decision := range legacy {
			if !validDecision(decision) {
				continue
			}
			if _, exists := m.persistentAlways[tool]; !exists {
				m.persistentAlways[tool] = decision
			}
		}
	}
	return nil
}

// validDecision 判断持久化决策是否合法。手改配置写出的其他值一律忽略，
// 回退到默认策略（仅接受 "allow" / "deny"）。
func validDecision(decision string) bool {
	return decision == "allow" || decision == "deny"
}

func (m *Manager) savePolicyFile() error {
	if m.policyFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.policyFile), 0755); err != nil {
		return err
	}
	f, err := os.Create(m.policyFile)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(policyFile{Always: m.persistentAlways})
}

// SetDefaultForTest 设置工具默认决策（仅供测试使用）。
func (m *Manager) SetDefaultForTest(toolName string, decision Decision) {
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, ok := m.policies[toolName]
	if !ok {
		policy = ToolPolicy{}
	}
	policy.Default = decision
	m.policies[toolName] = policy
}
