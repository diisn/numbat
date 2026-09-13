package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// line 表示消息区域中的一行可渲染内容。
type line interface {
	View(width int, selected bool) string
	IsTool() bool
	PlainText() string
}

// ansiPattern 匹配 ANSI 转义序列（颜色/样式等）。
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripANSI 去除字符串中的 ANSI 转义序列。
func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// wrapText 按终端宽度对文本做 ANSI 感知换行；width<=0 时原样返回。
func wrapText(s string, width int) string {
	if width < 1 {
		return s
	}
	return ansi.Wrap(s, width, "")
}

// wrapIndent 将 s 换行到 width，并给续行补上 indent，避免换行后缩进丢失。
func wrapIndent(s, indent string, width int) string {
	avail := width - ansi.StringWidth(indent)
	if avail < 1 {
		avail = 1
	}
	wrapped := ansi.Wrap(s, avail, "")
	if indent == "" {
		return wrapped
	}
	return strings.ReplaceAll(wrapped, "\n", "\n"+indent)
}

// streamLine 记录一次流式回复当前输出位置，逐 token 就地更新消息行。
type streamLine struct {
	idx  int    // 对应 model.lines 中的行索引
	text string // 已累积的 token 文本
}

// 消息标签：渲染为独立一行，使说话方在消息流中一眼可辨。
const (
	agentLabel = "Agent"
	userLabel  = "You"
)

// textLine 是普通文本行。
// label 非空时渲染为独立标签行（如 "Agent:"），正文另起一行，labelStyle 决定其配色；
// text 存已渲染文本；markdown 非空时存原始 Markdown，渲染推迟到 View，
// 这样终端宽度变化后可按新宽度重排（原始 Markdown 必须保留）。
type textLine struct {
	label      string
	labelStyle lipgloss.Style
	text       string
	markdown   string
}

func (l *textLine) View(width int, selected bool) string {
	content := l.text
	if l.markdown != "" {
		content = renderMarkdown(l.markdown, width)
	}
	content = wrapText(content, width)
	if l.label != "" {
		content = l.labelStyle.Render(l.label+":") + "\n" + content
	}
	if selected {
		return selectionStyle.Render(content)
	}
	return content
}

func (l *textLine) IsTool() bool { return false }

func (l *textLine) PlainText() string {
	if l.markdown != "" {
		return l.markdown
	}
	return stripANSI(l.text)
}

// maxOutputLines 限制工具块展开后 output 的最大显示行数。
const maxOutputLines = 20

// toolLine 是工具调用块，支持展开/收起。
type toolLine struct {
	toolUseID    string
	toolName     string
	params       map[string]any
	output       string
	elapsed      int
	isError      bool
	expanded     bool
	finished     bool
	spinnerView  string // 由 model 在每次渲染前更新，未完成时显示
	scrollOffset int    // output 区域的滚动偏移（行号）
}

