package permissions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// policy.toml 往返：always_allow 决策落盘为 [always] 节下的 bash = "allow"。
func TestPolicyFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.toml")
	perm := NewManager(path, 0)

	// onAsk 被调用说明 pending 已注册；此时由另一 goroutine 响应 always_allow
	asked := make(chan struct{})
	go func() {
		<-asked
		perm.Respond("tu-always", "always_allow")
	}()

	allowed, decision, err := perm.CheckAndWait(context.Background(), "tu-always", "bash",
		map[string]any{"command": "go build ./..."}, "sess-1", func() { close(asked) })
	if err != nil {
		t.Fatalf("CheckAndWait: %v", err)
	}
	if !allowed || decision != "always_allow" {
		t.Fatalf("allowed=%v decision=%q, want true/always_allow", allowed, decision)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 policy.toml 失败: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[always]") {
		t.Errorf("policy.toml 缺少 [always] 节: %q", content)
	}
	if !strings.Contains(content, `bash = "allow"`) {
		t.Errorf("policy.toml 缺少 bash = \"allow\": %q", content)
	}
}

// 兼容旧格式（扁平顶层键）：加载后直接命中缓存返回 auto_allow，不进入审批。
func TestLoadLegacyFlatPolicyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.toml")
	if err := os.WriteFile(path, []byte("bash = \"allow\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	perm := NewManager(path, 0)

	asked := false
	allowed, decision, err := perm.CheckAndWait(context.Background(), "tu-1", "bash",
		map[string]any{"command": "go build ./..."}, "sess-1", func() { asked = true })
	if err != nil {
		t.Fatalf("CheckAndWait: %v", err)
	}
	if !allowed || decision != "auto_allow" {
		t.Fatalf("allowed=%v decision=%q, want true/auto_allow", allowed, decision)
	}
	if asked {
		t.Error("旧格式命中缓存时不应进入审批")
	}
}

// 新格式读取：[always] 下的 deny 决策直接返回 auto_deny。
func TestLoadAlwaysSectionDeny(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.toml")
	if err := os.WriteFile(path, []byte("[always]\nbash = \"deny\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	perm := NewManager(path, 0)

	allowed, decision, err := perm.CheckAndWait(context.Background(), "tu-1", "bash",
		map[string]any{"command": "go build ./..."}, "sess-1", func() {})
	if err != nil {
		t.Fatalf("CheckAndWait: %v", err)
	}
	if allowed || decision != "auto_deny" {
		t.Fatalf("allowed=%v decision=%q, want false/auto_deny", allowed, decision)
	}
}
