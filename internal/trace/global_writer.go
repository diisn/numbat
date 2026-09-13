package trace

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/youngyangyang04/numbat/internal/events"
)

// TraceRecord 是全局 trace 的一条记录。
type TraceRecord struct {
	Ts        string `json:"ts"`
	Direction string `json:"direction"`
	Layer     string `json:"layer"` // "ipc" | "event" | "llm"
	Kind      string `json:"kind"`  // "request" | "response" | "event"
	RunID     string `json:"run_id,omitempty"`
	Step      int    `json:"step,omitempty"`
	Data      any    `json:"data"`
}

// GlobalWriter 将全局 trace 记录写入单个 JSONL 文件。
type GlobalWriter struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	writeCount int
}

// NewGlobalWriter 创建全局 trace writer。path 为输出 JSONL 文件路径。
func NewGlobalWriter(path string) (*GlobalWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &GlobalWriter{file: f, path: path}, nil
}

// Write 写入一条 trace 记录。
func (w *GlobalWriter) Write(rec TraceRecord) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return
	}
	if rec.Ts == "" {
		rec.Ts = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = w.file.Write(append(line, '\n'))

	w.writeCount++
	if w.writeCount%512 == 0 {
		if info, err := w.file.Stat(); err == nil && info.Size() > 10*1024*1024 {
			if err := w.file.Close(); err != nil {
				slog.Error("trace rotate: close failed", "error", err)
				// close 失败时不继续轮转，保留当前 file
			} else {
				// Windows 上 rename 到已存在的目标会失败，先删掉旧备份再轮转。
				_ = os.Remove(w.path + ".1")
				if err := os.Rename(w.path, w.path+".1"); err != nil {
					slog.Error("trace rotate: rename failed", "error", err)
				}
				f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
				if err != nil {
					slog.Error("trace rotate: reopen failed", "error", err)
					w.file = nil
					return
				}
				w.file = f
			}
		}
	}
}

// Close 关闭文件。
func (w *GlobalWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}

// Subscribe 订阅事件总线上的所有需要记录的事件。
func (w *GlobalWriter) Subscribe(bus *events.Bus) {
	for _, ev := range eventTypes {
		bus.Subscribe(reflect.TypeOf(ev), w.onEvent)
	}
}

// onEvent 将单条事件写入全局 trace 文件。
func (w *GlobalWriter) onEvent(ctx context.Context, ev events.Event) error {
	w.Write(TraceRecord{
		Ts:        time.Now().UTC().Format(time.RFC3339),
		Direction: "CORE",
		Layer:     "event",
		Kind:      "event",
		RunID:     extractRunID(ev),
		Data:      ev,
	})
	return nil
}
