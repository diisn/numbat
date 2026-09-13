package main

import "github.com/charmbracelet/lipgloss"

// ── 语义色板 ──────────────────────────────────────────
// 基础色
var (
	colorPrimary = lipgloss.Color("#7D56F4") // 品牌紫
	colorText    = lipgloss.Color("#FAFAFA") // 正文白
	colorMuted   = lipgloss.Color("#888888") // 次要灰
	colorSuccess = lipgloss.Color("#04B575") // 成功绿
	colorAccent  = lipgloss.Color("#F4D03F") // 强调金
	colorError   = lipgloss.Color("#FF6B6B") // 错误红
)

// 容器色
var (
	colorSurface    = lipgloss.Color("#1A1A2E") // 状态栏/容器背景
	colorSurfaceAlt = lipgloss.Color("#2A2A3E") // 工具块背景
	colorBorder     = lipgloss.Color("#3A3A4E") // 通用边框
	colorSelection  = lipgloss.Color("#333346") // 选中态背景（微紫调）
)

// 工具状态色（Gemini 启发：gutter 颜色 = 执行状态）
var (
	colorToolRunning = lipgloss.Color("#7D56F4") // 紫色 = 运行中
	colorToolDone    = lipgloss.Color("#04B575") // 绿色 = 完成
	colorToolFailed  = lipgloss.Color("#FF6B6B") // 红色 = 失败
)

// ── 文本样式 ──────────────────────────────────────────
var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	textStyle   = lipgloss.NewStyle().Foreground(colorText)
	systemStyle = lipgloss.NewStyle().Foreground(colorMuted)
	userStyle   = lipgloss.NewStyle().Foreground(colorSuccess)
	toolStyle   = lipgloss.NewStyle().Foreground(colorAccent)
	errorStyle  = lipgloss.NewStyle().Foreground(colorError)

	// agentLabelStyle / userLabelStyle 渲染消息标签行（"Agent:" / "You:"）：
	// 加粗 + 各自配色，与正文区分开，让说话方一眼可辨。
	agentLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(colorPrimary)
	userLabelStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorSuccess)

	// runningStyle 渲染状态栏的「运行中」指示。
	runningStyle = lipgloss.NewStyle().Foreground(colorAccent)
)

// ── 布局样式 ──────────────────────────────────────────
var (
	// 状态栏：深色背景 + 左右 padding
	statusBarStyle = lipgloss.NewStyle().
			Background(colorSurface).
			Padding(0, 1)

	// 分隔线：用 border 色，不再裸 ─
	separatorStyle = lipgloss.NewStyle().
			Foreground(colorBorder)

	// 输入框：圆角边框
	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	// 权限弹窗：圆角边框 + 品牌色边框
	permBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(1, 2)

	// 斜杠弹窗：圆角边框
	slashPopupStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	// 选中态
	selectionStyle = lipgloss.NewStyle().
			Background(colorSelection)
)

// toolStateColor 返回工具执行状态对应的颜色。
func toolStateColor(finished, isError bool) lipgloss.Color {
	switch {
	case !finished:
		return colorToolRunning
	case isError:
		return colorToolFailed
	default:
		return colorToolDone
	}
}
