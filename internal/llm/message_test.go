package llm

import (
	"encoding/json"
	"testing"
)

// Anthropic 兼容端点（如 DeepSeek）要求 tool_use 块必须带 input 字段。
// Input 使用 omitempty 会在空值/空 map 时整字段省略，导致
// 400 "messages[i].content: missing field `input`"。
func TestContentBlock_MarshalToolUseAlwaysCarriesInput(t *testing.T) {
	cases := []struct {
		name  string
		block ContentBlock
	}{
		{"nil input", ContentBlock{Type: "tool_use", ID: "tu-1", Name: "list_dir"}},
		{"empty input", ContentBlock{Type: "tool_use", ID: "tu-1", Name: "list_dir", Input: map[string]any{}}},
		{"non-empty input", ContentBlock{Type: "tool_use", ID: "tu-1", Name: "bash", Input: map[string]any{"command": "ls"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.block)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if _, ok := raw["input"]; !ok {
				t.Fatalf("tool_use 块缺少 input 字段: %s", data)
			}
			if raw["input"] == nil {
				t.Fatalf("tool_use 块的 input 不应为 null: %s", data)
			}
		})
	}
}

// 非 tool_use 块不应出现 input 字段，保持与原 omitempty 行为一致，
// 避免向端点发送多余字段。
func TestContentBlock_MarshalNonToolUseOmitsInput(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "hi"},
		{Type: "tool_result", ToolUseID: "tu-1", Content: "ok"},
		{Type: "thinking", Thinking: "hmm", Signature: "sig"},
	}

	for _, block := range blocks {
		data, err := json.Marshal(block)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if _, ok := raw["input"]; ok {
			t.Fatalf("%s 块不应有 input 字段: %s", block.Type, data)
		}
	}
}

// 其余字段的序列化行为不能被自定义 MarshalJSON 破坏。
func TestContentBlock_MarshalPreservesFields(t *testing.T) {
	data, err := json.Marshal(ContentBlock{
		Type:      "tool_use",
		ID:        "tu-9",
		Name:      "bash",
		Input:     map[string]any{"command": "pwd"},
		ToolUseID: "tu-8",
		Content:   "out",
		IsError:   true,
		Thinking:  "think",
		Signature: "sig",
		Text:      "txt",
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	want := map[string]any{
		"type":        "tool_use",
		"id":          "tu-9",
		"name":        "bash",
		"input":       map[string]any{"command": "pwd"},
		"tool_use_id": "tu-8",
		"content":     "out",
		"is_error":    true,
		"thinking":    "think",
		"signature":   "sig",
		"text":        "txt",
	}
	for k, v := range want {
		if got[k] == nil {
			t.Errorf("字段 %q 丢失", k)
			continue
		}
		if !jsonEqual(got[k], v) {
			t.Errorf("字段 %q = %v, want %v", k, got[k], v)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
