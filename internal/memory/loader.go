package memory

import (
	"os"
	"path/filepath"
	"strings"
)

// LoadContextFile 读取指定路径的 context 文件。
func LoadContextFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ContextPaths 返回全局和项目 context 路径。
// 取不到 home 时 globalPath 留空（LoadContextFile 会返回空串），
// 避免退化成相对路径把项目 context 重复读成全局 context。
func ContextPaths() (globalPath, projectPath string) {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		globalPath = filepath.Join(home, ".numbat", "context.md")
	}
	projectPath = filepath.Join(".numbat", "context.md")
	return
}

// LoadAll 加载全局和项目 context。
func LoadAll() (global, project string) {
	globalPath, projectPath := ContextPaths()
	return LoadContextFile(globalPath), LoadContextFile(projectPath)
}
