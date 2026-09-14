package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/youngyangyang04/numbat/internal/llm"
)

// Store 是会话文件存储。
type Store struct {
	root  string
	locks sync.Map // session ID -> *sync.Mutex，保护同一会话的并发文件 I/O
}

// NewStore 创建会话存储。
func NewStore(root string) *Store {
	return &Store{root: root}
}

// sessionLock 返回指定会话的互斥锁（不存在则创建）。
func (s *Store) sessionLock(sid string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(sid, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// SessionDir 返回会话目录。
func (s *Store) SessionDir(sid string) string {
	return filepath.Join(s.root, sid)
}

// List 扫描存储根目录，返回所有磁盘会话元数据。
func (s *Store) List() ([]*Session, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := s.ReadMeta(e.Name()); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// ClearMessages 清空会话消息历史（thread.jsonl），保留元数据与笔记。
func (s *Store) ClearMessages(sid string) error {
	mu := s.sessionLock(sid)
	mu.Lock()
	defer mu.Unlock()
	path := filepath.Join(s.SessionDir(sid), "thread.jsonl")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Remove(path)
}

// WriteMeta 写入会话元数据。
func (s *Store) WriteMeta(session *Session) error {
	mu := s.sessionLock(session.ID)
	mu.Lock()
	defer mu.Unlock()
	dir := s.SessionDir(session.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := session.ToJSON()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644)
}

// ReadMeta 读取会话元数据。
func (s *Store) ReadMeta(sid string) (*Session, error) {
	mu := s.sessionLock(sid)
	mu.Lock()
	defer mu.Unlock()
	path := filepath.Join(s.SessionDir(sid), "meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var session Session
	if err := session.FromJSON(data); err != nil {
		return nil, err
	}
	return &session, nil
}

// AppendMessage 追加一条消息。
func (s *Store) AppendMessage(sid string, role string, content []llm.ContentBlock) error {
	return s.AppendMessages(sid, []llm.Message{{Role: role, Content: content}})
}

// AppendMessages 按顺序追加多条消息到 thread.jsonl（单次打开、逐行写入）。
func (s *Store) AppendMessages(sid string, messages []llm.Message) error {
	if len(messages) == 0 {
		return nil
	}
	mu := s.sessionLock(sid)
	mu.Lock()
	defer mu.Unlock()
	dir := s.SessionDir(sid)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "thread.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	ts := time.Now().UTC().Format(time.RFC3339)
	for _, msg := range messages {
		data, err := json.Marshal(map[string]any{
			"ts":      ts,
			"role":    msg.Role,
			"content": msg.Content,
		})
		if err != nil {
			return err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// ReadMessages 读取会话消息历史。
func (s *Store) ReadMessages(sid string) ([]llm.Message, error) {
	mu := s.sessionLock(sid)
	mu.Lock()
	defer mu.Unlock()
	path := filepath.Join(s.SessionDir(sid), "thread.jsonl")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var messages []llm.Message
	scanner := bufio.NewScanner(file)
	// 默认 64KB 缓冲会在大模型回复（单条 jsonl 行超长）时直接报错，
	// 导致整段历史读取失败，这里放宽到 4MB（与传输层回放一致）。
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var row struct {
			Role    string             `json:"role"`
			Content []llm.ContentBlock `json:"content"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		if row.Role != "user" && row.Role != "assistant" {
			continue
		}
		messages = append(messages, llm.Message{Role: row.Role, Content: row.Content})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return trimOrphanToolUse(messages), nil
}

// trimOrphanToolUse 裁掉尾部未配对的 tool_use 及其后的消息，避免下一次请求被上游以
// messages.invalid 拒绝。
// 只有被中止的 run 才会留下孤立 tool_use —— 正常完成的 run 会补齐 tool_result。
func trimOrphanToolUse(messages []llm.Message) []llm.Message {
	pending := make(map[string]bool)
	lastBalanced := 0
	for i, msg := range messages {
		for _, block := range msg.Content {
			switch {
			case msg.Role == "assistant" && block.Type == "tool_use":
				pending[block.ID] = true
			case msg.Role == "user" && block.Type == "tool_result":
				delete(pending, block.ToolUseID)
			}
		}
		if len(pending) == 0 {
			lastBalanced = i + 1
		}
	}
	if len(pending) == 0 {
		return messages
	}
	slog.Warn("session: trimming orphan tool_use blocks from thread", "dropped", len(messages)-lastBalanced)
	return messages[:lastBalanced]
}

// WriteCompacted 覆盖写入压缩后的消息。
func (s *Store) WriteCompacted(sid string, messages []llm.Message) error {
	mu := s.sessionLock(sid)
	mu.Lock()
	defer mu.Unlock()
	dir := s.SessionDir(sid)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "thread.jsonl")
	// 备份名带纳秒：秒级精度下同一秒内二次压缩会 rename 到已存在文件（Windows 上直接失败）。
	bak := filepath.Join(dir, fmt.Sprintf("thread_%s.jsonl.bak", time.Now().UTC().Format("20060102_150405.000000000")))
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, bak); err != nil {
			slog.Warn("session: failed to backup thread.jsonl", "session_id", sid, "error", err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, msg := range messages {
		row := map[string]any{
			"ts":      time.Now().UTC().Format(time.RFC3339),
			"role":    msg.Role,
			"content": msg.Content,
		}
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// AppendNote 追加笔记。
func (s *Store) AppendNote(sid, content, runID string) error {
	mu := s.sessionLock(sid)
	mu.Lock()
	defer mu.Unlock()
	dir := s.SessionDir(sid)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	note := fmt.Sprintf("## Note (%s, %s)\n%s\n\n", time.Now().UTC().Format(time.RFC3339), runID, content)
	f, err := os.OpenFile(filepath.Join(dir, "notes.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(note)
	return err
}

// ReadNotes 读取笔记。
func (s *Store) ReadNotes(sid string) (string, error) {
	mu := s.sessionLock(sid)
	mu.Lock()
	defer mu.Unlock()
	path := filepath.Join(s.SessionDir(sid), "notes.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
