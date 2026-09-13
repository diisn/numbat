package session

import (
	"encoding/json"
	"time"
)

// Status 表示会话状态。
type Status string

// 会话状态常量。
const (
	StatusActive          Status = "active"
	StatusWaitingForInput Status = "waiting_for_input"
	StatusClosed          Status = "closed"
)

// Mode 表示会话模式。
type Mode string

// 会话模式常量。
const (
	ModeOneShot Mode = "one_shot"
	ModeChat    Mode = "chat"
)

// Session 表示一个会话。
type Session struct {
	ID        string   `json:"id"`
	Mode      Mode     `json:"mode"`
	Status    Status   `json:"status"`
	Title     string   `json:"title"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	RunIDs    []string `json:"run_ids"`
}

// NewSession 创建新会话。
func NewSession(id string, mode Mode, title string) *Session {
	now := time.Now().UTC().Format(time.RFC3339)
	return &Session{
		ID:        id,
		Mode:      mode,
		Status:    StatusActive,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
		RunIDs:    []string{},
	}
}

// Clone 返回会话的深拷贝。Manager 用它在返回给调用方前隔离内部对象，
// 使调用方可以自由读写，而不与并发访问共享可变状态。
func (s *Session) Clone() *Session {
	cp := *s
	cp.RunIDs = append([]string{}, s.RunIDs...)
	return &cp
}

// ToJSON 序列化会话。
func (s *Session) ToJSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// FromJSON 反序列化会话。
func (s *Session) FromJSON(data []byte) error {
	return json.Unmarshal(data, s)
}
