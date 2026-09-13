package agents

import (
	"embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// builtinFS 内建角色配置，编译期打包，避免依赖运行时源码相对路径。
//
//go:embed builtin/*.toml
var builtinFS embed.FS

// Profile 是子 Agent 的角色配置。
type Profile struct {
	Name         string
	Description  string
	SystemPrompt string
	AllowedTools []string
	Model        string
}

// profileFile 是 TOML 角色配置文件格式。
type profileFile struct {
	Agent struct {
		Description  string   `toml:"description"`
		SystemPrompt string   `toml:"system_prompt"`
		AllowedTools []string `toml:"allowed_tools"`
		Model        string   `toml:"model"`
	} `toml:"agent"`
}

// Loader 按三级优先级（项目本地 > 用户全局 > 内建）查找角色配置。
type Loader struct {
	projectDir string // 项目本地 agents 目录，默认 ".numbat/agents"
	userDir    string // 用户全局 agents 目录，默认 "~/.numbat/agents"
}

// NewLoader 创建默认角色加载器。
func NewLoader() *Loader {
	return &Loader{}
}

// NewLoaderWithDirs 创建使用指定目录的角色加载器（主要用于测试）。
func NewLoaderWithDirs(projectDir, userDir string) *Loader {
	return &Loader{projectDir: projectDir, userDir: userDir}
}

// Load 查找指定角色配置；未找到返回 nil。
func (l *Loader) Load(name string) *Profile {
	for _, dir := range l.searchDirs() {
		data, err := os.ReadFile(filepath.Join(dir, name+".toml"))
		if err != nil {
			continue // 目录不存在或文件缺失：继续下一优先级
		}
		return parseProfile(name, data)
	}
	// 内建角色（最后一级）：从 embed 读取（embed 路径恒用 "/"）
	data, err := builtinFS.ReadFile("builtin/" + name + ".toml")
	if err != nil {
		return nil
	}
	return parseProfile(name, data)
}

// searchDirs 返回按优先级排序的目录列表（内建由 embed 兜底，不在此列）。
func (l *Loader) searchDirs() []string {
	var dirs []string
	if l.projectDir != "" {
		dirs = append(dirs, l.projectDir)
	} else {
		dirs = append(dirs, filepath.Join(".numbat", "agents"))
	}
	if l.userDir != "" {
		dirs = append(dirs, l.userDir)
	} else {
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, ".numbat", "agents"))
		}
	}
	return dirs
}

// parseProfile 解析 TOML 内容为 Profile；解析失败返回 nil。
func parseProfile(name string, data []byte) *Profile {
	var pf profileFile
	if err := toml.Unmarshal(data, &pf); err != nil {
		return nil
	}
	return &Profile{
		Name:         name,
		Description:  pf.Agent.Description,
		SystemPrompt: strings.TrimSpace(pf.Agent.SystemPrompt),
		AllowedTools: pf.Agent.AllowedTools,
		Model:        pf.Agent.Model,
	}
}
