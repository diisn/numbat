package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/youngyangyang04/numbat/internal/events"
	"github.com/youngyangyang04/numbat/internal/skills"
	"github.com/youngyangyang04/numbat/internal/tui"
)

// state 表示 TUI 与 numbat-core 的连接状态。
type state string

const (
	stateConnecting   state = "connecting"
	stateReady        state = "ready"
	stateDisconnected state = "disconnected"
	reconnectInterval       = 2 * time.Second
)

type eventMsg map[string]any

// 各种内部消息类型
type connectedMsg struct{}
type disconnectedMsg struct{}
type reconnectMsg struct{}
type errMsg error

// sessionCreatedMsg 表示会话创建成功。
// 首条消息可能发送失败，但会话已经建好，sessionID 必须照常记录；
// 失败原因随 sendErr 一并回传，避免静默丢错。
type sessionCreatedMsg struct {
	sessionID string
	sendErr   error
}

// rpcErrMsg 表示一次异步 RPC 调用失败，op 为操作名用于展示。
type rpcErrMsg struct {
	op  string
	err error
}

// permRespondedMsg 表示权限审批已发送。
type permRespondedMsg struct {
	decision string
}

// compactDoneMsg 表示手动压缩完成，带回压缩前后的 token 估算。
type compactDoneMsg struct {
	originalTokens int
	summaryTokens  int
}

// sessionCompactResult 是 session.compact 的返回结构。
type sessionCompactResult struct {
	OriginalTokens int `json:"original_tokens"`
	SummaryTokens  int `json:"summary_tokens"`
}

// sendDoneMsg 表示一次 session.send_message 已返回，用于清除「等待 run」门控。
// 不产生任何可见输出——发送过程的反馈由事件流承担。
type sendDoneMsg struct{}

// runAbortedMsg 表示一次 agent.abort 调用已返回，ok 为后端是否成功触发取消。
type runAbortedMsg struct {
	runID string
	ok    bool
}

// mode 表示 TUI 当前交互模式，替代零散的布尔标志。
type mode int

const (
	modeInput  mode = iota // 输入模式（含 slash 补全子状态）
	modeBrowse             // 浏览消息
	modePerm               // 权限审批
	modeHelp               // 帮助页面
)

type permChoice struct {
	decision string
	label    string
	key      string
}

var permChoices = []permChoice{
	{"allow_once", "Allow once", "y"},
	{"always_allow", "Always allow", "a"},
	{"deny_once", "Deny", "n"},
	{"always_deny", "Always deny", "d"},
}

// slashItem 是 / 自动补全列表中的一条命令（内建命令或 skill）。
type slashItem struct {
	name string
	desc string
}

type model struct {
	addr            string
	client          *tui.Client
	sessionID       string
	lines           []line
	lineYOffsets    []int // 每个逻辑行在 viewport 内容中的起始物理行号
	input           textarea.Model
	viewport        viewport.Model
	spinner         spinner.Model
	followingBottom bool                   // 新增消息时是否自动滚动到底部
	pending         map[string]string      // toolUseID -> toolName
	toolBlocks      map[string]*toolLine   // toolUseID -> tool line
	streams         map[string]*streamLine // runID -> 流式输出状态
	state           state
	currentMode     mode
	selectedLine    int
	permToolUseID   string
	permCursor      int
	slashItems      []slashItem
	slashFiltered   []slashItem
	slashCursor     int
	slashVisible    bool
	history         []string // 历史输入记录
	historyIdx      int      // 当前浏览的历史位置（-1 表示不在浏览历史状态）
	width           int
	height          int
	currentRunID    string          // 当前活动主 run，agent.abort 的目标
	awaitingRun     bool            // 本 TUI 有 send_message 在等，用于门控（避免误认其他客户端的 run）
	subagentRuns    map[string]bool // 已知的子代理 run id，用于把嵌套 run 排除在主 run 之外
}

