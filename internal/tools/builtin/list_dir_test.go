package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 描述包含 tree 与条目上限 200。
func TestListDirToolDescription(t *testing.T) {
	d := ListDirTool{}.Description()
	if !strings.Contains(d, "tree") {
		t.Errorf("Description 缺少 \"tree\": %q", d)
	}
	if !strings.Contains(d, "200") {
		t.Errorf("Description 缺少 \"200\": %q", d)
	}
}

// path 不是必需参数：required 为空切片。
func TestListDirToolPathNotRequired(t *testing.T) {
	schema := ListDirTool{}.InputSchema()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required 类型错误: %T", schema["required"])
	}
	if len(required) != 0 {
		t.Errorf("required = %v, want 空切片", required)
	}
}

// 不传 path 时默认 "."（不报 schema_error），首行为根目录且子目录带 "/" 后缀。
func TestListDirToolDefaultPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 切到临时目录，使默认路径 "." 指向它
	t.Chdir(dir)

	res, err := ListDirTool{}.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("Invoke 不应返回 error: %v", err)
	}
	if res.IsError {
		t.Fatalf("不传 path 不应报错: %+v", res)
	}

	lines := strings.Split(res.Content, "\n")
	if len(lines) == 0 || !strings.HasSuffix(lines[0], "/") {
		t.Errorf("首行 = %q, 应以 / 结尾", lines[0])
	}
	if !strings.Contains(res.Content, "sub/") {
		t.Errorf("输出应包含子目录 sub/: %q", res.Content)
	}
}

// max_depth=1 只列一层：a 之下的 b/c/d 不应出现。
func TestListDirToolMaxDepth(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b", "c", "d"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := ListDirTool{}.Invoke(context.Background(), map[string]any{
		"path":      dir,
		"max_depth": float64(1),
	})
	if err != nil {
		t.Fatalf("Invoke 不应返回 error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Invoke 报错: %+v", res)
	}

	// 严格断言只输出「根 + a/」两行，等价于 b 及其更深层被裁掉
	lines := strings.Split(res.Content, "\n")
	if len(lines) != 2 {
		t.Fatalf("max_depth=1 应只输出 2 行，实际 %d 行: %q", len(lines), res.Content)
	}
	if !strings.Contains(lines[1], "a/") {
		t.Errorf("第二行 = %q, 应包含 a/", lines[1])
	}
}

// 超过 200 条时输出 (truncated)。
func TestListDirToolTruncation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 201; i++ {
		name := filepath.Join(dir, fmt.Sprintf("f%03d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res, err := ListDirTool{}.Invoke(context.Background(), map[string]any{"path": dir})
	if err != nil {
		t.Fatalf("Invoke 不应返回 error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Invoke 报错: %+v", res)
	}
	if !strings.Contains(res.Content, "(truncated)") {
		t.Errorf("超过 200 条应输出 (truncated): %q", res.Content)
	}
}
