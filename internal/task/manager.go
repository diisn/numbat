package task

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Task 表示一个可追踪的工作单元。
type Task struct {
	ID          int    `json:"id"`
	Subject     string `json:"subject"`
	Description string `json:"description"`
	Status      string `json:"status"` // pending / in_progress / completed
	BlockedBy   []int  `json:"blocked_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Manager 管理任务的文件持久化（task_<id>.json）。
type Manager struct {
	dir    string
	mu     sync.Mutex
	nextID int
}

// NewManager 创建任务管理器，dir 为任务文件目录。
func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir}
	m.nextID = m.maxID() + 1
	return m, nil
}

// maxID 扫描目录中 task_*.json 文件，返回最大 ID。
func (m *Manager) maxID() int {
	maxID := 0
	entries, _ := os.ReadDir(m.dir)
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		parts := strings.SplitN(name, "_", 2)
		if len(parts) != 2 || parts[0] != "task" {
			continue
		}
		if id, err := strconv.Atoi(parts[1]); err == nil && id > maxID {
			maxID = id
		}
	}
	return maxID
}

// load 读取指定 ID 的任务文件。
func (m *Manager) load(taskID int) (*Task, error) {
	path := filepath.Join(m.dir, fmt.Sprintf("task_%d.json", taskID))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("task %d not found", taskID)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// save 将任务写入对应 JSON 文件。
func (m *Manager) save(t *Task) error {
	path := filepath.Join(m.dir, fmt.Sprintf("task_%d.json", t.ID))
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// Create 创建新任务，校验依赖存在性。
func (m *Manager) Create(subject, description string, blockedBy []int) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, dep := range blockedBy {
		if _, err := os.Stat(filepath.Join(m.dir, fmt.Sprintf("task_%d.json", dep))); err != nil {
			return nil, fmt.Errorf("blocked_by task %d not found", dep)
		}
	}
	now := nowUTC()
	t := &Task{
		ID:          m.nextID,
		Subject:     subject,
		Description: description,
		Status:      "pending",
		BlockedBy:   blockedBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := m.save(t); err != nil {
		return nil, err
	}
	m.nextID++
	return t, nil
}

// Get 读取指定 ID 的任务。
func (m *Manager) Get(taskID int) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load(taskID)
}

// Update 更新任务状态或依赖；completed 时自动清理其他任务的 blocked_by。
func (m *Manager) Update(taskID int, status string, addBlockedBy, removeBlockedBy []int) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.load(taskID)
	if err != nil {
		return nil, err
	}
	if status != "" {
		if status != "pending" && status != "in_progress" && status != "completed" {
			return nil, fmt.Errorf("invalid status: %q", status)
		}
		t.Status = status
		if status == "completed" {
			m.clearDependency(taskID)
		}
	}
	if len(addBlockedBy) > 0 {
		seen := make(map[int]bool)
		var merged []int
		for _, id := range t.BlockedBy {
			if !seen[id] {
				seen[id] = true
				merged = append(merged, id)
			}
		}
		for _, id := range addBlockedBy {
			if !seen[id] {
				seen[id] = true
				merged = append(merged, id)
			}
		}
		t.BlockedBy = merged
	}
	if len(removeBlockedBy) > 0 {
		remove := make(map[int]bool)
		for _, id := range removeBlockedBy {
			remove[id] = true
		}
		var kept []int
		for _, id := range t.BlockedBy {
			if !remove[id] {
				kept = append(kept, id)
			}
		}
		t.BlockedBy = kept
	}
	t.UpdatedAt = nowUTC()
	if err := m.save(t); err != nil {
		return nil, err
	}
	return t, nil
}

// ListAll 返回所有任务，按 ID 升序。
func (m *Manager) ListAll() []*Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, _ := os.ReadDir(m.dir)
	var ids []int
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		parts := strings.SplitN(name, "_", 2)
		if len(parts) != 2 || parts[0] != "task" {
			continue
		}
		if id, err := strconv.Atoi(parts[1]); err == nil {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)

	var tasks []*Task
	for _, id := range ids {
		if t, err := m.load(id); err == nil {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

// clearDependency 将 completedID 从所有其他任务的 blocked_by 中移除。
func (m *Manager) clearDependency(completedID int) {
	entries, _ := os.ReadDir(m.dir)
	for _, e := range entries {
		path := filepath.Join(m.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			continue
		}
		blocked := false
		for _, id := range t.BlockedBy {
			if id == completedID {
				blocked = true
				break
			}
		}
		if !blocked {
			continue
		}
		var kept []int
		for _, id := range t.BlockedBy {
			if id != completedID {
				kept = append(kept, id)
			}
		}
		t.BlockedBy = kept
		t.UpdatedAt = nowUTC()
		out, _ := json.MarshalIndent(&t, "", "  ")
		if err := os.WriteFile(path, out, 0o644); err != nil {
			slog.Warn("task: failed to clear dependency", "task_id", t.ID, "path", path, "error", err)
		}
	}
}

// FormatList 格式化任务列表摘要，供 task_list 工具返回。
func (m *Manager) FormatList() string {
	tasks := m.ListAll()
	if len(tasks) == 0 {
		return "No tasks."
	}
	markers := map[string]string{
		"pending":     "[ ]",
		"in_progress": "[>]",
		"completed":   "[x]",
	}
	var lines []string
	for _, t := range tasks {
		blocked := ""
		if len(t.BlockedBy) > 0 {
			blocked = fmt.Sprintf(" (blocked by: %v)", t.BlockedBy)
		}
		marker, ok := markers[t.Status]
		if !ok {
			marker = "[?]"
		}
		lines = append(lines, fmt.Sprintf("%s #%d: %s%s", marker, t.ID, t.Subject, blocked))
	}
	return strings.Join(lines, "\n")
}