func initialModel(addr string) *model {
	m := &model{
		addr:            addr,
		client:          tui.NewClient(addr),
		pending:         make(map[string]string),
		toolBlocks:      make(map[string]*toolLine),
		streams:         make(map[string]*streamLine),
		subagentRuns:    make(map[string]bool),
		state:           stateConnecting,
		currentMode:     modeInput,
		followingBottom: true,
		selectedLine:    -1,
		historyIdx:      -1,
		slashItems:      buildSlashItems(),
	}
	m.input = textarea.New()
	m.input.Placeholder = "Type a message or command... (alt+enter for newline)"
	m.input.Prompt = ""
	m.input.ShowLineNumbers = false
	m.input.SetHeight(3)
	// Enter 由主循环负责提交，因此仅保留 alt+enter 作为显式换行键。
	m.input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "newline"))
	m.input.Focus()
	m.viewport = viewport.New(80, 20)
	m.viewport.MouseWheelEnabled = true
	m.spinner = spinner.New(spinner.WithSpinner(spinner.Line), spinner.WithStyle(toolStyle))
	return m
}

// buildSlashItems 构建 / 自动补全候选：内建命令 + 所有可用 skill。
func buildSlashItems() []slashItem {
	items := []slashItem{
		{"new", "start a new session"},
		{"compact", "compress context window"},
		{"quit", "quit"},
	}
	loader := skills.NewLoader()
	for _, s := range loader.ListAllSkills() {
		desc := s.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		items = append(items, slashItem{name: s.Name, desc: desc})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	return items
}

func (m *model) Init() tea.Cmd {
	m.state = stateConnecting
	return m.connectCmd()
}

func (m *model) connectCmd() tea.Cmd {
	return func() tea.Msg {
		if err := m.client.Connect(); err != nil {
			return errMsg(err)
		}
		if err := m.client.Subscribe(); err != nil {
			return errMsg(err)
		}
		return connectedMsg{}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		// 鼠标滚轮滚动 viewport；未达底部时暂停自动跟随，滚回底部则恢复。
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		m.followingBottom = m.viewport.AtBottom()
		return m, vpCmd
	case tea.KeyMsg:
		// 帮助模式：Esc/q 关闭
		if m.currentMode == modeHelp {
			if msg.String() == "q" || msg.Type == tea.KeyEsc {
				m.currentMode = modeInput
				m.input.Focus()
				return m, nil
			}
			return m, nil // 帮助模式下忽略其他按键
		}
		// / 补全弹出时的按键处理（此时 Esc 只关弹窗，不中断 run：浮层优先）
		if handled, model, cmd := m.handleSlashKey(msg); handled {
			return model, cmd
		}
		// 有活动 run 时 Esc 中断，优先于权限弹窗与模式切换
		if msg.Type == tea.KeyEsc && m.isRunning() {
			// 权限弹窗中中断：后端会立即结束等待，弹窗必须一并收起，
			// 否则 run 已终止而界面仍停在对话框上（输入框失焦，看起来像卡死）
			if m.currentMode == modePerm {
				m.exitPermMode()
			}
			return m, m.abortRunCmd(m.currentRunID)
		}
		if m.currentMode == modePerm {
			return m.handlePermKey(msg)
		}
		// ? 键打开帮助（输入模式和浏览模式下均可）
		if msg.String() == "?" {
			m.currentMode = modeHelp
			m.input.Blur()
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			if m.client != nil {
				_ = m.client.Close()
			}
			return m, tea.Quit
		case tea.KeyEsc:
			if m.currentMode != modeInput {
				// 回到输入模式：恢复自动跟随底部并滚到底
				m.currentMode = modeInput
				m.input.Focus()
				m.followingBottom = true
				m.viewport.GotoBottom()
				m.syncViewport()
				return m, nil
			}
			// 进入浏览模式：选中最后一行
			m.currentMode = modeBrowse
			m.input.Blur()
			if len(m.lines) > 0 {
				m.selectedLine = len(m.lines) - 1
			}
			m.syncViewport()
			return m, nil
		case tea.KeyEnter:
			if m.currentMode != modeInput {
				return m.toggleSelectedTool()
			}
			if msg.Alt {
				// alt+enter 换行：交予 textarea 处理
				break
			}
			return m.handleInput()
		case tea.KeyUp:
			if m.currentMode != modeInput {
				m.moveSelection(-1)
				m.ensureSelectionVisible()
				return m, nil
			}
			// 输入模式下：上箭头浏览历史（slash 弹出时交给 textarea 处理）
			if !m.slashVisible && len(m.history) > 0 {
				m.navigateHistory(-1)
				return m, nil
			}
		case tea.KeyDown:
			if m.currentMode != modeInput {
				m.moveSelection(1)
				m.ensureSelectionVisible()
				return m, nil
			}
			// 输入模式下：下箭头浏览历史
			if !m.slashVisible && len(m.history) > 0 {
				m.navigateHistory(1)
				return m, nil
			}
		case tea.KeyPgUp:
			if m.currentMode != modeInput {
				m.viewport.PageUp()
				m.followingBottom = false
				return m, nil
			}
		case tea.KeyPgDown:
			if m.currentMode != modeInput {
				m.viewport.PageDown()
				m.followingBottom = m.viewport.AtBottom()
				return m, nil
			}
		case tea.KeyHome:
			if m.currentMode != modeInput {
				m.selectedLine = 0
				m.viewport.GotoTop()
				m.followingBottom = false
				m.syncViewport()
				return m, nil
			}
		case tea.KeyEnd:
			if m.currentMode != modeInput {
				m.selectedLine = len(m.lines) - 1
				m.viewport.GotoBottom()
				m.followingBottom = true
				m.syncViewport()
				return m, nil
			}
		default:
			if m.currentMode != modeInput {
				switch msg.String() {
				case "k":
					m.moveSelection(-1)
					m.ensureSelectionVisible()
					return m, nil
				case "j":
					m.moveSelection(1)
					m.ensureSelectionVisible()
					return m, nil
				case "g":
					m.selectedLine = 0
					m.viewport.GotoTop()
					m.followingBottom = false
					m.syncViewport()
					return m, nil
				case "G":
					m.selectedLine = len(m.lines) - 1
					m.viewport.GotoBottom()
					m.followingBottom = true
					m.syncViewport()
					return m, nil
				case "y":
					return m.copySelected()
				case "[":
					m.scrollToolOutput(-1)
					return m, nil
				case "]":
					m.scrollToolOutput(1)
					return m, nil
				case "{":
					m.scrollToolOutput(-maxOutputLines)
					return m, nil
				case "}":
					m.scrollToolOutput(maxOutputLines)
					return m, nil
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(msg.Width - 6) // 输入框圆角边框(2) + padding(2) + 内边距(2)
		m.viewport.Width = msg.Width
		m.updateViewportHeight()
		// 尺寸变化后重新构建内容
		m.syncViewport()

	case connectedMsg:
		m.state = stateReady
		m.addMessage(systemStyle.Render("Connected to numbat-core"))
		return m, m.eventListener()

	case errMsg:
		if m.state == stateConnecting {
			m.state = stateDisconnected
			m.addMessage(errorStyle.Render("Connection failed: " + msg.Error()))
			return m, m.scheduleReconnect()
		}
		m.addMessage(errorStyle.Render("Error: " + msg.Error()))

	case disconnectedMsg:
		if m.state == stateDisconnected {
			return m, nil
		}
		m.state = stateDisconnected
		m.sessionID = ""
		m.pending = make(map[string]string)
		// 运行状态一并清空，避免重连后残留「幽灵 running」或向失效的 run_id 发起中止
		m.currentRunID = ""
		m.awaitingRun = false
		m.subagentRuns = make(map[string]bool)
		m.addMessage(errorStyle.Render("Disconnected from numbat-core"))
		if m.client != nil {
			_ = m.client.Close()
		}
		m.client = tui.NewClient(m.addr)
		return m, m.scheduleReconnect()

	case reconnectMsg:
		m.state = stateConnecting
		m.addMessage(systemStyle.Render("Reconnecting..."))
		return m, m.connectCmd()

	case sessionCreatedMsg:
		m.sessionID = msg.sessionID
		m.awaitingRun = false
		m.addMessage(systemStyle.Render("Session: " + msg.sessionID))
		if msg.sendErr != nil {
			m.addMessage(errorStyle.Render("send first message failed: " + msg.sendErr.Error()))
		}

	case sendDoneMsg:
		// send_message 已返回（run 已收尾），解除等待门控
		m.awaitingRun = false

	case runAbortedMsg:
		if msg.ok {
			m.addMessage(systemStyle.Render(fmt.Sprintf("Aborting run %s...", shortRunID(msg.runID))))
		} else {
			m.addMessage(systemStyle.Render("Run already finished"))
		}

	case rpcErrMsg:
		// 发送失败时同样要解除门控，否则后续会把其他客户端的 run 误认成本 TUI 的
		m.awaitingRun = false
		m.addMessage(errorStyle.Render(msg.op + " failed: " + msg.err.Error()))

	case permRespondedMsg:
		m.addMessage(systemStyle.Render("Permission " + msg.decision))

	case compactDoneMsg:
		m.addMessage(systemStyle.Render(fmt.Sprintf("Context compacted: %d -> %d tokens",
			msg.originalTokens, msg.summaryTokens)))

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		// 有活跃工具或活动 run 时继续 tick（后者让 LLM 思考阶段也能看到运行指示）
		if !m.hasActiveTools() && !m.isRunning() {
			return m, nil
		}
		if m.hasActiveTools() {
			// 工具 spinner 渲染在 viewport 内容里，需要重建
			m.syncViewport()
		}
		return m, cmd

	case eventMsg:
		if m.state != stateReady {
			m.state = stateReady
		}
		m.handleEvent(msg)
		m.syncViewport()
		// 有活跃工具或活动 run 时启动 spinner
		if m.hasActiveTools() || m.isRunning() {
			return m, tea.Batch(m.eventListener(), spinner.Tick)
		}
		return m, m.eventListener()
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.refreshSlash()
	return m, cmd
}

// ---------- 输入处理 ----------

// navigateHistory 在输入历史中上下浏览。方向：-1 向上（更早），+1 向下（更新）。
func (m *model) navigateHistory(dir int) {
	if len(m.history) == 0 {
		return
	}
	if m.historyIdx == -1 {
		// 首次进入历史浏览：从最后一条开始
		if dir > 0 {
			return // 已经在最新，无需向下
		}
		m.historyIdx = len(m.history) - 1
	} else {
		m.historyIdx += dir
		if m.historyIdx < 0 {
			m.historyIdx = 0
		}
		if m.historyIdx >= len(m.history) {
			// 超出范围：回到空输入
			m.historyIdx = -1
			m.input.SetValue("")
			return
		}
	}
	m.input.SetValue(m.history[m.historyIdx])
	// 光标移到末尾
	m.input.SetCursor(len(m.input.Value()))
}

func (m *model) handleInput() (tea.Model, tea.Cmd) {
	value := m.input.Value()
	m.input.SetValue("")
	m.slashVisible = false
	if strings.TrimSpace(value) == "" {
		return m, nil
	}

	// 存入历史（去重：与最后一条相同则不追加）
	if len(m.history) == 0 || m.history[len(m.history)-1] != value {
		m.history = append(m.history, value)
	}
	m.historyIdx = -1 // 发送后重置历史浏览位置

	if strings.HasPrefix(value, "/") {
		return m.handleCommand(value)
	}

	m.addUserMessage(value)
	// 置等待门控：run.started 到达时据此判断该 run 由本 TUI 触发（事件无 session_id 可过滤）
	m.awaitingRun = true
	if m.sessionID == "" {
		return m, m.createSessionAndSendCmd(value)
	}
	return m, m.sendMessageCmd(m.sessionID, value)
}

func (m *model) handleCommand(cmd string) (tea.Model, tea.Cmd) {
	parts := strings.SplitN(cmd, " ", 2)
	switch parts[0] {
	case "/quit":
		return m, tea.Quit
	case "/new":
		m.addMessage(systemStyle.Render("Creating new session..."))
		return m, m.createSessionCmd()
	case "/compact":
		if m.sessionID == "" {
			m.addMessage(systemStyle.Render("No active session"))
			return m, nil
		}
		m.addMessage(systemStyle.Render("Compacting session..."))
		return m, m.compactSessionCmd(m.sessionID)
	default:
		// "/name args" 原样发给 core，由 core 端解析并触发对应 skill
		// （与 Python 版一致：skill 命中时模板渲染结果作为 system prompt，未命中时按普通消息处理）。
		m.addUserMessage(cmd)
		// 同 handleInput：/skill 命令同样会触发 run
		m.awaitingRun = true
		if m.sessionID == "" {
			return m, m.createSessionAndSendCmd(cmd)
		}
		return m, m.sendMessageCmd(m.sessionID, cmd)
	}
}

func (m *model) slashSelect() (tea.Model, tea.Cmd) {
	if m.slashCursor < 0 || m.slashCursor >= len(m.slashFiltered) {
		return m, nil
	}
	item := m.slashFiltered[m.slashCursor]
	full := "/" + item.name + " "
	m.input.SetValue(full)
	m.input.SetCursor(len(full))
	m.slashVisible = false
	return m, nil
}

// ---------- 异步 RPC 命令（都返回 tea.Cmd，结果通过 Msg 回传） ----------

// createSessionAndSendCmd 异步创建会话并发送首条消息。
func (m *model) createSessionAndSendCmd(content string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.client.Call("session.create", map[string]string{"mode": "chat", "title": "tui"})
		if err != nil {
			return rpcErrMsg{op: "create session", err: err}
		}
		sid, ok := sessionIDFromResult(result)
		if !ok {
			return rpcErrMsg{op: "create session", err: fmt.Errorf("unexpected session.create result: %v", result)}
		}
		// 发送首条消息：失败也要随会话创建结果一并回传（会话已建好，不能丢 sessionID）
		_, sendErr := m.client.Call("session.send_message", map[string]string{"session_id": sid, "content": content})
		return sessionCreatedMsg{sessionID: sid, sendErr: sendErr}
	}
}

// createSessionCmd 异步创建会话。
func (m *model) createSessionCmd() tea.Cmd {
	return func() tea.Msg {
		result, err := m.client.Call("session.create", map[string]string{"mode": "chat", "title": "tui"})
		if err != nil {
			return rpcErrMsg{op: "create session", err: err}
		}
		sid, ok := sessionIDFromResult(result)
		if !ok {
			return rpcErrMsg{op: "create session", err: fmt.Errorf("unexpected session.create result: %v", result)}
		}
		return sessionCreatedMsg{sessionID: sid}
	}
}

// sessionIDFromResult 从 session.create 的返回中取出会话 ID。
func sessionIDFromResult(result any) (string, bool) {
	m, ok := result.(map[string]any)
	if !ok {
		return "", false
	}
	sid, ok := m["id"].(string)
	return sid, ok && sid != ""
}

// sendMessageCmd 异步发送消息。该调用阻塞至 run 结束，返回时解除等待门控。
func (m *model) sendMessageCmd(sid, content string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.client.Call("session.send_message", map[string]string{"session_id": sid, "content": content})
		if err != nil {
			return rpcErrMsg{op: "send", err: err}
		}
		return sendDoneMsg{}
	}
}

// abortRunCmd 异步请求中止指定 run。ok 为 false 表示该 run 已结束（不在取消注册表中）。
func (m *model) abortRunCmd(runID string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.client.Call("agent.abort", map[string]string{"run_id": runID})
		if err != nil {
			return rpcErrMsg{op: "abort", err: err}
		}
		ok, _ := result.(bool)
		return runAbortedMsg{runID: runID, ok: ok}
	}
}

// compactSessionCmd 异步压缩会话。成功后回传压缩前后的 token 估算，
// 否则「Compacting session...」之后没有任何收尾提示，看起来像一直卡着。
func (m *model) compactSessionCmd(sid string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.client.Call("session.compact", map[string]string{"session_id": sid})
		if err != nil {
			return rpcErrMsg{op: "compact", err: err}
		}
		var res sessionCompactResult
		if raw, ok := result.(map[string]any); ok {
			mapToStructChecked(raw, &res, "session.compact")
		}
		return compactDoneMsg{originalTokens: res.OriginalTokens, summaryTokens: res.SummaryTokens}
	}
}

// respondPermissionCmd 异步回复权限审批。
func (m *model) respondPermissionCmd(toolUseID, decision string) tea.Cmd {
	return func() tea.Msg {
		_, err := m.client.Call("permission.respond", map[string]string{"tool_use_id": toolUseID, "decision": decision})
		if err != nil {
			return rpcErrMsg{op: "permission respond", err: err}
		}
		return permRespondedMsg{decision: decision}
	}
}

func (m *model) pickPerm(decision string) (tea.Model, tea.Cmd) {
	if m.permToolUseID == "" {
		return m, nil
	}
	toolUseID := m.permToolUseID
	m.exitPermMode()
	return m, m.respondPermissionCmd(toolUseID, decision)
}

// exitPermMode 收起权限弹窗并回到输入模式。
// 审批作答与「中断时顺手收起弹窗」共用，避免两处清理逻辑走样。
func (m *model) exitPermMode() {
	delete(m.pending, m.permToolUseID)
	m.permToolUseID = ""
	m.permCursor = 0
	m.currentMode = modeInput
	m.input.Focus()
	m.updateViewportHeight()
}

// ---------- 事件处理 ----------

// hasActiveTools 检查是否有工具正在执行（未完成）。
func (m *model) hasActiveTools() bool {
	for _, tl := range m.toolBlocks {
		if !tl.finished {
			return true
		}
	}
	return false
}

// isRunning 返回当前是否有本 TUI 触发的主 run 在执行。
func (m *model) isRunning() bool {
	return m.currentRunID != ""
}

// mapToStructChecked 将 map 解析到结构体，失败时打日志。
func mapToStructChecked(m map[string]any, v any, eventType string) {
	if err := mapToStruct(m, v); err != nil {
		slog.Warn("failed to parse event payload", "type", eventType, "err", err)
	}
}

func (m *model) handleEvent(payload map[string]any) {
	eventType, _ := payload["type"].(string)
	switch eventType {
	case "llm.response":
		var ev events.LLMResponse
		mapToStructChecked(payload, &ev, eventType)
		if st, ok := m.streams[ev.RunID]; ok {
			delete(m.streams, ev.RunID)
			// token 已逐字显示；用权威完整文本兜底替换（如流中途重试导致的不完整段）。
			// 存原始 Markdown 而非渲染结果：渲染推迟到 View，终端宽度变化后仍可重排。
			if ev.Text != "" {
				tl := m.lines[st.idx].(*textLine)
				tl.text = ""
				tl.markdown = ev.Text
			}
			return
		}
		if ev.Text != "" {
			m.addMarkdownMessage(ev.Text)
		}
	case "llm.token":
		var ev events.LLMTokens
		mapToStructChecked(payload, &ev, eventType)
		st, ok := m.streams[ev.RunID]
		if !ok {
			st = &streamLine{idx: len(m.lines)}
			m.streams[ev.RunID] = st
			m.lines = append(m.lines, &textLine{label: agentLabel, labelStyle: agentLabelStyle})
		}
		st.text += ev.Token
		m.lines[st.idx].(*textLine).text = textStyle.Render(st.text)
	case "llm.usage":
		var ev events.LLMUsage
		mapToStructChecked(payload, &ev, eventType)
		m.addMessage(systemStyle.Render(fmt.Sprintf("usage: in %d / out %d tokens %s",
			ev.InputTokens, ev.OutputTokens, renderCtxBar(ev.ContextPct))))
	case "tool.call_started":
		var ev events.ToolCallStarted
		mapToStructChecked(payload, &ev, eventType)
		tl := &toolLine{
			toolUseID: ev.ToolUseID,
			toolName:  ev.ToolName,
			params:    ev.Params,
		}
		m.toolBlocks[ev.ToolUseID] = tl
		m.addLine(tl)
	case "tool.call_finished":
		var ev events.ToolCallFinished
		mapToStructChecked(payload, &ev, eventType)
		if tl, ok := m.toolBlocks[ev.ToolUseID]; ok {
			tl.output = ev.Output
			tl.elapsed = ev.ElapsedMs
			tl.finished = true
		}
	case "tool.call_failed":
		var ev events.ToolCallFailed
		mapToStructChecked(payload, &ev, eventType)
		if tl, ok := m.toolBlocks[ev.ToolUseID]; ok {
			tl.output = ev.Error
			tl.elapsed = ev.ElapsedMs
			tl.isError = true
			tl.finished = true
		}
	case "permission.requested":
		var ev events.PermissionRequested
		mapToStructChecked(payload, &ev, eventType)
		m.pending[ev.ToolUseID] = ev.ToolName
		m.addMessage(systemStyle.Render(fmt.Sprintf("Permission requested for %s [%s]", ev.ToolName, ev.Preview)))
		m.currentMode = modePerm
		m.permToolUseID = ev.ToolUseID
		m.permCursor = 0
		m.input.Blur()
		m.updateViewportHeight()
	case "permission.granted":
		var ev events.PermissionGranted
		mapToStructChecked(payload, &ev, eventType)
		delete(m.pending, ev.ToolUseID)
	case "permission.denied":
		var ev events.PermissionDenied
		mapToStructChecked(payload, &ev, eventType)
		delete(m.pending, ev.ToolUseID)
	case "run.started":
		var ev events.RunStarted
		mapToStructChecked(payload, &ev, eventType)
		// 只认本 TUI 触发的 run（awaitingRun 门控），并排除子代理 run——
		// 嵌套子代理也会发 run.started，不排除会覆盖主 run 的 id 导致中止打偏。
		// 子代理先发 subagent.started 后发 run.started，因此此处集合已登记。
		if m.awaitingRun && !m.subagentRuns[ev.RunID] {
			m.currentRunID = ev.RunID
		}
		m.addMessage(systemStyle.Render("Run started: " + ev.RunID))
	case "run.finished":
		var ev events.RunFinished
		mapToStructChecked(payload, &ev, eventType)
		if ev.RunID == m.currentRunID {
			m.currentRunID = ""
		}
		// 带上 Reason，否则被中止的 run 只显示 failed，与真实失败无法区分
		status := ev.Status
		if ev.Reason != "" {
			status += " (" + ev.Reason + ")"
		}
		m.addMessage(systemStyle.Render("Run finished: " + status))
	case "context.compacted":
		var ev events.ContextCompacted
		mapToStructChecked(payload, &ev, eventType)
		m.addMessage(systemStyle.Render(fmt.Sprintf("Context compacted: %d -> %d tokens", ev.OriginalTokens, ev.SummaryTokens)))
	case "subagent.started":
		var ev events.SubagentStarted
		mapToStructChecked(payload, &ev, eventType)
		m.subagentRuns[ev.RunID] = true
		m.addMessage(systemStyle.Render(fmt.Sprintf("Subagent [%s]: %s", ev.RunID, ev.Description)))
	case "subagent.finished":
		var ev events.SubagentFinished
		mapToStructChecked(payload, &ev, eventType)
		m.addMessage(systemStyle.Render(fmt.Sprintf("Subagent [%s] finished: %s", ev.RunID, ev.Status)))
	case "skill.invoked":
		var ev events.SkillInvoked
		mapToStructChecked(payload, &ev, eventType)
		m.addMessage(systemStyle.Render(fmt.Sprintf("Skill invoked: %s %s", ev.SkillName, ev.Arguments)))
	}
}

func (m *model) eventListener() tea.Cmd {
	return func() tea.Msg {
		env, ok := <-m.client.Events()
		if !ok {
			return disconnectedMsg{}
		}
		// 负载形态异常时返回空事件：handleEvent 会忽略，监听照常重新挂起。
		payload, _ := env.Result.(map[string]any)
		return eventMsg(payload)
	}
}

func (m *model) scheduleReconnect() tea.Cmd {
	return tea.Tick(reconnectInterval, func(time.Time) tea.Msg {
		return reconnectMsg{}
	})
}

// ---------- 消息行管理 ----------

func (m *model) addLine(l line) {
	m.lines = append(m.lines, l)
	m.syncViewport()
}

func (m *model) addMessage(text string) {
	m.addLine(&textLine{text: text})
}

// addMarkdownMessage 追加一条助手消息，保留原始 Markdown 并带 "Agent:" 标签；
// 渲染延迟到 View，终端宽度变化时可按新宽度重排。
func (m *model) addMarkdownMessage(markdown string) {
	m.addLine(&textLine{label: agentLabel, labelStyle: agentLabelStyle, markdown: markdown})
}

// addUserMessage 追加一条用户消息，带 "You:" 标签行（与助手消息格式统一）。
// 标签与正文同为绿色：标签加粗以区分层级。
func (m *model) addUserMessage(text string) {
	m.addLine(&textLine{label: userLabel, labelStyle: userLabelStyle, text: userStyle.Render(text)})
}

func (m *model) moveSelection(delta int) {
	if len(m.lines) == 0 {
		m.selectedLine = -1
		return
	}
	m.selectedLine += delta
	if m.selectedLine < 0 {
		m.selectedLine = 0
	}
	if m.selectedLine >= len(m.lines) {
		m.selectedLine = len(m.lines) - 1
	}
}

// scrollViewport 滚动视口 n 行（正向下，负向上），并同步 followingBottom 状态。
func (m *model) scrollViewport(n int) {
	if n > 0 {
		m.viewport.LineDown(n)
		m.followingBottom = m.viewport.AtBottom()
	} else {
		m.viewport.LineUp(-n)
		m.followingBottom = false
	}
}

// ensureSelectionVisible 确保选中行在 viewport 可见范围内。
func (m *model) ensureSelectionVisible() {
	if m.selectedLine < 0 || m.selectedLine >= len(m.lineYOffsets) {
		return
	}
	y := m.lineYOffsets[m.selectedLine]
	vpTop := m.viewport.YOffset
	vpBottom := vpTop + m.viewport.Height - 1
	if y < vpTop {
		m.viewport.SetYOffset(y)
		m.followingBottom = false
	} else if y > vpBottom {
		// 滚动到选中行在视口底部
		m.viewport.SetYOffset(y - m.viewport.Height + 1)
		m.followingBottom = m.viewport.AtBottom()
	}
	// 选中行变化后需要重渲染高亮
	m.syncViewport()
}

// updateViewportHeight 根据当前 UI 状态更新 viewport 高度。
func (m *model) updateViewportHeight() {
	vpHeight := m.height - 9 // 状态栏 + 分隔线×2 + 输入框(含边框)
	if m.currentMode == modePerm {
		vpHeight -= 10 // 权限弹窗(含边框+padding)
	}
	if m.slashVisible {
		slashLines := min(len(m.slashFiltered), 8) + 3 // items + hint + border
		if len(m.slashFiltered) > 8 {
			slashLines++ // "more" 提示行
		}
		vpHeight -= slashLines
	}
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.viewport.Height = vpHeight
}

// syncViewport 重新构建 viewport 内容；如果 followingBottom 为 true 则滚动到底部。
func (m *model) syncViewport() {
	content, offsets := m.buildContent()
	m.lineYOffsets = offsets
	m.viewport.SetContent(content)
	if m.followingBottom {
		m.viewport.GotoBottom()
	}
}

func (m *model) toggleSelectedTool() (tea.Model, tea.Cmd) {
	if m.selectedLine < 0 || m.selectedLine >= len(m.lines) {
		return m, nil
	}
	if tl, ok := m.lines[m.selectedLine].(*toolLine); ok {
		tl.expanded = !tl.expanded
		m.syncViewport()
	}
	return m, nil
}

// copySelected 将选中行的纯文本复制到剪贴板。
func (m *model) copySelected() (tea.Model, tea.Cmd) {
	if m.selectedLine < 0 || m.selectedLine >= len(m.lines) {
		return m, nil
	}
	text := m.lines[m.selectedLine].PlainText()
	if err := clipboard.WriteAll(text); err != nil {
		m.addMessage(errorStyle.Render("Copy failed: " + err.Error()))
	} else {
		m.addMessage(systemStyle.Render("Copied to clipboard"))
	}
	return m, nil
}

// scrollToolOutput 滚动选中工具块的 output 区域（仅对展开的 toolLine 有效）。
func (m *model) scrollToolOutput(delta int) {
	if m.selectedLine < 0 || m.selectedLine >= len(m.lines) {
		return
	}
	tl, ok := m.lines[m.selectedLine].(*toolLine)
	if !ok || !tl.expanded || !tl.finished {
		return
	}
	tl.scrollOffset += delta
	if tl.scrollOffset < 0 {
		tl.scrollOffset = 0
	}
	// 上限在 View 里做 clamp（因为 totalLines 在渲染时才知道）
	m.syncViewport()
}

// ---------- 工具函数 ----------

func mapToStruct(m map[string]any, v any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// shortRunID 截断 run id 供展示使用（完整 id 过长，取前 8 位已足够区分）。
func shortRunID(runID string) string {
	if len(runID) <= 8 {
		return runID
	}
	return runID[:8]
}
