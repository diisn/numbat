import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ContentBlock, Message, PermissionDecision, RunSummary, Session, SkillMeta } from '@/types/api'
import { createClient, USE_MOCK, type Client, type ConnectionStatus } from '@/lib/client'
import { MOCK_SKILLS } from '@/lib/mock-client'
import { ToolCallCard, type ToolCallState } from '@/components/ToolCallCard'
import { PermissionDialog } from '@/components/PermissionDialog'
import { UsageBadge, SkillBadge, CompactionBadge } from '@/components/Badges'
import { Sidebar } from '@/components/Sidebar'
import { NavRail, type PageView } from '@/components/NavRail'
import { RunHistoryPage } from '@/components/RunHistoryPage'
import { MarkdownRenderer } from '@/components/MarkdownRenderer'
import { SettingsPage } from '@/components/SettingsPage'
import { WorkbenchPage } from '@/components/WorkbenchPage'
import { SubagentTree } from '@/components/SubagentTree'
import { SlashAutocomplete, type SlashItem } from '@/components/SlashAutocomplete'
import { HelpModal } from '@/components/HelpModal'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { applyTheme, loadThemePreference } from '@/lib/theme'

const STATUS_LABEL: Record<ConnectionStatus, string> = {
  connecting: '连接中…',
  connected: '已连接',
  reconnecting: '重连中…',
  disconnected: '已断开',
}

