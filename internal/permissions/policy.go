package permissions

import (
	"fmt"
	"regexp"
)

// Decision 表示权限决策。
type Decision string

// 权限决策常量。
const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
	Ask   Decision = "ask"
)

// ToolPolicy 表示单个工具的策略。
type ToolPolicy struct {
	Default       Decision
	AllowPatterns []string
	DenyPatterns  []string
}

// DefaultPolicies 返回默认策略。
func DefaultPolicies() map[string]ToolPolicy {
	return map[string]ToolPolicy{
		"bash":       {Default: Ask},
		"write_file": {Default: Ask},
		"read_file":  {Default: Allow},
		"list_dir":   {Default: Allow},
		"note_save":  {Default: Allow},
	}
}

var outsideCWDHeuristics = []*regexp.Regexp{
	regexp.MustCompile(`(^|\s)/[^\s]`),
	regexp.MustCompile(`(^|\s)~`),
	regexp.MustCompile(`(^|\s)\.\.(/|$|\s)`),
	regexp.MustCompile(`\$\{?HOME\b`),
	regexp.MustCompile(`\$\{?PWD\b`),
	regexp.MustCompile(`(^|\s|;|&&|\|\|)cd(\s|$)`),
}

// MatchesOutsideCWD 判断 bash 命令是否命中 outside-cwd 启发式规则。
func MatchesOutsideCWD(command string) bool {
	for _, re := range outsideCWDHeuristics {
		if re.MatchString(command) {
			return true
		}
	}
	return false
}

// ParamPreview 生成参数摘要。
func ParamPreview(toolName string, params map[string]any) string {
	previewKey := map[string]string{
		"bash":       "command",
		"read_file":  "path",
		"write_file": "path",
		"list_dir":   "path",
		"note_save":  "content",
	}

	const maxLen = 60
	key := previewKey[toolName]
	if key != "" {
		if val, ok := params[key]; ok {
			s := fmt.Sprintf("%s=%v", key, val)
			if len(s) > maxLen {
				return s[:maxLen] + "…"
			}
			return s
		}
	}
	s := fmt.Sprintf("%+v", params)
	if len(s) > maxLen {
		return s[:maxLen] + "…"
	}
	return s
}
