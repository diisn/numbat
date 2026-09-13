package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 描述包含 1 MB 与 parent。
func TestWriteFileToolDescription(t *testing.T) {
	d := WriteFileTool{}.Description()
	if !strings.Contains(d, "1 MB") {
		t.Errorf("Description 缺少 \"1 MB\": %q", d)
	}
	if !strings.Contains(d, "parent") {
		t.Errorf("Description 缺少 \"parent\": %q", d)
	}
}

// 成功时返回 "wrote N bytes to <path>"，并自动创建父目录、写入内容。
func TestWriteFileToolInvokeSuccess(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.ToSlash(filepath.Join("sub", "deep", "note.txt"))
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	t.Chdir(dir)

	content := "hello 世界"
	res, err := WriteFileTool{}.Invoke(context.Background(), map[string]any{"path": rel, "content": content})
	if err != nil {
		t.Fatalf("Invoke 不应返回 error: %v", err)
	}
	if res.IsError {
		t.Fatalf("Invoke 报错: %+v", res)
	}
	want := fmt.Sprintf("wrote %d bytes to %s", len(content), rel)
	if res.Content != want {
		t.Errorf("Content = %q, want %q", res.Content, want)
	}

	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("文件未写入: %v", err)
	}
	if string(got) != content {
		t.Errorf("文件内容 = %q, want %q", got, content)
	}
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		t.Errorf("父目录未创建: err=%v", err)
	}
}

// 内容超过 1MB 时返回 runtime_error。
func TestWriteFileToolContentTooLarge(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("a", 1*1024*1024+1)

	res, err := WriteFileTool{}.Invoke(context.Background(), map[string]any{
		"path":    filepath.Join(dir, "big.txt"),
		"content": big,
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
	if !strings.Contains(res.Content, "content too large") {
		t.Errorf("Content = %q, 应包含 \"content too large\"", res.Content)
	}
}

// "a..b.txt" 是合法文件名（非路径穿越），"../x.txt" 应被拒绝。
func TestWriteFileToolPathValidation(t *testing.T) {
	t.Run("合法文件名 a..b.txt", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		res, err := WriteFileTool{}.Invoke(context.Background(), map[string]any{"path": "a..b.txt", "content": "x"})
		if err != nil {
			t.Fatalf("Invoke 不应返回 error: %v", err)
		}
		if res.IsError {
			t.Fatalf("合法文件名不应被拒绝: %+v", res)
		}
		if _, err := os.Stat(filepath.Join(dir, "a..b.txt")); err != nil {
			t.Errorf("文件未写入: %v", err)
		}
	})

	t.Run("路径穿越 ../x.txt", func(t *testing.T) {
		res, err := WriteFileTool{}.Invoke(context.Background(), map[string]any{"path": "../x.txt", "content": "x"})
		if err != nil {
			t.Fatalf("Invoke 不应返回 error: %v", err)
		}
		if !res.IsError {
			t.Errorf("IsError = false, want true")
		}
		if !strings.Contains(res.Content, "path traversal") {
			t.Errorf("Content = %q, 应包含 \"path traversal\"", res.Content)
		}
	})
}
