package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeTraceLine 按 Writer 的真实格式写出一个 run 的轨迹文件。
func writeRunTrace(t *testing.T, runsDir, runID string, lines []map[string]any) {
	t.Helper()
	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	f, err := os.Create(filepath.Join(dir, traceFileName))
	if err != nil {
		t.Fatalf("create trace: %v", err)
	}
	defer func() { _ = f.Close() }()
	for _, ln := range lines {
		data, err := json.Marshal(ln)
		if err != nil {
			t.Fatalf("marshal line: %v", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatalf("write line: %v", err)
		}
	}
}

func TestListRunsAggregatesTrace(t *testing.T) {
	dir := t.TempDir()
	writeRunTrace(t, dir, "run-1", []map[string]any{
		{"ts": "2026-01-01T00:00:00Z", "type": "run.started", "run_id": "run-1", "goal": "读取文件"},
		{"ts": "2026-01-01T00:00:01Z", "type": "llm.request", "run_id": "run-1", "messages": []any{}},
		{"ts": "2026-01-01T00:00:02Z", "type": "llm.usage", "run_id": "run-1", "input_tokens": 1200, "output_tokens": 300, "context_pct": 0.006},
		{"ts": "2026-01-01T00:00:03Z", "type": "tool.call_started", "run_id": "run-1", "tool_use_id": "tu-1", "tool_name": "read_file"},
		{"ts": "2026-01-01T00:00:04Z", "type": "tool.call_finished", "run_id": "run-1", "tool_use_id": "tu-1", "tool_name": "read_file", "elapsed_ms": 42},
		{"ts": "2026-01-01T00:00:05Z", "type": "tool.call_started", "run_id": "run-1", "tool_use_id": "tu-2", "tool_name": "bash"},
		{"ts": "2026-01-01T00:00:06Z", "type": "tool.call_failed", "run_id": "run-1", "tool_use_id": "tu-2", "tool_name": "bash", "error": "command not found", "elapsed_ms": 7},
		{"ts": "2026-01-01T00:00:07Z", "type": "skill.invoked", "run_id": "run-1", "skill_name": "spec"},
		{"ts": "2026-01-01T00:00:10Z", "type": "run.finished", "run_id": "run-1", "status": "success", "result": "完成"},
	})

	runs, total, err := ListRuns(dir, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("want 1 run, got total=%d len=%d", total, len(runs))
	}

	r := runs[0]
	if r.RunID != "run-1" || r.Goal != "读取文件" {
		t.Errorf("run 基础字段有误: %+v", r)
	}
	if r.Status != "success" {
		t.Errorf("status = %q, want success", r.Status)
	}
	if r.StartedAt != "2026-01-01T00:00:00Z" || r.FinishedAt != "2026-01-01T00:00:10Z" {
		t.Errorf("时间字段有误: %q → %q", r.StartedAt, r.FinishedAt)
	}
	if r.DurationMs != 10000 {
		t.Errorf("DurationMs = %d, want 10000", r.DurationMs)
	}
	if r.Skill != "spec" {
		t.Errorf("Skill = %q, want spec", r.Skill)
	}
	if r.Tokens.InputTokens != 1200 || r.Tokens.OutputTokens != 300 {
		t.Errorf("Tokens = %+v", r.Tokens)
	}
	if len(r.Tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(r.Tools))
	}
	if r.Tools[0].ToolName != "read_file" || r.Tools[0].Status != "success" || r.Tools[0].ElapsedMs != 42 {
		t.Errorf("tool[0] = %+v", r.Tools[0])
	}
	if r.Tools[1].Status != "failed" || r.Tools[1].ElapsedMs != 7 {
		t.Errorf("tool[1] = %+v", r.Tools[1])
	}
	if len(r.Events) != 9 {
		t.Errorf("want 9 events, got %d", len(r.Events))
	}
}