func (l *toolLine) View(width int, selected bool) string {
	stateClr := toolStateColor(l.finished, l.isError)
	gutter := lipgloss.NewStyle().Foreground(stateClr).Render("│")

	// 展开/收起/运行中标记
	mark := "▶"
	if !l.finished {
		mark = l.spinnerView
	} else if l.expanded {
		mark = "▼"
	}

	// 状态文本：✓ elapsed / ✕ failed / spinner running
	var statusText string
	if l.finished {
		if l.isError {
			statusText = lipgloss.NewStyle().Foreground(colorError).Render(" ✕ failed")
		} else {
			statusText = lipgloss.NewStyle().Foreground(colorSuccess).Render(fmt.Sprintf(" ✓ %dms", l.elapsed))
		}
	} else {
		statusText = lipgloss.NewStyle().Foreground(stateClr).Render(" " + l.spinnerView + " running")
	}

	// summary 行：mark + gutter(状态色) + 工具名 + 参数 + 状态
	summary := fmt.Sprintf("%s %s %s %s%s",
		mark,
		gutter,
		toolStyle.Render("Tool:"),
		l.toolName,
		paramSummary(l.toolName, l.params),
	) + statusText

	if !l.expanded || !l.finished {
		summary = wrapText(summary, width)
		if selected {
			return selectionStyle.Render(summary)
		}
		return summary
	}

	// 语法高亮 output
	lang := detectOutputLanguage(l.toolName, l.params)
	outputDisplay := l.output
	if lang != "" || looksLikeJSON(l.output) {
		if lang == "" {
			lang = "json"
		}
		highlighted := syntaxHighlight(l.output, lang)
		if highlighted != "" {
			outputDisplay = highlighted
		}
	}

	// 按 limit 截取可见区域
	outputLines := strings.Split(outputDisplay, "\n")
	totalLines := len(outputLines)
	start := l.scrollOffset
	if start < 0 {
		start = 0
	}
	if start > totalLines-maxOutputLines && totalLines > maxOutputLines {
		start = totalLines - maxOutputLines
	}
	end := start + maxOutputLines
	if end > totalLines {
		end = totalLines
	}
	visibleOutput := strings.Join(outputLines[start:end], "\n")

	// 滚动指示器
	scrollHint := ""
	if totalLines > maxOutputLines {
		pct := 0
		if totalLines > 0 {
			pct = start * 100 / (totalLines - maxOutputLines)
		}
		scrollHint = systemStyle.Render(fmt.Sprintf(" [%d-%d/%d lines %d%%]", start+1, end, totalLines, pct))
	}

	// 展开内容：每行带 gutter 前缀，与 summary 行的 gutter 对齐（2 空格缩进）
	// 超过终端宽度时换行，续行补同样的缩进，避免被裁掉
	bodyIndent := "  " + gutter + " "
	outIndent := bodyIndent + "  "
	var detail strings.Builder
	detail.WriteString("\n" + wrapIndent(bodyIndent+systemStyle.Render("params: ")+paramSummary(l.toolName, l.params), bodyIndent, width))
	detail.WriteString("\n" + wrapIndent(bodyIndent+systemStyle.Render("output:")+scrollHint, bodyIndent, width))
	for _, line := range strings.Split(visibleOutput, "\n") {
		detail.WriteString("\n" + wrapIndent(outIndent+line, outIndent, width))
	}
	detail.WriteString("\n  " + gutter)

	result := wrapText(summary, width) + detail.String()
	if selected {
		result = selectionStyle.Render(result)
	}
	return result
}

func (l *toolLine) IsTool() bool { return true }
func (l *toolLine) PlainText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tool: %s\n", l.toolName)
	if l.output != "" {
		fmt.Fprintf(&b, "Output:\n%s\n", l.output)
	}
	return b.String()
}

// paramSummary 根据工具名提取关键参数做摘要。
func paramSummary(toolName string, params map[string]any) string {
	keysByTool := map[string][]string{
		"read_file":  {"path"},
		"write_file": {"path"},
		"list_dir":   {"path", "max_depth"},
		"bash":       {"command"},
		"note_save":  {"content"},
	}
	keys := keysByTool[toolName]
	if len(keys) == 0 {
		for k := range params {
			keys = append(keys, k)
			if len(keys) >= 2 {
				break
			}
		}
	}
	parts := []string{}
	for _, k := range keys {
		if v, ok := params[k]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, ", ")
}

// syntaxHighlight 对 code 按 language 做语法高亮，失败时返回原文。
func syntaxHighlight(code, language string) string {
	if strings.TrimSpace(code) == "" {
		return code
	}
	lexer := lexers.Get(language)
	if lexer == nil {
		// 尝试自动检测
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		return code
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	iter, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var b strings.Builder
	if err := formatter.Format(&b, style, iter); err != nil {
		return code
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// detectOutputLanguage 根据工具名和参数推断输出内容的语言，用于语法高亮。
func detectOutputLanguage(toolName string, params map[string]any) string {
	switch toolName {
	case "bash":
		return "shell-session"
	case "read_file", "write_file":
		if path, ok := params["path"].(string); ok {
			ext := filepath.Ext(path)
			switch ext {
			case ".go":
				return "go"
			case ".py":
				return "python"
			case ".js", ".ts":
				return "javascript"
			case ".json":
				return "json"
			case ".yaml", ".yml":
				return "yaml"
			case ".md":
				return "markdown"
			case ".toml":
				return "toml"
			case ".html":
				return "html"
			case ".css":
				return "css"
			case ".rs":
				return "rust"
			case ".sh":
				return "bash"
			}
		}
	}
	return ""
}

// looksLikeJSON 简单判断字符串是否像 JSON。
func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	return (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"))
}
