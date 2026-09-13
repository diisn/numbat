package channel

import "testing"

// staticResolver 返回名称即配置的 Resolver，用于匹配逻辑测试。
func staticResolver(names ...string) Resolver {
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	return func(name string) (*AgentConfig, bool) {
		if !known[name] {
			return nil, false
		}
		return &AgentConfig{Name: name, Model: "model-" + name}, true
	}
}

func TestRouterPriority(t *testing.T) {
	r := NewRouter(
		"default-agent",
		map[string]string{"alice": "sender-agent", "bob": "missing-agent"},
		map[string]string{"tg-main": "channel-agent"},
		staticResolver("default-agent", "sender-agent", "channel-agent"),
	)

	cases := []struct {
		name        string
		channelName string
		senderID    string
		want        string // 期望命中的 Agent 名；空表示拒绝（返回 nil）
	}{
		// 发送者精确匹配优先于通道名匹配
		{"sender beats channel", "tg-main", "alice", "sender-agent"},
		{"sender match on any channel", "feishu", "alice", "sender-agent"},
		// 发送者无规则时走通道名匹配
		{"channel match", "tg-main", "carol", "channel-agent"},
		// 均无规则时回退默认 Agent
		{"fallback to default", "feishu", "carol", "default-agent"},
		// 发送者规则命中但 Agent 解析失败：逐级回退到通道名
		{"sender resolve miss falls to channel", "tg-main", "bob", "channel-agent"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Route(c.channelName, c.senderID)
			if c.want == "" {
				if got != nil {
					t.Fatalf("want nil agent, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want agent %q, got nil", c.want)
			}
			if got.Name != c.want {
				t.Fatalf("want agent %q, got %q", c.want, got.Name)
			}
		})
	}
}

func TestRouterConfigFields(t *testing.T) {
	// 校验 Route 返回的配置携带 model/system_prompt/allowed_tools。
	r := NewRouter(
		"executor",
		nil,
		nil,
		func(name string) (*AgentConfig, bool) {
			if name != "executor" {
				return nil, false
			}
			return &AgentConfig{
				Name:         "executor",
				Model:        "claude-sonnet-4-6",
				SystemPrompt: "你是执行专家",
				AllowedTools: []string{"bash", "read_file"},
			}, true
		},
	)
	got := r.Route("telegram", "someone")
	if got == nil {
		t.Fatal("want agent, got nil")
	}
	if got.Name != "executor" || got.Model != "claude-sonnet-4-6" || got.SystemPrompt != "你是执行专家" {
		t.Fatalf("agent config fields not propagated: %+v", got)
	}
	if len(got.AllowedTools) != 2 || got.AllowedTools[0] != "bash" {
		t.Fatalf("unexpected allowed_tools: %v", got.AllowedTools)
	}
}

func TestRouterRejectWhenDefaultMissing(t *testing.T) {
	// 无默认 Agent 且所有规则都解析失败：返回 nil（由调用方拒绝消息）。
	r := NewRouter(
		"",
		map[string]string{"alice": "ghost"},
		nil,
		staticResolver(), // 任何名称都解析失败
	)
	if got := r.Route("tg-main", "alice"); got != nil {
		t.Fatalf("want nil (reject), got %+v", got)
	}
	if got := r.Route("tg-main", "nobody"); got != nil {
		t.Fatalf("want nil (reject), got %+v", got)
	}
}
