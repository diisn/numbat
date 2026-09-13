package builtin

import (
	"context"
	"strings"
	"testing"
)

// 描述包含 64 KB，schema 的 timeout 取值范围为 [1, 120]。
func TestBashToolDescriptionAndSchema(t *testing.T) {
	tool := BashTool{}

	if !strings.Contains(tool.Description(), "64 KB") {
		t.Errorf("Description 缺少 \"64 KB\": %q", tool.Description())
	}

	schema := tool.InputSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema.properties 类型错误: %T", schema["properties"])
	}
	timeout, ok := props["timeout"].(map[string]any)
	if !ok {
		t.Fatalf("properties 缺少 timeout: %v", props)
	}
	if timeout["minimum"] != 1 {
		t.Errorf("timeout.minimum = %v, want 1", timeout["minimum"])
	}
	if timeout["maximum"] != 120 {
		t.Errorf("timeout.maximum = %v, want 120", timeout["maximum"])
	}
}

// 非零退出码：执行一个不存在的可执行名（cmd / sh 均以非零退出码收尾，跨平台稳定）。
func TestBashToolInvokeNonZeroExit(t *testing.T) {
	tool := BashTool{}
	res, err := tool.Invoke(context.Background(), map[string]any{
		"command": "numbat-nonexistent-exec-xyz",
	})
	if err != nil {
		t.Fatalf("Invoke 不应返回 error: %v", err)
	}
	if !res.IsError {
		t.Errorf("IsError = false, want true")
	}
	if res.ErrorType != "runtime_error" {
		t.Errorf("ErrorType = %q, want runtime_error", res.ErrorType)
	}
	if !strings.HasPrefix(res.Content, "[exit ") {
		t.Errorf("Content = %q, 应以 \"[exit \" 开头", res.Content)
	}
}

// 成功路径：echo 是 cmd / sh 均有的内建命令；空输出由 exit 0 验证。
func TestBashToolInvokeSuccessAndEmptyOutput(t *testing.T) {
	tool := BashTool{}

	res, err := tool.Invoke(context.Background(), map[string]any{"command": "echo numbat-output"})
	if err != nil {
		t.Fatalf("Invoke 不应返回 error: %v", err)
	}
	if res.IsError {
		t.Fatalf("成功命令不应报错: %+v", res)
	}
	// 不同平台行尾（\r\n vs \n）不同，去除首尾空白后应等于命令输出
	if got := strings.TrimSpace(res.Content); got != "numbat-output" {
		t.Errorf("Content = %q, want %q", got, "numbat-output")
	}

	// exit 0 在 cmd 与 sh 中都是无输出的内建命令
	res, err = tool.Invoke(context.Background(), map[string]any{"command": "exit 0"})
	if err != nil {
		t.Fatalf("Invoke 不应返回 error: %v", err)
	}
	if res.IsError {
		t.Fatalf("exit 0 不应报错: %+v", res)
	}
	if res.Content != "[no output]" {
		t.Errorf("无输出时 Content = %q, want \"[no output]\"", res.Content)
	}
}

// 缺少 command 时返回 schema_error。
func TestBashToolInvokeMissingCommand(t *testing.T) {
	tool := BashTool{}
	res, err := tool.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Invoke 不应返回 error: %v", err)
	}
	if !res.IsError || res.ErrorType != "schema_error" {
		t.Errorf("res = %+v, want IsError=true ErrorType=schema_error", res)
	}
}
