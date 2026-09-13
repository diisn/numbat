package context

import (
	"strings"
	"testing"
)

// 记忆层全空时 SystemPrompt 必须恰好等于 base，不得附加任何前后缀或分隔符。
func TestSystemPromptExactlyBaseWhenNoMemory(t *testing.T) {
	c := NewExecutionContext("run-1", "goal", 5)
	base := "你是基础提示词"
	if got := c.SystemPrompt(base); got != base {
		t.Fatalf("SystemPrompt = %q, want %q", got, base)
	}
}

// GlobalContext 非空时 base 在前、记忆段在后。
func TestSystemPromptAppendsGlobalContextAfterBase(t *testing.T) {
	c := NewExecutionContext("run-1", "goal", 5)
	c.GlobalContext = "全局记忆内容"
	base := "BASE_PROMPT"

	got := c.SystemPrompt(base)
	if !strings.Contains(got, base) {
		t.Fatalf("SystemPrompt 缺少 base: %q", got)
	}
	if !strings.Contains(got, "## Global Context") {
		t.Fatalf("SystemPrompt 缺少 ## Global Context 段: %q", got)
	}
	if strings.Index(got, base) >= strings.Index(got, "## Global Context") {
		t.Fatalf("base 应出现在 ## Global Context 之前: %q", got)
	}
}

// SystemPromptOverride 替换 base，但记忆层仍追加在 override 之后。
func TestSystemPromptOverrideReplacesBaseButKeepsMemory(t *testing.T) {
	c := NewExecutionContext("run-1", "goal", 5)
	c.SystemPromptOverride = "OVERRIDE_PROMPT"
	c.GlobalContext = "全局记忆内容"

	got := c.SystemPrompt("BASE_PROMPT")
	if !strings.Contains(got, "OVERRIDE_PROMPT") {
		t.Fatalf("SystemPrompt 应包含 override: %q", got)
	}
	if strings.Contains(got, "BASE_PROMPT") {
		t.Fatalf("SystemPrompt 不应包含被替换的 base: %q", got)
	}
	if !strings.Contains(got, "## Global Context") {
		t.Fatalf("override 场景下记忆层仍应注入: %q", got)
	}
}

// 三个记忆段同时存在时顺序为 Global → Project → Session Notes。
func TestSystemPromptMemoryOrder(t *testing.T) {
	c := NewExecutionContext("run-1", "goal", 5)
	c.GlobalContext = "G"
	c.ProjectContext = "P"
	c.SessionNotes = "S"

	got := c.SystemPrompt("base")
	gi := strings.Index(got, "## Global Context")
	pi := strings.Index(got, "## Project Context")
	si := strings.Index(got, "## Session Notes")
	if gi < 0 || pi < 0 || si < 0 {
		t.Fatalf("三个记忆段应全部出现: %q", got)
	}
	if !(gi < pi && pi < si) {
		t.Fatalf("记忆段顺序错误 (global=%d project=%d session=%d): %q", gi, pi, si, got)
	}
}

// MarkSuccess / MarkFailed 写入字段，且两者之后 IsDone 均为 true。
func TestMarkSuccessAndFailed(t *testing.T) {
	c := NewExecutionContext("run-1", "goal", 5)
	if c.IsDone() {
		t.Fatal("新建的 run 不应已结束")
	}

	c.MarkSuccess("ok")
	if c.Status != "success" || c.Result != "ok" {
		t.Errorf("MarkSuccess 后 Status/Result = %q/%q, want success/ok", c.Status, c.Result)
	}
	if !c.IsDone() {
		t.Error("MarkSuccess 后 IsDone 应为 true")
	}

	f := NewExecutionContext("run-2", "goal", 5)
	f.MarkFailed("llm_error", "boom")
	if f.Status != "failed" || f.Reason != "llm_error" || f.Result != "boom" {
		t.Errorf("MarkFailed 后 Status/Reason/Result = %q/%q/%q, want failed/llm_error/boom", f.Status, f.Reason, f.Result)
	}
	if !f.IsDone() {
		t.Error("MarkFailed 后 IsDone 应为 true")
	}
}