// 时间戳 → HH:MM（本地时间）
function formatTime(ts: number): string {
  const d = new Date(ts)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

// 模块级单例 client（Mock 或真实 WsClient）
const client: Client = createClient()

interface ChatMessage {
  role: 'user' | 'assistant'
  content: ContentBlock[]
  runId?: string
  /** 消息创建时间戳（毫秒），用于显示 HH:MM */
  ts?: number
  usage?: { input_tokens: number; output_tokens: number; context_pct: number }
  skillName?: string
  subagents?: { runId: string; description: string; status: string }[]
  compacted?: { original_tokens: number; summary_tokens: number }
  queued?: boolean
  /** run 被用户中止 */
  aborted?: boolean
}

interface PendingPermission {
  tool_use_id: string
  tool_name: string
  params: Record<string, unknown>
  preview: string
  run_id: string
}

// 历史加载没有 tool.call_* 事件流，从 tool_result 块反推工具调用终态，
// 否则 ToolCallCard 会把已完成的调用永远显示为「执行中…」
function deriveToolStates(history: Message[]): Record<string, ToolCallState> {
  const states: Record<string, ToolCallState> = {}
  for (const m of history) {
    for (const block of m.content) {
      if (block.type === 'tool_result' && block.tool_use_id) {
        states[block.tool_use_id] = block.is_error
          ? { status: 'failed', error: block.content ?? '' }
          : { status: 'success', output: block.content ?? '' }
      }
    }
  }
  return states
}

// 虚拟列表（PRD §5）：消息数超过阈值时，DOM 中只保留最近 WINDOW 条，
// 更早的消息折叠为一条可展开的提示，避免长对话下 DOM 无限膨胀。
const VIRTUAL_THRESHOLD = 500


const SCENARIOS = [
  '「你好」→ 纯文本对话',
  '「读取文件」→ read_file 工具调用',
  '「运行命令」→ bash + 权限审批',
  '「写入文件」→ write_file + 权限审批',
  '「分析架构」→ 子 Agent',
  '「重构代码」→ 多步 ReAct 循环',
  '「后台分析」→ 后台子 Agent + 轮询',
  '「任务计划」→ Task 创建',
  '「错误测试」→ 工具失败',
  '/orchestrate → 三阶段 multi-agent',
  '/review → Skill 代码审查',
  '/init → Skill 项目初始化',
  '/summarize → Skill 对话摘要',
]

// 内建斜杠命令：前端处理，不发送后端（对齐 TUI handleCommand）。
const BUILTIN_COMMANDS: { name: string; desc: string }[] = [
  { name: 'new', desc: '新建会话' },
  { name: 'compact', desc: '压缩上下文' },
  { name: 'clear', desc: '清空当前会话' },
]

function App() {
  const [status, setStatus] = useState<ConnectionStatus>('disconnected')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [toolStates, setToolStates] = useState<Record<string, ToolCallState>>({})
  const [pendingPermissions, setPendingPermissions] = useState<PendingPermission[]>([])
  const [input, setInput] = useState('')
  const [responding, setResponding] = useState(false)
  const [messageQueue, setMessageQueue] = useState<string[]>([])
  const [showBackToBottom, setShowBackToBottom] = useState(false)
  // 虚拟列表：可见窗口大小（用户可点击展开更早的消息）
  const [windowSize, setWindowSize] = useState(VIRTUAL_THRESHOLD)
  // 用户向上滚动时冻结的窗口起点；null 表示跟随尾部
  const [pinnedStart, setPinnedStart] = useState<number | null>(null)
  const [debugMode, setDebugMode] = useState(false)
  const [debugEvents, setDebugEvents] = useState<{ type: string; run_id: string; data: unknown }[]>([])

  // 侧边栏 + 任务面板状态
  const [sessions, setSessions] = useState<Session[]>([])
  const [activeSessionId, setActiveSessionId] = useState('')
  const [runs, setRuns] = useState<RunSummary[]>([])
  const [skills, setSkills] = useState<SkillMeta[]>(MOCK_SKILLS)
  const [view, setView] = useState<PageView>('chat')

  // 斜杠命令补全 + 帮助弹窗状态
  const [slashVisible, setSlashVisible] = useState(false)
  const [slashCursor, setSlashCursor] = useState(0)
  const [slashFiltered, setSlashFiltered] = useState<SlashItem[]>([])
  const [showHelp, setShowHelp] = useState(false)
  // 自定义确认框：仅清空会话时使用
  const [confirmClear, setConfirmClear] = useState(false)

  // 启动时应用用户保存的主题（未设置过则跟随系统）
  useEffect(() => {
    applyTheme(loadThemePreference())
  }, [])

  // 虚拟列表：默认保留最近 windowSize 条；用户向上滚动时冻结起点（窗口改为向下增长），
  // 避免流式追加把正在阅读的内容挤出 DOM。
  const tailStart = Math.max(0, messages.length - windowSize)
  const windowStart = pinnedStart !== null ? Math.min(pinnedStart, tailStart) : tailStart
  const visibleMessages = windowStart > 0 ? messages.slice(windowStart) : messages

  const sessionIdRef = useRef<string>('')
  const activeRunIdRef = useRef<string>('')
  const subRunIdsRef = useRef<Set<string>>(new Set())
  const pendingCompactionRef = useRef<{
    run_id: string
    original_tokens: number
    summary_tokens: number
  } | null>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const chatScrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const isUserScrollingRef = useRef(false)
  // 权限响应回调的 ref（供全局键盘快捷键调用，避免闭包过期）
  const permissionRespondRef = useRef<
    ((toolUseId: string, decision: PermissionDecision) => void) | null
  >(null)
  const debugModeRef = useRef(false)
  const showHelpRef = useRef(false)
  // 消息队列的镜像（供事件回调安全读取，避免在 setState updater 内做副作用）
  const messageQueueRef = useRef<string[]>([])
  // 最近一次由本会话发出的消息文本。run 事件不带 session_id，
  // 只能用 goal 区分「本会话的 run」与「工作台 agent.run」，避免事件串台。
  const pendingSendRef = useRef<string>('')
  // 用户是否主动中止了当前 run（用于区分中止与真实 RPC 错误）
  const abortingRef = useRef(false)
  // send 的最新引用（供 onInputKeyDown 调用，避免在定义前引用造成 TDZ）
  const sendRef = useRef<() => void>(() => {})
  // rAF 批量合并 token
  const pendingTokensRef = useRef<string>('')
  const rafScheduledRef = useRef(false)

  // 同步 debugMode 到 ref
  useEffect(() => {
    debugModeRef.current = debugMode
    if (!debugMode) setDebugEvents([])
  }, [debugMode])

  // 同步 showHelp 到 ref（供全局 keydown 读取，避免闭包过期）
  useEffect(() => {
    showHelpRef.current = showHelp
  }, [showHelp])

  // 刷新会话列表
  const refreshSessions = useCallback(async () => {
    try {
      const list = await client.call('session.list')
      setSessions(list)
    } catch (err) {
      console.warn('[session] 刷新会话列表失败：', err)
    }
  }, [])

  // 刷新运行历史（后端从 trace 事件聚合）
  const refreshRuns = useCallback(async () => {
    try {
      const res = await client.call('run.list', {})
      setRuns(res.runs ?? [])
    } catch (err) {
      console.warn('[run] 刷新运行历史失败：', err)
    }
  }, [])

  // 斜杠补全候选 = 内建命令 + 技能（对齐 TUI buildSlashItems）
  const slashItems = useMemo<SlashItem[]>(
    () => [
      ...BUILTIN_COMMANDS.map((c) => ({ name: c.name, desc: c.desc, kind: 'command' as const })),
      ...skills.map((s) => ({ name: s.name, desc: s.description, kind: 'skill' as const })),
    ],
    [skills],
  )

  // 输入变化时刷新补全（仅当以 "/" 开头且不含空格，对齐 TUI refreshSlash）
  const refreshSlash = useCallback(
    (value: string) => {
      if (!value.startsWith('/') || value.includes(' ')) {
        setSlashVisible(false)
        return
      }
      const query = value.slice(1).toLowerCase()
      const filtered = slashItems.filter((it) => it.name.toLowerCase().includes(query))
      setSlashFiltered(filtered)
      setSlashCursor(0)
      setSlashVisible(filtered.length > 0)
    },
    [slashItems],
  )

  // 选中补全项：替换输入为 "/name "（带尾空格），光标移到最后
  const selectSlash = useCallback(
    (item: SlashItem) => {
      const full = `/${item.name} `
      setInput(full)
      setSlashVisible(false)
      requestAnimationFrame(() => {
        inputRef.current?.focus()
        const len = full.length
        inputRef.current?.setSelectionRange(len, len)
      })
    },
    [],
  )

  // 输入框键盘：斜杠补全弹窗打开时优先拦截方向键/确认/Esc
  const onInputKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (slashVisible) {
        if (e.key === 'ArrowDown') {
          e.preventDefault()
          setSlashCursor((c) => (slashFiltered.length ? (c + 1) % slashFiltered.length : 0))
          return
        }
        if (e.key === 'ArrowUp') {
          e.preventDefault()
          setSlashCursor((c) =>
            slashFiltered.length ? (c - 1 + slashFiltered.length) % slashFiltered.length : 0,
          )
          return
        }
        if (e.key === 'Enter' || e.key === 'Tab') {
          e.preventDefault()
          if (slashFiltered[slashCursor]) selectSlash(slashFiltered[slashCursor])
          return
        }
        if (e.key === 'Escape') {
          e.preventDefault()
          setSlashVisible(false)
          return
        }
      }
      // 斜杠弹窗外的常规按键
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        sendRef.current()
      }
    },
    [slashVisible, slashFiltered, slashCursor, selectSlash],
  )

  // 全局键盘：? 打开帮助（输入框内属于正常输入，不拦截）、Esc 关闭帮助、权限审批快捷键
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === '?') {
        const ta = inputRef.current
        const typingInInput = ta !== null && document.activeElement === ta
        if (!showHelpRef.current && !typingInInput) setShowHelp(true)
        return
      }
      if (e.key === 'Escape' && showHelpRef.current) {
        setShowHelp(false)
        return
      }
      // 权限审批快捷键：Y=允许一次 A=总是允许 N=拒绝一次 D=总是拒绝（作用于最早待审批项）
      const permKeys: Record<string, PermissionDecision> = {
        y: 'allow_once',
        a: 'always_allow',
        n: 'deny_once',
        d: 'always_deny',
      }
      const permElems = document.querySelectorAll<HTMLElement>('.perm-dialog')
      if (permElems.length > 0 && !e.metaKey && !e.ctrlKey && !e.altKey && e.key.length === 1) {
        const key = e.key.toLowerCase()
        const decision = permKeys[key]
        if (decision) {
          const first = permElems[0]
          const runId = first?.dataset.runId
          const toolUseId = first?.dataset.toolUseId
          if (runId && toolUseId) {
            e.preventDefault()
            permissionRespondRef.current?.(toolUseId, decision)
            return
          }
        }
      }
      if (e.key === 'Escape') {
        const openDetails = document.querySelectorAll<HTMLElement>('.chat-messages details[open]')
        openDetails.forEach((d) => d.removeAttribute('open'))
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // 初始化：连接 → 恢复最近会话（无则新建）→ 订阅事件
  useEffect(() => {
    let initialized = false

    const offStatus = client.onStatus(async (s) => {
      setStatus(s)
      if (s === 'connected') {
        if (!initialized) {
          initialized = true
          // 首次连接：恢复最近的未关闭会话，避免每次刷新都新建
          try {
            const list = await client.call('session.list')
            setSessions(list)
            const recent = [...list]
              .filter((it) => it.status !== 'closed')
              .sort((a, b) => (b.updated_at ?? '').localeCompare(a.updated_at ?? ''))[0]
            if (recent) {
              const history = await client.call('session.get_history', {
                session_id: recent.id,
              })
              sessionIdRef.current = recent.id
              setActiveSessionId(recent.id)
              setMessages(
                history.map((m) => ({ role: m.role, content: m.content })),
              )
              setToolStates(deriveToolStates(history))
            } else {
              const session = await client.call('session.create', {
                mode: 'chat',
                title: 'WebUI 会话',
              })
              sessionIdRef.current = session.id
              setActiveSessionId(session.id)
            }
          } catch {
            // 恢复失败时保持空态，不阻塞后续操作
          }
        }
        // 每次连接（含重连）都重新订阅事件 + 刷新会话列表
        await client.call('event.subscribe', {})
        await refreshSessions()
        await refreshRuns()
        // 拉取技能列表（斜杠补全 + 侧栏 Skills）；失败时保留 MOCK 兜底
        try {
          const list = await client.call('skill.list')
          if (Array.isArray(list) && list.length > 0) setSkills(list)
        } catch {
          /* 忽略，保持 MOCK_SKILLS */
        }
      }
    })

    const offEvent = client.onEvent((event) => {
      // 调试模式：记录所有事件
      if (debugModeRef.current) {
        const { type, ...data } = event
        // 只有 run 级事件带 run_id，session.* 等没有
        const runId = 'run_id' in event ? event.run_id : ''
        setDebugEvents((prev) => [...prev, { type, run_id: runId, data }])
      }

      switch (event.type) {
        /* ── run 生命周期 ── */
        case 'run.started': {
          // 子 Agent 的 run.started 不创建新消息
          if (subRunIdsRef.current.has(event.run_id)) return
          // 只认领本会话发起的 run：运行历史/工作台的 run 不带 session_id，
          // 若不加此判断，agent.run 的事件会串入当前会话消息流。
          if (!pendingSendRef.current || event.goal !== pendingSendRef.current) return
          pendingSendRef.current = ''
          activeRunIdRef.current = event.run_id
          setResponding(true)
          const compacted = pendingCompactionRef.current
          pendingCompactionRef.current = null
          setMessages((prev) => [
            ...prev,
            {
              role: 'assistant',
              content: [],
              runId: event.run_id,
              ts: Date.now(),
              compacted:
                compacted?.run_id === event.run_id
                  ? {
                      original_tokens: compacted.original_tokens,
                      summary_tokens: compacted.summary_tokens,
                    }
                  : undefined,
            },
          ])
          break
        }

        case 'run.finished': {
          if (event.run_id !== activeRunIdRef.current) return
          setResponding(false)
          activeRunIdRef.current = ''
          // 用户主动中止：标记 run 组为已中止，并把仍在执行的工具置为已中止。
          // 注意：标志由 send() 收尾时清除，此处只读不清，否则 send 的 catch
          // 会因标志已被消费而把中止误报为发送失败。
          if (abortingRef.current) {
            setMessages((prev) => {
              const last = prev[prev.length - 1]
              if (!last || last.role !== 'assistant' || last.runId !== event.run_id) return prev
              return [...prev.slice(0, -1), { ...last, aborted: true }]
            })
            setToolStates((prev) => {
              const next = { ...prev }
              for (const [id, st] of Object.entries(next)) {
                if (st.status === 'running') next[id] = { ...st, status: 'aborted' }
              }
              return next
            })
          }
          // 运行结束后刷新会话列表（更新 updated_at）与运行历史（新 trace）
          refreshSessions()
          refreshRuns()
          // 处理消息队列：如果有排队消息，自动发送下一条。
          // 注意：发送动作不能放在 setMessageQueue 的 updater 内（StrictMode 会双调用，
          // 导致消息重复发送），因此通过 ref 读取队列。
          const [next, ...rest] = messageQueueRef.current
          if (next && sessionIdRef.current) {
            messageQueueRef.current = rest
            setMessageQueue(rest)
            pendingSendRef.current = next
            setTimeout(() => {
              client
                .call('session.send_message', {
                  session_id: sessionIdRef.current!,
                  content: next,
                })
                .catch(() => {
                  /* 队列发送失败，忽略 */
                })
            }, 100)
          }
          break
        }

        /* ── LLM 流式 ── */
        case 'llm.token': {
          if (event.run_id !== activeRunIdRef.current) return
          // rAF 批量合并：累积 token，在下一帧统一更新 DOM
          pendingTokensRef.current += event.token
          if (!rafScheduledRef.current) {
            rafScheduledRef.current = true
            requestAnimationFrame(() => {
              const tokens = pendingTokensRef.current
              pendingTokensRef.current = ''
              rafScheduledRef.current = false
              if (tokens) {
                setMessages((prev) => {
                  const last = prev[prev.length - 1]
                  if (!last || last.role !== 'assistant' || last.runId !== activeRunIdRef.current)
                    return prev
                  const lastBlock = last.content[last.content.length - 1]
                  if (lastBlock?.type === 'text') {
                    return [
                      ...prev.slice(0, -1),
                      {
                        ...last,
                        content: [
                          ...last.content.slice(0, -1),
                          { ...lastBlock, text: (lastBlock.text ?? '') + tokens },
                        ],
                      },
                    ]
                  }
                  return [
                    ...prev.slice(0, -1),
                    {
                      ...last,
                      content: [...last.content, { type: 'text', text: tokens }],
                    },
                  ]
                })
              }
            })
          }
          break
        }

        /* ── LLM 响应收口 ── */
        case 'llm.response': {
          // llm.response 标记本轮 LLM 输出结束，后续可能有 tool 调用
          break
        }

        /* ── LLM 用量 ── */
        case 'llm.usage': {
          if (event.run_id !== activeRunIdRef.current) return
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (!last || last.role !== 'assistant' || last.runId !== event.run_id)
              return prev
            return [
              ...prev.slice(0, -1),
              {
                ...last,
                usage: {
                  input_tokens: event.input_tokens,
                  output_tokens: event.output_tokens,
                  context_pct: event.context_pct,
                },
              },
            ]
          })
          break
        }

        /* ── 工具调用 ── */
        case 'tool.call_started': {
          if (event.run_id !== activeRunIdRef.current) return
          const { tool_use_id, tool_name, params } = event
          setToolStates((prev) => ({
            ...prev,
            [tool_use_id]: { status: 'running' },
          }))
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (!last || last.role !== 'assistant' || last.runId !== event.run_id)
              return prev
            return [
              ...prev.slice(0, -1),
              {
                ...last,
                content: [
                  ...last.content,
                  { type: 'tool_use', id: tool_use_id, name: tool_name, input: params },
                ],
              },
            ]
          })
          break
        }

        case 'tool.call_finished': {
          if (event.run_id !== activeRunIdRef.current) return
          setToolStates((prev) => ({
            ...prev,
            [event.tool_use_id]: {
              status: 'success',
              output: event.output,
              elapsedMs: event.elapsed_ms,
            },
          }))
          break
        }

        case 'tool.call_failed': {
          if (event.run_id !== activeRunIdRef.current) return
          setToolStates((prev) => ({
            ...prev,
            [event.tool_use_id]: {
              status: 'failed',
              error: event.error,
              errorType: event.error_type,
              elapsedMs: event.elapsed_ms,
            },
          }))
          break
        }

        /* ── 权限审批 ── */
        case 'permission.requested': {
          // 只处理本会话 run 的权限请求，避免工作台的请求串入
          if (event.run_id !== activeRunIdRef.current) return
          setPendingPermissions((prev) => [
            ...prev,
            {
              tool_use_id: event.tool_use_id,
              tool_name: event.tool_name,
              params: event.params,
              preview: event.preview,
              run_id: event.run_id,
            },
          ])
          break
        }

        case 'permission.granted':
        case 'permission.denied': {
          setPendingPermissions((prev) =>
            prev.filter((p) => p.tool_use_id !== event.tool_use_id),
          )
          break
        }

        /* ── Skill ── */
        case 'skill.invoked': {
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (!last || last.role !== 'assistant' || last.runId !== event.run_id)
              return prev
            return [
              ...prev.slice(0, -1),
              { ...last, skillName: event.skill_name },
            ]
          })
          break
        }

        /* ── 上下文压缩 ── */
        case 'context.compacted': {
          if (event.run_id !== activeRunIdRef.current) return
          pendingCompactionRef.current = {
            run_id: event.run_id,
            original_tokens: event.original_tokens,
            summary_tokens: event.summary_tokens,
          }
          break
        }

        /* ── 子 Agent ── */
        case 'subagent.started': {
          subRunIdsRef.current.add(event.run_id)
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (!last || last.role !== 'assistant' || last.runId !== event.parent_run_id)
              return prev
            const subagents = [
              ...(last.subagents ?? []),
              { runId: event.run_id, description: event.description, status: 'running' },
            ]
            return [...prev.slice(0, -1), { ...last, subagents }]
          })
          break
        }

        case 'subagent.finished': {
          subRunIdsRef.current.delete(event.run_id)
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (!last || last.role !== 'assistant' || last.runId !== event.parent_run_id)
              return prev
            const subagents = (last.subagents ?? []).map((s) =>
              s.runId === event.run_id ? { ...s, status: event.status } : s,
            )
            return [...prev.slice(0, -1), { ...last, subagents }]
          })
          break
        }

        /* ── 会话事件 ── */
        case 'session.created': {
          refreshSessions()
          break
        }

        case 'session.closed': {
          refreshSessions()
          break
        }
      }
    })

    client.connect()

    return () => {
      offStatus()
      offEvent()
      client.disconnect()
    }
  }, [refreshSessions, refreshRuns])

  // 自动滚动到底部（用户向上滚动时暂停）
  useEffect(() => {
    if (isUserScrollingRef.current) return
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // 检测用户滚动
  const handleScroll = useCallback(() => {
    const el = chatScrollRef.current
    if (!el) return
    const distFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight
    if (distFromBottom > 120) {
      // 刚向上滚动：冻结窗口起点，避免流式追加把正在阅读的内容挤走
      if (!isUserScrollingRef.current) {
        setPinnedStart(Math.max(0, messages.length - windowSize))
      }
      isUserScrollingRef.current = true
      setShowBackToBottom(true)
    } else {
      isUserScrollingRef.current = false
      setShowBackToBottom(false)
      setPinnedStart(null)
    }
  }, [messages.length, windowSize])

  // Esc 关闭展开的卡片（已在全局 keydown 中统一处理）
  // 权限响应：RPC 失败时移除该请求，避免对话框永久停留
  const handlePermissionRespond = useCallback(
    async (toolUseId: string, decision: PermissionDecision) => {
      try {
        await client.call('permission.respond', { tool_use_id: toolUseId, decision })
      } catch (err) {
        console.warn('[permission] 响应失败，移除该请求：', err)
        setPendingPermissions((prev) => prev.filter((p) => p.tool_use_id !== toolUseId))
      }
    },
    [],
  )
  // 同步到 ref，供全局键盘快捷键使用
  permissionRespondRef.current = handlePermissionRespond

  const send = useCallback(async () => {
    const text = input.trim()
    if (!text) return

    // 内建斜杠命令：前端处理，不发送后端（对齐 TUI handleCommand）
    if (text.startsWith('/')) {
      const cmdName = text.split(' ')[0].slice(1)
      const builtin = BUILTIN_COMMANDS.find((c) => c.name === cmdName)
      if (builtin) {
        setInput('')
        switch (builtin.name) {
          case 'new':
            await handleNewSession()
            break
          case 'compact':
            await handleCompact()
            break
          case 'clear':
            await handleClearSession()
            break
        }
        return
      }
    }

    if (!sessionIdRef.current) return

    // 检查会话是否已关闭
    const currentSession = sessions.find((s) => s.id === sessionIdRef.current)
    if (currentSession?.status === 'closed') return

    setInput('')

    if (responding) {
      // run 进行中，加入队列
      messageQueueRef.current = [...messageQueueRef.current, text]
      setMessageQueue(messageQueueRef.current)
      setMessages((prev) => [
        ...prev,
        { role: 'user', content: [{ type: 'text', text }], queued: true, ts: Date.now() },
      ])
      return
    }

    setMessages((prev) => [
      ...prev,
      { role: 'user', content: [{ type: 'text', text }], ts: Date.now() },
    ])
    pendingSendRef.current = text
    try {
      await client.call('session.send_message', {
        session_id: sessionIdRef.current,
        content: text,
      })
    } catch (err) {
      // 用户主动中止时，后端会以 context canceled 结束本次 send_message，
      // 这属于预期行为，不应显示为发送失败。
      if (!abortingRef.current) {
        // RPC 错误内联显示，不打断会话
        setMessages((prev) => [
          ...prev,
          {
            role: 'assistant',
            content: [
              {
                type: 'text',
                text: `[发送失败] ${err instanceof Error ? err.message : String(err)}`,
              },
            ],
          },
        ])
      }
    } finally {
      // 本次发送周期结束，清除中止标志（run.finished 已在收尾时读取过）
      pendingSendRef.current = ''
      abortingRef.current = false
    }
  }, [input, responding, sessions])

  // 同步 send 最新引用（onInputKeyDown 经此调用，避免 TDZ 与闭包过期）
  sendRef.current = send

  // 中止当前 run
  const handleAbort = useCallback(async () => {
    if (!activeRunIdRef.current) return
    abortingRef.current = true
    await client.call('agent.abort', { run_id: activeRunIdRef.current })
  }, [])

  // 压缩历史
  const handleCompact = useCallback(async () => {
    if (!sessionIdRef.current) return
    try {
      await client.call('session.compact', { session_id: sessionIdRef.current })
    } catch (err) {
      console.warn('[session] 压缩上下文失败：', err)
    }
  }, [])

  // 切换/清空会话时重置虚拟列表窗口与滚动状态
  const resetChatWindow = useCallback(() => {
    setWindowSize(VIRTUAL_THRESHOLD)
    setPinnedStart(null)
    setShowBackToBottom(false)
    isUserScrollingRef.current = false
  }, [])

  // 清空会话：先弹自定义确认框
  const handleClearSession = useCallback(async () => {
    setConfirmClear(true)
  }, [])

  // 确认后真正清空
  const handleConfirmClear = useCallback(async () => {
    setConfirmClear(false)
    if (!sessionIdRef.current) return
    try {
      await client.call('session.clear', { session_id: sessionIdRef.current })
    } catch (err) {
      console.warn('[session] 清空会话失败：', err)
      return
    }
    setMessages([])
    setToolStates({})
    setPendingPermissions([])
    resetChatWindow()
    activeRunIdRef.current = ''
    subRunIdsRef.current.clear()
    // 旧 run 的 run.finished 已被丢弃，需同步复位，否则「停止」按钮卡在运行态
    setResponding(false)
  }, [resetChatWindow])

  // 回到底部
  const handleBackToBottom = useCallback(() => {
    isUserScrollingRef.current = false
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [])

  // Skill 点击触发：切换到对话页，输入框填入 /name
  const handleSkillClick = useCallback((skillName: string) => {
    setView('chat')
    setInput(`/${skillName} `)
  }, [])

  // 新建会话
  const handleNewSession = useCallback(async () => {
    let session
    try {
      session = await client.call('session.create', {
        mode: 'chat',
        title: `会话 ${Date.now().toString().slice(-4)}`,
      })
    } catch (err) {
      console.warn('[session] 创建会话失败：', err)
      return
    }
    sessionIdRef.current = session.id
    setActiveSessionId(session.id)
    setMessages([])
    setToolStates({})
    setPendingPermissions([])
    resetChatWindow()
    activeRunIdRef.current = ''
    subRunIdsRef.current.clear()
    // 旧 run 的 run.finished 已被丢弃，需同步复位
    setResponding(false)
    await refreshSessions()
  }, [refreshSessions, resetChatWindow])

  // 切换会话
  const handleSelectSession = useCallback(async (id: string) => {
    if (id === sessionIdRef.current) return
    let history
    try {
      history = await client.call('session.get_history', { session_id: id })
    } catch (err) {
      console.warn('[session] 加载历史失败：', err)
      return
    }
    sessionIdRef.current = id
    setActiveSessionId(id)
    setMessages(
      history.map((m) => ({
        role: m.role,
        content: m.content,
      })),
    )
    setToolStates(deriveToolStates(history))
    setPendingPermissions([])
    resetChatWindow()
    activeRunIdRef.current = ''
    subRunIdsRef.current.clear()
    // 旧 run 的 run.finished 已被丢弃，需同步复位
    setResponding(false)
  }, [resetChatWindow])

  // 关闭会话
  const handleCloseSession = useCallback(async (id: string) => {
    try {
      await client.call('session.close', { session_id: id })
    } catch (err) {
      console.warn('[session] 关闭会话失败：', err)
      return
    }
    if (id === sessionIdRef.current) {
      // 当前会话被关闭，清空
      setMessages([])
      setToolStates({})
      resetChatWindow()
      sessionIdRef.current = ''
      setActiveSessionId('')
    }
    await refreshSessions()
  }, [refreshSessions, resetChatWindow])

  return (
    <div className="app-layout">
      <NavRail active={view} onChange={setView} />
      {view === 'chat' ? (
        <>
          <Sidebar
            sessions={sessions}
            activeSessionId={activeSessionId}
            skills={skills}
            onSelectSession={handleSelectSession}
            onNewSession={handleNewSession}
            onCloseSession={handleCloseSession}
            onClearSession={handleClearSession}
            onSkillClick={handleSkillClick}
          />
          <div className="main-area">
            <div className="chat-container">
          <header className="chat-header">
            <div className="header-left">
              <h1>Numbat</h1>
              {USE_MOCK && <span className="mock-badge">MOCK</span>}
              {messageQueue.length > 0 && (
                <span className="queue-badge">队列 {messageQueue.length}</span>
              )}
            </div>
            <div className="header-right">
              <button
                type="button"
                className="help-btn"
                onClick={() => setShowHelp(true)}
                title="快捷键帮助"
              >
                帮助
              </button>
              <button
                type="button"
                className="compact-btn"
                onClick={handleCompact}
                disabled={!activeSessionId || responding}
                title="压缩对话历史"
              >
                压缩
              </button>
              <button
                type="button"
                className={`debug-btn ${debugMode ? 'active' : ''}`}
                onClick={() => setDebugMode((v) => !v)}
                title="调试模式"
              >
                调试
              </button>
              <span className={`status status-${status}`}>{STATUS_LABEL[status]}</span>
            </div>
          </header>

          {status === 'disconnected' && (
            <div className="disconnect-banner">已断开连接，正在尝试重连…</div>
          )}

          {debugMode && (
            <div className="debug-panel">
              <div className="debug-panel-header">
                <span>调试事件流 ({debugEvents.length})</span>
                <button type="button" onClick={() => setDebugEvents([])}>清空</button>
              </div>
              <div className="debug-event-list">
                {debugEvents.slice(-50).map((e, i) => (
                  <div key={i} className="debug-event">
                    <span className={`debug-event-type debug-evt-${e.type.split('.')[0]}`}>{e.type}</span>
                    <span className="debug-event-run">{e.run_id}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          <div className="chat-messages" ref={chatScrollRef} onScroll={handleScroll}>
            {messages.length === 0 && (
              <div className="empty-hint">
                发送一条消息开始对话
                <br />
                <span className="dim">试试以下场景：</span>
                <div className="scenario-list">
                  {SCENARIOS.map((s) => (
                    <span key={s} className="scenario-item">{s}</span>
                  ))}
                </div>
              </div>
            )}
            {windowStart > 0 && (
              <div className="virtual-more">
                <button
                  type="button"
                  className="virtual-more-btn"
                  onClick={() => setWindowSize((s) => s + VIRTUAL_THRESHOLD)}
                >
                  已折叠 {windowStart} 条更早的消息 · 点击展开
                </button>
              </div>
            )}
            {visibleMessages.map((msg, i) => (
              // key 用绝对下标，保证窗口滑动时 React 不会复用到别的消息
              <div key={windowStart + i} className={`message message-${msg.role}`}>
                <div className="message-meta">
                  <span className="role-label">
                    {msg.queued ? '排队' : msg.role === 'user' ? '你' : 'Numbat'}
                  </span>
                  {msg.ts && <span className="msg-time">{formatTime(msg.ts)}</span>}
                </div>
                {msg.role === 'user' ? (
                  <div className={`bubble ${msg.queued ? 'bubble-queued' : ''}`}>
                    {msg.content.map((block, j) => (
                      <span key={j}>{block.text}</span>
                    ))}
                  </div>
                ) : (
                  <div className="assistant-content">
                    {(msg.skillName || msg.compacted || msg.aborted) && (
                      <div className="msg-badges">
                        {msg.skillName && <SkillBadge skillName={msg.skillName} />}
                        {msg.compacted && (
                          <CompactionBadge
                            originalTokens={msg.compacted.original_tokens}
                            summaryTokens={msg.compacted.summary_tokens}
                          />
                        )}
                        {msg.aborted && (
                          <span className="aborted-badge">⏹ 已中止</span>
                        )}
                      </div>
                    )}
                    {msg.subagents?.length ? (
                      <SubagentTree subagents={msg.subagents} />
                    ) : null}
                    {msg.content.map((block, j) => {
                      if (block.type === 'text') {
                        return <MarkdownRenderer key={j} content={block.text ?? ''} />
                      }
                      if (block.type === 'thinking') {
                        return (
                          <details key={j} className="thinking-block">
                            <summary>思考过程</summary>
                            <p className="thinking-text">{block.thinking}</p>
                          </details>
                        )
                      }
                      if (block.type === 'tool_use') {
                        const toolUseId = block.id ?? ''
                        return (
                          <ToolCallCard
                            key={j}
                            toolUse={block}
                            state={toolStates[toolUseId]}
                          />
                        )
                      }
                      return null
                    })}
                    {responding && windowStart + i === messages.length - 1 && (
                      <span className="cursor" />
                    )}
                  </div>
                )}
                {msg.role === 'assistant' &&
                  pendingPermissions
                    .filter((p) => p.run_id === msg.runId)
                    .map((perm) => (
                      <PermissionDialog
                        key={perm.tool_use_id}
                        runId={perm.run_id}
                        toolUseId={perm.tool_use_id}
                        toolName={perm.tool_name}
                        params={perm.params}
                        preview={perm.preview}
                        onRespond={(d) =>
                          handlePermissionRespond(perm.tool_use_id, d)
                        }
                      />
                    ))}
                {msg.usage && (
                  <UsageBadge
                    inputTokens={msg.usage.input_tokens}
                    outputTokens={msg.usage.output_tokens}
                    contextPct={msg.usage.context_pct}
                  />
                )}
              </div>
            ))}
            <div ref={messagesEndRef} />
          </div>

          {showBackToBottom && (
            <button type="button" className="back-to-bottom" onClick={handleBackToBottom}>
              ↓ 回到底部
            </button>
          )}

          <div className="chat-input">
            <SlashAutocomplete
              visible={slashVisible}
              items={slashFiltered}
              cursor={slashCursor}
              onSelect={selectSlash}
            />
            <textarea
              ref={inputRef}
              value={input}
              onChange={(e) => {
                setInput(e.target.value)
                refreshSlash(e.target.value)
              }}
              onKeyDown={onInputKeyDown}
              placeholder={
                responding
                  ? '输入消息将排队等待…（Enter 排队）'
                  : '输入消息…（Enter 发送，Shift+Enter 换行，/ 打开命令补全）'
              }
              rows={1}
              disabled={status !== 'connected'}
            />
            <button
              type="button"
              onClick={responding ? handleAbort : send}
              disabled={!responding && (!input.trim() || status !== 'connected')}
              className={responding ? 'send-btn-stop' : ''}
            >
              {responding ? '停止' : '发送'}
            </button>
          </div>
        </div>

      </div>
        </>
      ) : (
        <div className="page-content">
          {view === 'runs' && <RunHistoryPage runs={runs} />}
          {view === 'settings' && (
            <SettingsPage isMock={USE_MOCK} />
          )}
          {view === 'workbench' && (
            <WorkbenchPage client={client} />
          )}
        </div>
      )}

      {showHelp && <HelpModal onClose={() => setShowHelp(false)} />}

      {confirmClear && (
        <ConfirmDialog
          title="清空会话"
          message="确定清空当前会话的所有消息吗？此操作不可撤销。"
          confirmText="确认清空"
          cancelText="取消"
          onConfirm={handleConfirmClear}
          onCancel={() => setConfirmClear(false)}
        />
      )}
    </div>
  )
}

export default App
