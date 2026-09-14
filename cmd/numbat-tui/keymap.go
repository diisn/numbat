package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handlePermKey 处理权限审批模式下的按键。
func (m *model) handlePermKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyUp:
		m.permCursor--
		if m.permCursor < 0 {
			m.permCursor = len(permChoices) - 1
		}
		return m, nil
	case tea.KeyDown:
		m.permCursor++
		if m.permCursor >= len(permChoices) {
			m.permCursor = 0
		}
		return m, nil
	case tea.KeyEnter:
		return m.pickPerm(permChoices[m.permCursor].decision)
	default:
		switch strings.ToLower(msg.String()) {
		case "k":
			m.permCursor--
			if m.permCursor < 0 {
				m.permCursor = len(permChoices) - 1
			}
		case "j":
			m.permCursor++
			if m.permCursor >= len(permChoices) {
				m.permCursor = 0
			}
		case "y":
			return m.pickPerm("allow_once")
		case "a":
			return m.pickPerm("always_allow")
		case "n":
			return m.pickPerm("deny_once")
		case "d":
			return m.pickPerm("always_deny")
		}
		return m, nil
	}
}

// handleSlashKey 处理 slash 补全弹出时的按键。返回 (是否已处理, model, cmd)
func (m *model) handleSlashKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	if !m.slashVisible || m.currentMode != modeInput {
		return false, m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.slashVisible = false
		return true, m, nil
	case tea.KeyUp, tea.KeyDown:
		delta := 1
		if msg.Type == tea.KeyUp {
			delta = -1
		}
		m.slashMove(delta)
		return true, m, nil
	case tea.KeyEnter:
		if msg.Alt {
			// alt+enter 换行：交予 textarea 处理
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.refreshSlash()
			return true, m, cmd
		}
		if len(m.slashFiltered) == 0 {
			return false, m, nil // 无匹配项，交还给 handleInput
		}
		model, cmd := m.slashSelect()
		return true, model, cmd
	case tea.KeyTab:
		if len(m.slashFiltered) == 0 {
			return true, m, nil
		}
		model, cmd := m.slashSelect()
		return true, model, cmd
	default:
		if s := msg.String(); s == "k" || s == "j" {
			delta := 1
			if s == "k" {
				delta = -1
			}
			m.slashMove(delta)
			return true, m, nil
		}
	}
	return false, m, nil
}

// slashMove 移动 slash 补全光标。
func (m *model) slashMove(delta int) {
	if len(m.slashFiltered) == 0 {
		return
	}
	m.slashCursor += delta
	if m.slashCursor < 0 {
		m.slashCursor = len(m.slashFiltered) - 1
	}
	if m.slashCursor >= len(m.slashFiltered) {
		m.slashCursor = 0
	}
}

// refreshSlash 在输入内容变化时更新 / 自动补全状态。
// 仅当输入以 "/" 开头且不含空格时弹出候选列表。
func (m *model) refreshSlash() {
	wasVisible := m.slashVisible
	wasCount := len(m.slashFiltered)
	if m.currentMode != modeInput {
		m.slashVisible = false
	} else {
		value := m.input.Value()
		if !strings.HasPrefix(value, "/") || strings.Contains(value, " ") {
			m.slashVisible = false
		} else {
			query := strings.ToLower(value[1:])
			filtered := make([]slashItem, 0, len(m.slashItems))
			for _, item := range m.slashItems {
				if strings.Contains(strings.ToLower(item.name), query) {
					filtered = append(filtered, item)
				}
			}
			m.slashFiltered = filtered
			m.slashCursor = 0
			m.slashVisible = true
		}
	}
	// 可见性或候选数变化时更新 viewport 高度
	if wasVisible != m.slashVisible || (m.slashVisible && wasCount != len(m.slashFiltered)) {
		m.updateViewportHeight()
	}
}
