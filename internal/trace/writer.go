package trace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/youngyangyang04/numbat/internal/events"
)

// eventTypes 列出需要记录的事件类型（事件流即一次 run 的完整轨迹）。
// 不含 llm.token：单条增量过密，且可由 llm.response 还原。
var eventTypes = []any{
	events.RunStarted{},
	events.RunFinished{},
	events.LLMRequest{},
	events.LLMResponse{},
	// llm.usage 用于 run.list 汇总 token 用量（每次 LLM 调用一条）
	events.LLMUsage{},
	events.ToolCallStarted{},
	events.ToolCallFinished{},
	events.ToolCallFailed{},
	events.PermissionRequested{},
	events.PermissionGranted{},
	events.PermissionDenied{},
	events.ContextCompacted{},
	events.SubagentStarted{},
	events.SubagentFinished{},
	events.SkillInvoked{},
}

// Writer 将事件流按 run 写入 <dir>/<runID>/events.jsonl。
type Writer struct {
	dir   string
	mu    sync.Mutex
	files map[string]*os.File
}

// NewWriter 创建 trace writer，dir 为 traces 根目录。
func NewWriter(dir string) *Writer {
	return &Writer{
		dir:   dir,
		files: make(map[string]*os.File),
	}
}

// Subscribe 订阅所有需要记录的事件。
func (w *Writer) Subscribe(bus *events.Bus) {
	for _, ev := range eventTypes {
		bus.Subscribe(reflect.TypeOf(ev), w.onEvent)
	}
}

// onEvent 将单条事件写入对应 run 的 trace 文件。
func (w *Writer) onEvent(ctx context.Context, ev events.Event) error {
	runID := extractRunID(ev)
	if runID == "" {
		return nil
	}

	// 只序列化一次事件，然后手动在 JSON 开头插入 ts 和 type 字段，
	// 替代原来的 marshal→unmarshal→合并 map→marshal 三次序列化。
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	ts := strconv.Quote(time.Now().UTC().Format(time.RFC3339))
	topic := strconv.Quote(events.Topic(ev))
	// data 格式: {"field1":...} → {"ts":"...","type":"...","field1":...}
	prefix := `{"ts":` + ts + `,"type":` + topic + ","
	line := make([]byte, 0, len(prefix)+len(data)+1)
	line = append(line, prefix...)
	line = append(line, data[1:]...) // 跳过开头的 '{'
	line = append(line, '\n')
	_, err = w.fileFor(runID).Write(line)
	return err
}

// extractRunID 通过反射提取事件的 RunID 字段。
func extractRunID(ev events.Event) string {
	v := reflect.ValueOf(ev)
	f := v.FieldByName("RunID")
	if !f.IsValid() || f.Kind() != reflect.String {
		return ""
	}
	return f.String()
}

// fileFor 返回 runID 对应的 trace 文件，首次访问时创建。
func (w *Writer) fileFor(runID string) *os.File {
	w.mu.Lock()
	defer w.mu.Unlock()
	if f, ok := w.files[runID]; ok {
		return f
	}
	dir := filepath.Join(w.dir, runID)
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		// 打开失败时丢弃记录，避免反复报错
		f, _ = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}
	w.files[runID] = f
	return f
}

// Close 关闭所有已打开的 trace 文件。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, f := range w.files {
		_ = f.Close()
	}
	return nil
}
