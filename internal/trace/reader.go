package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultRunLimit 是 run.list 未指定 limit 时返回的 run 数量上限。
	DefaultRunLimit = 30
	// maxEventsPerRun 限制单个 run 返回的事件条数，避免响应体过大。
	maxEventsPerRun = 100
	// traceFileName 与 Writer 写入的文件名保持一致。
	traceFileName = "events.jsonl"
)

// TokenUsage 取自该 run 最后一次 LLM 调用的用量（与前端实时徽章口径一致）。
type TokenUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	ContextPct   float64 `json:"context_pct"`
}

// ToolSummary 汇总一次工具调用。
type ToolSummary struct {
	ToolName  string `json:"tool_name"`
	ToolUseID string `json:"tool_use_id"`
	// running | success | failed
	Status    string `json:"status"`
	ElapsedMs int    `json:"elapsed_ms"`
}

// SubagentSummary 汇总一个子 Agent。
type SubagentSummary struct {
	RunID       string `json:"run_id"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// EventSummary 是轨迹中的一条事件，只保留展示所需的轻量字段。
type EventSummary struct {
	Type   string `json:"type"`
	TS     string `json:"ts"`
	Detail string `json:"detail"`
}

// RunSummary 是一次 run 的可观测摘要，由 <runsDir>/<runID>/events.jsonl 聚合而成。
type RunSummary struct {
	RunID      string            `json:"run_id"`
	Goal       string            `json:"goal"`
	Status     string            `json:"status"`
	StartedAt  string            `json:"started_at"`
	FinishedAt string            `json:"finished_at"`
	DurationMs int               `json:"duration_ms"`
	Skill      string            `json:"skill,omitempty"`
	Tokens     TokenUsage        `json:"tokens"`
	Tools      []ToolSummary     `json:"tools"`
	Subagents  []SubagentSummary `json:"subagents"`
	Events     []EventSummary    `json:"events"`
}

// traceLine 覆盖轨迹行中可能需要读取的全部字段。
// 只保留轻量字段：llm.request 的 messages 等大字段不解析。
type traceLine struct {
	TS     string `json:"ts"`
	Type   string `json:"type"`
	RunID  string `json:"run_id"`
	Goal   string `json:"goal"`
	Status string `json:"status"`
	Text   string `json:"text"`

	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	ContextPct   float64 `json:"context_pct"`

	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
	ElapsedMs int    `json:"elapsed_ms"`
	Error     string `json:"error"`

	Description string `json:"description"`
	ParentRunID string `json:"parent_run_id"`

	SkillName string `json:"skill_name"`

	OriginalTokens int `json:"original_tokens"`
	SummaryTokens  int `json:"summary_tokens"`
}

// ListRuns 扫描 runsDir 下的 run 轨迹并聚合为摘要，按开始时间倒序返回。
//
// 子 Agent 的 run 拥有独立轨迹目录，不单独列出，而是归并到父 run 的 Subagents 中
// （子 run 的轨迹里带有一条 RunID 等于自身的 subagent.started，据此识别）。
// 返回值为 (摘要列表, 顶层 run 总数)。
func ListRuns(runsDir string, limit int) ([]RunSummary, int, error) {
	if runsDir == "" {
		return []RunSummary{}, 0, nil
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []RunSummary{}, 0, nil
		}
		return nil, 0, err
	}

	byID := make(map[string]RunSummary)
	children := make(map[string][]SubagentSummary)
	isChild := make(map[string]bool)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runID := e.Name()
		lines, err := readTrace(filepath.Join(runsDir, runID, traceFileName))
		if err != nil || len(lines) == 0 {
			continue
		}
		byID[runID] = summarize(runID, lines)

		for _, ln := range lines {
			if ln.Type == "subagent.started" && ln.RunID == runID {
				isChild[runID] = true
				children[ln.ParentRunID] = append(children[ln.ParentRunID], SubagentSummary{
					RunID:       runID,
					Description: ln.Description,
					Status:      byID[runID].Status,
				})
			}
		}
	}

	runs := make([]RunSummary, 0, len(byID))
	for id, s := range byID {
		if isChild[id] {
			continue
		}
		if subs, ok := children[id]; ok {
			s.Subagents = subs
		}
		runs = append(runs, s)
	}

	// ts 为定长 RFC3339(UTC)，字典序等价于时间序
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt > runs[j].StartedAt })

	total := len(runs)
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, total, nil
}

// readTrace 读取并解析单个 run 的轨迹文件，跳过损坏行。
func readTrace(path string) ([]traceLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []traceLine
	sc := bufio.NewScanner(f)
	// llm.request 单行可能很大（含完整消息序列），放宽单行上限
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ln traceLine
		if err := json.Unmarshal(raw, &ln); err != nil {
			continue
		}
		if ln.Type == "" {
			continue
		}
		lines = append(lines, ln)
	}
	return lines, sc.Err()
}

// summarize 将一次 run 的事件序列聚合为摘要。
func summarize(runID string, lines []traceLine) RunSummary {
	s := RunSummary{
		RunID:     runID,
		Status:    "running", // 与 ExecContext.Status 初值一致
		Tools:     []ToolSummary{},
		Subagents: []SubagentSummary{},
		Events:    []EventSummary{},
	}
	toolIndex := make(map[string]int)
	var startTS, endTS string

	for _, ln := range lines {
		switch ln.Type {
		case "run.started":
			s.Goal = ln.Goal
			startTS = ln.TS
		case "run.finished":
			if ln.Status != "" {
				s.Status = ln.Status
			}
			endTS = ln.TS
		case "llm.usage":
			s.Tokens = TokenUsage{
				InputTokens:  ln.InputTokens,
				OutputTokens: ln.OutputTokens,
				ContextPct:   ln.ContextPct,
			}
		case "tool.call_started":
			toolIndex[ln.ToolUseID] = len(s.Tools)
			s.Tools = append(s.Tools, ToolSummary{
				ToolName:  ln.ToolName,
				ToolUseID: ln.ToolUseID,
				Status:    "running",
			})
		case "tool.call_finished":
			if i, ok := toolIndex[ln.ToolUseID]; ok {
				s.Tools[i].Status = "success"
				s.Tools[i].ElapsedMs = ln.ElapsedMs
			}
		case "tool.call_failed":
			if i, ok := toolIndex[ln.ToolUseID]; ok {
				s.Tools[i].Status = "failed"
				s.Tools[i].ElapsedMs = ln.ElapsedMs
			}
		case "skill.invoked":
			s.Skill = ln.SkillName
		}

		if len(s.Events) < maxEventsPerRun {
			s.Events = append(s.Events, EventSummary{Type: ln.Type, TS: ln.TS, Detail: eventDetail(ln)})
		}
	}

	s.StartedAt = startTS
	s.FinishedAt = endTS
	if startTS != "" && endTS != "" {
		st, errS := time.Parse(time.RFC3339, startTS)
		et, errE := time.Parse(time.RFC3339, endTS)
		if errS == nil && errE == nil {
			s.DurationMs = int(et.Sub(st).Milliseconds())
		}
	}
	return s
}

// eventDetail 生成事件的一行人读描述。
func eventDetail(ln traceLine) string {
	switch ln.Type {
	case "run.started":
		return "goal: " + truncate(ln.Goal, 80)
	case "run.finished":
		return "status: " + ln.Status
	case "llm.request":
		return "请求 LLM"
	case "llm.response":
		return truncate(ln.Text, 80)
	case "tool.call_started":
		return ln.ToolName
	case "tool.call_finished":
		return fmt.Sprintf("%s — %dms", ln.ToolName, ln.ElapsedMs)
	case "tool.call_failed":
		return fmt.Sprintf("%s ✕ %s", ln.ToolName, truncate(ln.Error, 80))
	case "permission.requested":
		return ln.ToolName
	case "permission.granted":
		return ln.ToolName + " 已授权"
	case "permission.denied":
		return ln.ToolName + " 已拒绝"
	case "subagent.started":
		return truncate(ln.Description, 80)
	case "subagent.finished":
		return "status: " + ln.Status
	case "skill.invoked":
		return ln.SkillName
	case "context.compacted":
		return fmt.Sprintf("%d → %d tokens", ln.OriginalTokens, ln.SummaryTokens)
	}
	return ""
}

// truncate 截断过长文本，并把换行压平为空格。
func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
