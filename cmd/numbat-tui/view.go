package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *model) View() string {
	if m.currentMode == modeHelp {
		return m.renderHelp()
	}
	var b strings.Builder
	b.WriteString(m.renderStatusBar() + "\n")
	if m.currentMode == modePerm {
		b.WriteString(m.renderPermPrompt() + "\n")
	}
	b.WriteString(separatorStyle.Render(strings.Repeat("─", m.width)) + "\n")
	b.WriteString(m.viewport.View() + "\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", m.width)) + "\n")
	if m.slashVisible {
		b.WriteString(m.renderSlashPopup())
	}
	b.WriteString(inputBoxStyle.Render(m.input.View()) + "\n")
	return b.String()
}

func (m *model) renderPermPrompt() string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("⚠ Approve this tool?") + "\n")
	for i, c := range permChoices {
		line := fmt.Sprintf("  %s  %s", c.label, systemStyle.Render("("+c.key+")"))
		if i == m.permCursor {
			line = selectionStyle.Bold(true).Render("> " + line[2:])
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(systemStyle.Render("  ↑↓/jk navigate   enter/y/a/n/d select"))
	return permBoxStyle.Render(b.String())
}

func (m *model) renderStatusBar() string {
	session := ""
	if m.sessionID != "" {
		session = fmt.Sprintf("  [%s]", m.sessionID[:min(8, len(m.sessionID))])
	}
	indicator := lipgloss.NewStyle().Foreground(stateColor(m.state)).Render("●")
	stateText := lipgloss.NewStyle().Foreground(stateColor(m.state)).Bold(true).Render(string(m.state))
	content := headerStyle.Render("Numbat") +
		lipgloss.NewStyle().Foreground(colorMuted).Render("  "+m.addr+session) +
		"  " + indicator + " " + stateText
	// 运行中指示：LLM 思考阶段界面静止，靠它才能看出 run 仍在进行（也是 Esc 可中断的前提）
	if m.isRunning() {
		content += "  " + runningStyle.Render(m.spinner.View()+" running")
	}
	return statusBarStyle.Render(content)
}

// stateColor 返回连接状态对应的前景色。
func stateColor(s state) lipgloss.Color {
	switch s {
	case stateReady:
		return colorSuccess
	case stateDisconnected:
		return colorError
	default:
		return colorMuted
	}
}

func (m *model) renderSlashPopup() string {
	if len(m.slashFiltered) == 0 {
		return ""
	}
	maxShow := 8
	items := m.slashFiltered
	if len(items) > maxShow {
		items = items[:maxShow]
	}
	var b strings.Builder
	for i, item := range items {
		line := fmt.Sprintf("  /%s  %s", item.name, systemStyle.Render(item.desc))
		if i == m.slashCursor {
			line = selectionStyle.Bold(true).Render("> " + strings.TrimPrefix(line, "  "))
		}
		b.WriteString(line + "\n")
	}
	if len(m.slashFiltered) > maxShow {
		b.WriteString(systemStyle.Render(fmt.Sprintf("  ...(%d more)", len(m.slashFiltered)-maxShow)) + "\n")
	}
	b.WriteString(systemStyle.Render("↑↓/jk  tab/enter select  esc dismiss"))
	return slashPopupStyle.Render(b.String())
}

// buildContent 将所有消息渲染为完整文本，同时记录每个逻辑行的起始物理行号。
func (m *model) buildContent() (string, []int) {
	// 先更新未完成工具行的 spinner 当前帧
	if m.hasActiveTools() {
		sv := m.spinner.View()
		for _, tl := range m.toolBlocks {
			if !tl.finished {
				tl.spinnerView = sv
			}
		}
	}
	var b strings.Builder
	offsets := make([]int, len(m.lines))
	y := 0
	for i, l := range m.lines {
		offsets[i] = y
		selected := m.currentMode == modeBrowse && i == m.selectedLine
		rendered := l.View(m.width, selected)
		// 消息间加空行分隔，增强视觉呼吸感
		if i < len(m.lines)-1 {
			b.WriteString(rendered + "\n\n")
			y += strings.Count(rendered, "\n") + 2
		} else {
			b.WriteString(rendered + "\n")
			y += strings.Count(rendered, "\n") + 1
		}
	}
	return b.String(), offsets
}

// renderCtxBar 将上下文占用率渲染为彩色进度条。
// <70% 灰色、70%-85% 黄色、>=85% 红色。
func renderCtxBar(pct float64) string {
	const width = 20
	filled := int(pct * width)
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	label := fmt.Sprintf("ctx:%.1f%%", pct*100)
	var c lipgloss.Color
	switch {
	case pct >= 0.85:
		c = colorError
	case pct >= 0.70:
		c = colorAccent
	default:
		c = colorMuted
	}
	return lipgloss.NewStyle().Foreground(c).Render(label + " " + bar)
}

// renderHelp 渲染快捷键帮助页面。
func (m *model) renderHelp() string {
	title := headerStyle.Render(" Numbat — Keyboard Shortcuts")

	sections := []struct {
		name    string
		entries [][2]string
	}{
		{"Input Mode", [][2]string{
			{"Enter", "Send message"},
			{"Alt+Enter", "Insert newline"},
			{"↑ / ↓", "Browse input history"},
			{"/", "Slash command autocomplete"},
			{"Tab / Enter", "Select slash completion"},
			{"Esc", "Interrupt run, else switch to browse"},
			{"?", "Show this help"},
			{"Ctrl+C", "Quit"},
		}},
		{"Browse Mode", [][2]string{
			{"↑ / ↓ / k / j", "Move selection"},
			{"PgUp / PgDown", "Page scroll"},
			{"g / Home", "Go to top"},
			{"G / End", "Go to bottom"},
			{"Enter", "Toggle tool block expand/collapse"},
			{"[ / ]", "Scroll tool output up/down (1 line)"},
			{"{ / }", "Scroll tool output up/down (1 page)"},
			{"y", "Copy selected message to clipboard"},
			{"Esc", "Interrupt run, else back to input"},
		}},
		{"Permission Mode", [][2]string{
			{"↑ / ↓ / k / j", "Navigate choices"},
			{"y", "Allow once"},
			{"a", "Always allow"},
			{"n", "Deny once"},
			{"d", "Always deny"},
			{"Enter", "Confirm selection"},
			{"Esc", "Interrupt run"},
		}},
		{"Slash Commands", [][2]string{
			{"/new", "Start a new session"},
			{"/compact", "Compress context window"},
			{"/quit", "Quit"},
		}},
	}

	keyStyle := lipgloss.NewStyle().Foreground(colorAccent)
	var b strings.Builder
	b.WriteString(title + "\n\n")
	for _, s := range sections {
		b.WriteString(systemStyle.Bold(true).Render(" "+s.name+":") + "\n")
		for _, e := range s.entries {
			b.WriteString(keyStyle.Render(fmt.Sprintf("  %-18s", e[0])) + e[1] + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(systemStyle.Render(" Press Esc or q to close this help."))
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