// 未结束的 run 状态为 running，子 run 归并到父 run。
func TestListRunsMergesSubagentsAndKeepsRunningStatus(t *testing.T) {
	dir := t.TempDir()
	writeRunTrace(t, dir, "parent-1", []map[string]any{
		{"ts": "2026-01-02T00:00:00Z", "type": "run.started", "run_id": "parent-1", "goal": "分析架构"},
		{"ts": "2026-01-02T00:00:05Z", "type": "tool.call_started", "run_id": "parent-1", "tool_use_id": "tu-9", "tool_name": "spawn_agent"},
	})
	writeRunTrace(t, dir, "child-1", []map[string]any{
		{"ts": "2026-01-02T00:00:01Z", "type": "subagent.started", "run_id": "child-1", "parent_run_id": "parent-1", "description": "架构分析子 Agent"},
		{"ts": "2026-01-02T00:00:02Z", "type": "run.started", "run_id": "child-1", "goal": "分析架构"},
		{"ts": "2026-01-02T00:00:03Z", "type": "run.finished", "run_id": "child-1", "status": "success"},
		{"ts": "2026-01-02T00:00:04Z", "type": "subagent.finished", "run_id": "child-1", "parent_run_id": "parent-1", "status": "success"},
	})

	runs, total, err := ListRuns(dir, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	// 子 run 不单独列出
	if total != 1 || len(runs) != 1 {
		t.Fatalf("子 run 不应单独列出: total=%d len=%d", total, len(runs))
	}

	r := runs[0]
	if r.RunID != "parent-1" {
		t.Fatalf("顶层 run = %q, want parent-1", r.RunID)
	}
	if r.Status != "running" {
		t.Errorf("无 run.finished 时 status = %q, want running", r.Status)
	}
	if len(r.Subagents) != 1 {
		t.Fatalf("want 1 subagent, got %d", len(r.Subagents))
	}
	sub := r.Subagents[0]
	if sub.RunID != "child-1" || sub.Description != "架构分析子 Agent" || sub.Status != "success" {
		t.Errorf("subagent = %+v", sub)
	}
}

func TestListRunsSortsByStartDescAndAppliesLimit(t *testing.T) {
	dir := t.TempDir()
	for i, ts := range []string{"2026-01-01T00:00:00Z", "2026-01-03T00:00:00Z", "2026-01-02T00:00:00Z"} {
		id := string(rune('a' + i))
		writeRunTrace(t, dir, "run-"+id, []map[string]any{
			{"ts": ts, "type": "run.started", "run_id": "run-" + id, "goal": id},
			{"ts": ts, "type": "run.finished", "run_id": "run-" + id, "status": "success"},
		})
	}

	runs, total, err := ListRuns(dir, 2)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(runs) != 2 {
		t.Fatalf("limit 未生效: len=%d", len(runs))
	}
	if runs[0].RunID != "run-b" || runs[1].RunID != "run-c" {
		t.Errorf("排序有误: %s, %s", runs[0].RunID, runs[1].RunID)
	}
}

func TestListRunsMissingDirAndCorruptLines(t *testing.T) {
	runs, total, err := ListRuns(filepath.Join(t.TempDir(), "not-exist"), 0)
	if err != nil {
		t.Fatalf("目录不存在不应报错: %v", err)
	}
	if total != 0 || len(runs) != 0 {
		t.Fatalf("want empty, got total=%d len=%d", total, len(runs))
	}

	// 损坏行应被跳过，不影响其余行
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-x")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "not-json{\n" +
		`{"ts":"2026-01-01T00:00:00Z","type":"run.started","run_id":"run-x","goal":"g"}` + "\n" +
		"\n" +
		`{"ts":"2026-01-01T00:00:01Z","type":"run.finished","run_id":"run-x","status":"success"}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, traceFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	runs, total, err = ListRuns(dir, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("want 1 run, got total=%d len=%d", total, len(runs))
	}
	if runs[0].Goal != "g" || runs[0].Status != "success" {
		t.Errorf("损坏行影响了正常解析: %+v", runs[0])
	}
}

func TestListRunsEmptyDirArgument(t *testing.T) {
	runs, total, err := ListRuns("", 0)
	if err != nil || total != 0 || len(runs) != 0 {
		t.Fatalf("空目录参数应返回空结果: runs=%v total=%d err=%v", runs, total, err)
	}
}
