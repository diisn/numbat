package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/youngyangyang04/numbat/internal/events"
)

// Manager 管理会话生命周期。
type Manager struct {
	mu       sync.RWMutex
	store    *Store
	sessions map[string]*Session
	bus      *events.Bus
}

// NewManager 创建会话管理器。
func NewManager(store *Store, bus *events.Bus) *Manager {
	return &Manager{
		store:    store,
		sessions: make(map[string]*Session),
		bus:      bus,
	}
}

// Create 创建新会话。
func (m *Manager) Create(mode Mode, title string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sid := "sess-" + generateID()
	if title == "" {
		title = "untitled"
	}
	session := NewSession(sid, mode, title)
	m.sessions[sid] = session
	if err := m.store.WriteMeta(session); err != nil {
		return nil, err
	}
	_ = m.bus.Publish(context.Background(), events.SessionCreated{SessionID: sid, Mode: string(mode)})
	return session.Clone(), nil
}

// Get 获取会话。返回副本：调用方自由改写后再交给 Update，不会影响并发读取。
func (m *Manager) Get(sid string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if session, ok := m.sessions[sid]; ok {
		return session.Clone(), nil
	}
	return m.store.ReadMeta(sid)
}

// List 列出所有会话。返回内存会话与磁盘会话的并集，按更新时间倒序。
// 返回的都是副本，排序在锁外进行不会与写入方竞争。
func (m *Manager) List() []*Session {
	m.mu.RLock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Clone())
	}
	m.mu.RUnlock()

	onDisk, err := m.store.List()
	if err != nil {
		slog.Warn("session: list from store failed", "error", err)
	}
	seen := make(map[string]bool, len(out))
	for _, s := range out {
		seen[s.ID] = true
	}
	for _, s := range onDisk {
		if !seen[s.ID] {
			out = append(out, s)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// Clear 清空指定会话的消息历史。
func (m *Manager) Clear(sid string) error {
	if err := m.store.ClearMessages(sid); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, ok := m.sessions[sid]; ok {
		session.RunIDs = nil
		session.UpdatedAt = nowISO()
	}
	return nil
}

// Close 关闭会话。
func (m *Manager) Close(sid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sid]
	if !ok {
		var err error
		session, err = m.store.ReadMeta(sid)
		if err != nil {
			return err
		}
	}
	session.Status = StatusClosed
	session.UpdatedAt = nowISO()
	if err := m.store.WriteMeta(session); err != nil {
		return err
	}
	m.sessions[sid] = session
	_ = m.bus.Publish(context.Background(), events.SessionClosed{SessionID: sid})
	return nil
}

// Update 更新会话。
func (m *Manager) Update(session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session.UpdatedAt = nowISO()
	if err := m.store.WriteMeta(session); err != nil {
		return err
	}
	m.sessions[session.ID] = session
	return nil
}

// Store 返回底层存储。
func (m *Manager) Store() *Store {
	return m.store
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func generateID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}
