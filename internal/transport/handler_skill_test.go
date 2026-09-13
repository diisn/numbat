package transport

import (
	"context"
	"testing"
)

// splitSkillInvocation 按任意空白分隔，最多切一刀，参数侧保留内部空白。
func TestSplitSkillInvocation(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantArgs string
	}{
		{"/skill arg", "skill", "arg"},
		{"/skill", "skill", ""},
		{"/skill\targ", "skill", "arg"},
		{"/skill   a  b", "skill", "a  b"},
		{"/  skill  arg", "skill", "arg"},
	}

	for _, tc := range cases {
		name, args := splitSkillInvocation(tc.in)
		if name != tc.wantName || args != tc.wantArgs {
			t.Errorf("splitSkillInvocation(%q) = (%q, %q), want (%q, %q)",
				tc.in, name, args, tc.wantName, tc.wantArgs)
		}
	}
}

// skill.list 应返回内建技能，且 JSON 字段名与 WebUI SkillMeta 对齐
// （name/description/allowed_tools），供斜杠补全与侧栏使用。
func TestSkillList(t *testing.T) {
	handlers := NewHandlers(HandlerDeps{})
	h, ok := handlers["skill.list"]
	if !ok {
		t.Fatal("skill.list 未注册")
	}

	raw, err := h(context.Background(), nil)
	if err != nil {
		t.Fatalf("skill.list 失败: %v", err)
	}
	list, ok := raw.([]SkillInfo)
	if !ok {
		t.Fatalf("skill.list 返回类型 = %T, want []SkillInfo", raw)
	}

	builtins := map[string]bool{}
	for _, s := range list {
		builtins[s.Name] = true
		if s.Description == "" {
			t.Errorf("skill %q 缺 description", s.Name)
		}
	}
	for _, want := range []string{"init", "orchestrate", "review", "summarize"} {
		if !builtins[want] {
			t.Errorf("skill.list 缺少内建技能 %q", want)
		}
	}
}
