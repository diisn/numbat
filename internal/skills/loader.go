package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// builtinSkillFS 内建 skill，编译期打包（init/orchestrate/review/summarize），避免依赖运行时源码相对路径。
//
//go:embed builtin/*.md
var builtinSkillFS embed.FS

// Skill 表示一个 Markdown 格式的技能文件。
type Skill struct {
	Name                 string
	Description          string
	SystemPromptTemplate string
	AllowedTools         []string
}

var frontmatterRe = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n`)

// Loader 按优先级（项目本地 > 用户全局）查找 skill。
type Loader struct {
	projectDir string // 项目本地 skills 目录，默认 ".numbat/skills"
	userDir    string // 用户全局 skills 目录，默认 "~/.numbat/skills"
}

// NewLoader 创建默认的 skill 加载器。
func NewLoader() *Loader {
	return &Loader{}
}

// NewLoaderWithDirs 创建使用指定目录的 skill 加载器（主要用于测试）。
func NewLoaderWithDirs(projectDir, userDir string) *Loader {
	return &Loader{projectDir: projectDir, userDir: userDir}
}

// searchDirs 返回按优先级排序的搜索目录列表。
func (l *Loader) searchDirs() []string {
	dirs := []string{}
	if l.projectDir != "" {
		dirs = append(dirs, l.projectDir)
	} else {
		dirs = append(dirs, filepath.Join(".numbat", "skills"))
	}
	if l.userDir != "" {
		dirs = append(dirs, l.userDir)
	} else {
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, ".numbat", "skills"))
		}
	}
	return dirs
}

// Resolve 按名称查找 skill，同时支持扁平文件（name.md）和目录式（name/SKILL.md）。
// 优先级：项目本地 > 用户全局 > 内建（embed）。
func (l *Loader) Resolve(name string) (*Skill, error) {
	for _, dir := range l.searchDirs() {
		for _, candidate := range []string{
			filepath.Join(dir, name+".md"),
			filepath.Join(dir, name, "SKILL.md"),
		} {
			skill, err := parseSkillFile(candidate)
			if err == nil {
				return skill, nil
			}
		}
	}
	// 内建 skill（最后一级）：从 embed 读取（embed 路径恒用 "/"）
	if data, err := builtinSkillFS.ReadFile("builtin/" + name + ".md"); err == nil {
		if skill, perr := parseSkillBytes(name+".md", data); perr == nil {
			return skill, nil
		}
	}
	return nil, fmt.Errorf("skill not found: %s", name)
}

// ListAllSkills 列出所有可用 skill（内建 + 用户全局 + 项目本地），优先级从低到高覆盖。
func (l *Loader) ListAllSkills() []*Skill {
	seen := make(map[string]*Skill)
	// 内建为最低优先级，先放入
	for _, s := range l.listBuiltinSkills() {
		seen[s.Name] = s
	}
	// 用户全局、项目本地依次覆盖同名内建
	dirs := l.searchDirs()
	for i := len(dirs) - 1; i >= 0; i-- {
		l.scanDir(dirs[i], seen)
	}
	out := make([]*Skill, 0, len(seen))
	for _, s := range seen {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// listBuiltinSkills 解析所有内建 skill（embed）。
func (l *Loader) listBuiltinSkills() []*Skill {
	matches, err := fs.Glob(builtinSkillFS, "builtin/*.md")
	if err != nil {
		return nil
	}
	var out []*Skill
	for _, m := range matches {
		data, err := builtinSkillFS.ReadFile(m)
		if err != nil {
			continue
		}
		if s, err := parseSkillBytes(filepath.Base(m), data); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// scanDir 扫描单个目录下的扁平与目录式 skill 并入 seen（后写覆盖先写）。
func (l *Loader) scanDir(dir string, seen map[string]*Skill) {
	// 扁平文件
	if matches, err := filepath.Glob(filepath.Join(dir, "*.md")); err == nil {
		for _, f := range matches {
			if skill, err := parseSkillFile(f); err == nil {
				seen[skill.Name] = skill
			}
		}
	}
	// 目录式
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				skill, err := parseSkillFile(filepath.Join(dir, e.Name(), "SKILL.md"))
				if err == nil {
					seen[skill.Name] = skill
				}
			}
		}
	}
}

// RenderPrompt 将 skill 模板中的 $ARGUMENTS 替换为用户传入的参数。
func (l *Loader) RenderPrompt(s *Skill, arguments string) string {
	return strings.ReplaceAll(s.SystemPromptTemplate, "$ARGUMENTS", arguments)
}

// parseSkillFile 读取并解析单个 skill 文件。
func parseSkillFile(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSkillBytes(filepath.Base(path), data)
}

// parseSkillBytes 解析 skill 内容：frontmatter（name/description/allowed_tools）+ 正文。
func parseSkillBytes(baseName string, data []byte) (*Skill, error) {
	text := string(data)
	name := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	description := ""
	body := text
	var allowedTools []string

	if m := frontmatterRe.FindStringSubmatch(text); m != nil {
		body = text[len(m[0]):] // 去掉 frontmatter 后的正文
		lines := strings.Split(m[1], "\n")
		for i := 0; i < len(lines); i++ {
			stripped := strings.TrimSpace(lines[i])
			switch {
			case strings.HasPrefix(stripped, "name:"):
				name = strings.Trim(strings.TrimSpace(stripped[len("name:"):]), `"'`)
			case strings.HasPrefix(stripped, "description:"):
				val := strings.TrimSpace(stripped[len("description:"):])
				if val == ">" || val == "|" {
					fold := val == ">"
					var parts []string
					i++
					for i < len(lines) {
						line := lines[i]
						if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
							parts = append(parts, strings.TrimSpace(line))
							i++
							continue
						}
						break
					}
					if fold {
						description = strings.Join(parts, " ")
					} else {
						description = strings.Join(parts, "\n")
					}
					description = strings.TrimSpace(description)
					i-- // for 循环会再 +1
				} else {
					description = strings.Trim(strings.TrimSpace(val), `"'`)
				}
			case strings.HasPrefix(stripped, "- "):
				allowedTools = append(allowedTools, strings.TrimSpace(stripped[2:]))
			}
		}
	}

	return &Skill{
		Name:                 name,
		Description:          description,
		SystemPromptTemplate: strings.TrimSpace(body),
		AllowedTools:         allowedTools,
	}, nil
}
