// Mock 协议边界：实现与 WsClient 相同的公开接口。
// 模拟全量后端能力：RPC 调用 + 18 种事件流 + 权限审批 + Skill + 子 Agent + Task。
// 事实来源：src/types/api.ts（镜像 Go struct）。
import type {
  AgentProfile,
  ContentBlock,
  Event,
  Message,
  PermissionDecision,
  RPCMethod,
  RPCParams,
  RPCResult,
  RunStatus,
  RunSummary,
  Session,
  SessionMode,
  SkillMeta,
  Task,
} from '@/types/api'
import type { ConnectionStatus, EventHandler, StatusHandler } from './ws-client'

/* ── Mock 静态数据 ──────────────────────────────────── */

export const MOCK_SKILLS: SkillMeta[] = [
  { name: 'init', description: '分析当前项目，生成 .numbat/context.md 初始内容', allowed_tools: ['read_file', 'list_dir', 'write_file', 'bash'] },
  { name: 'orchestrate', description: '用 planner→executor→reviewer 三阶段 Multi-agent 工作流完成复杂任务', allowed_tools: ['spawn_agent', 'agent_result', 'task_create', 'task_update', 'task_list'] },
  { name: 'review', description: '对指定路径做代码审查，输出严重/建议/可选三级分类', allowed_tools: ['read_file', 'list_dir', 'bash'] },
  { name: 'summarize', description: '将当前 session 对话压缩为人类可读摘要', allowed_tools: ['note_save'] },
]

export const MOCK_AGENT_PROFILES: AgentProfile[] = [
  { name: 'planner', description: '规划专家：只读取分析，不修改；输出有序执行步骤', allowed_tools: ['read_file', 'list_dir', 'task_create', 'task_update'], model: 'claude-sonnet-4-6' },
  { name: 'executor', description: '执行专家：严格按计划执行，不做评判', allowed_tools: ['bash', 'read_file', 'write_file', 'list_dir', 'task_update', 'task_list'], model: 'claude-sonnet-4-6' },
  { name: 'reviewer', description: '审查专家：核查结果是否达成目标，客观评估', allowed_tools: ['read_file', 'list_dir', 'bash'], model: 'claude-sonnet-4-6' },
]

const ts = (offset: number) => {
  const d = new Date(Date.now() + offset)
  return d.toISOString()
}

export const MOCK_INITIAL_TASKS: Task[] = [
  { id: 1, subject: '分析项目结构', description: '读取项目目录和关键文件，理解整体架构', status: 'completed', blocked_by: [], created_at: ts(-3600000), updated_at: ts(-3500000) },
  { id: 2, subject: '实现核心功能', description: '编写主要业务逻辑代码', status: 'in_progress', blocked_by: [1], created_at: ts(-3500000), updated_at: ts(-1800000) },
  { id: 3, subject: '编写单元测试', description: '为核心功能编写测试用例', status: 'pending', blocked_by: [2], created_at: ts(-1800000), updated_at: ts(-1800000) },
  { id: 4, subject: '代码审查', description: '检查代码质量和规范', status: 'pending', blocked_by: [3], created_at: ts(-1800000), updated_at: ts(-1800000) },
]

/* ── 工具元数据（后端 internal/tools/） ──────────────── */

export interface ToolMeta {
  name: string
  description: string
  category: 'filesystem' | 'execution' | 'task' | 'agent' | 'search' | 'skill'
  dangerous: boolean
}

// 与后端注册的工具一一对应（internal/tools/builtin/*.go、internal/subagent/spawn.go）。
// 不列后端不存在的工具（AGENTS.md §5）。
export const MOCK_TOOLS: ToolMeta[] = [
  { name: 'bash', description: '执行 shell 命令', category: 'execution', dangerous: true },
  { name: 'read_file', description: '读取文件内容', category: 'filesystem', dangerous: false },
  { name: 'write_file', description: '写入文件内容', category: 'filesystem', dangerous: true },
  { name: 'list_dir', description: '列出目录内容', category: 'filesystem', dangerous: false },
  { name: 'note_save', description: '记录会话笔记', category: 'filesystem', dangerous: false },
  { name: 'task_create', description: '创建任务', category: 'task', dangerous: false },
  { name: 'task_update', description: '更新任务状态', category: 'task', dangerous: false },
  { name: 'task_list', description: '列出所有任务', category: 'task', dangerous: false },
  { name: 'task_get', description: '查询任务详情', category: 'task', dangerous: false },
  { name: 'spawn_agent', description: '生成子 Agent', category: 'agent', dangerous: false },
  { name: 'agent_result', description: '查询子 Agent 结果', category: 'agent', dangerous: false },
]

/* ── 运行历史记录 ─────────────────────────────────── */
// 形状与后端 run.list 一致（internal/trace/reader.go 的 RunSummary），
// 字段名与状态词表不得偏离 api.ts。

export const MOCK_RUN_HISTORY: RunSummary[] = [
  {
    run_id: 'run-001',
    goal: '分析项目架构',
    status: 'success',
    started_at: ts(-3600000),
    finished_at: ts(-3550000),
    duration_ms: 50000,
    subagents: [{ run_id: 'subrun-001', description: '架构分析子 Agent', status: 'success' }],
    tokens: { input_tokens: 3200, output_tokens: 1800, context_pct: 0.15 },
    tools: [
      { tool_name: 'spawn_agent', tool_use_id: 'tu-001', status: 'success', elapsed_ms: 48000 },
    ],
    events: [
      { type: 'run.started', ts: ts(-3600000), detail: 'goal: 分析项目架构' },
      { type: 'subagent.started', ts: ts(-3598000), detail: '架构分析子 Agent' },
      { type: 'subagent.finished', ts: ts(-3552000), detail: 'status: success' },
      { type: 'run.finished', ts: ts(-3550000), detail: 'status: success' },
    ],
  },
  {
    run_id: 'run-002',
    goal: '执行 go build 编译项目',
    status: 'success',
    started_at: ts(-1800000),
    finished_at: ts(-1780000),
    duration_ms: 20000,
    tokens: { input_tokens: 1500, output_tokens: 800, context_pct: 0.08 },
    tools: [{ tool_name: 'bash', tool_use_id: 'tu-002', status: 'success', elapsed_ms: 3200 }],
    subagents: [],
    events: [
      { type: 'run.started', ts: ts(-1800000), detail: 'goal: 执行 go build' },
      { type: 'permission.requested', ts: ts(-1798000), detail: 'bash' },
      { type: 'permission.granted', ts: ts(-1796000), detail: 'bash 已授权' },
      { type: 'tool.call_started', ts: ts(-1795000), detail: 'bash' },
      { type: 'tool.call_finished', ts: ts(-1792000), detail: 'bash — 3200ms' },
      { type: 'run.finished', ts: ts(-1780000), detail: 'status: success' },
    ],
  },
  {
    run_id: 'run-003',
    goal: '/orchestrate 重构认证模块',
    status: 'success',
    started_at: ts(-1200000),
    finished_at: ts(-1100000),
    duration_ms: 100000,
    skill: 'orchestrate',
    tokens: { input_tokens: 8500, output_tokens: 4200, context_pct: 0.42 },
    tools: [
      { tool_name: 'list_dir', tool_use_id: 'tu-003', status: 'success', elapsed_ms: 800 },
      { tool_name: 'read_file', tool_use_id: 'tu-004', status: 'success', elapsed_ms: 1200 },
      { tool_name: 'task_create', tool_use_id: 'tu-005', status: 'success', elapsed_ms: 300 },
      { tool_name: 'task_create', tool_use_id: 'tu-006', status: 'success', elapsed_ms: 300 },
      { tool_name: 'bash', tool_use_id: 'tu-007', status: 'success', elapsed_ms: 5400 },
      { tool_name: 'write_file', tool_use_id: 'tu-008', status: 'success', elapsed_ms: 600 },
      { tool_name: 'read_file', tool_use_id: 'tu-009', status: 'success', elapsed_ms: 900 },
    ],
    subagents: [
      { run_id: 'subrun-002', description: 'planner: 规划重构步骤', status: 'success' },
      { run_id: 'subrun-003', description: 'executor: 执行重构', status: 'success' },
      { run_id: 'subrun-004', description: 'reviewer: 审查结果', status: 'failed' },
    ],
    events: [
      { type: 'skill.invoked', ts: ts(-1200000), detail: 'orchestrate' },
      { type: 'run.started', ts: ts(-1199000), detail: 'goal: 重构认证模块' },
      { type: 'subagent.started', ts: ts(-1195000), detail: 'planner: 规划重构步骤' },
      { type: 'subagent.finished', ts: ts(-1170000), detail: 'status: success' },
      { type: 'subagent.started', ts: ts(-1168000), detail: 'executor: 执行重构' },
      { type: 'subagent.finished', ts: ts(-1130000), detail: 'status: success' },
      { type: 'subagent.started', ts: ts(-1128000), detail: 'reviewer: 审查结果' },
      { type: 'subagent.finished', ts: ts(-1102000), detail: 'status: failed' },
      { type: 'run.finished', ts: ts(-1100000), detail: 'status: success' },
    ],
  },
  {
    run_id: 'run-004',
    goal: '读取 config.yaml 配置文件',
    status: 'failed',
    started_at: ts(-600000),
    finished_at: ts(-580000),
    duration_ms: 20000,
    tokens: { input_tokens: 800, output_tokens: 400, context_pct: 0.05 },
    tools: [{ tool_name: 'read_file', tool_use_id: 'tu-010', status: 'failed', elapsed_ms: 500 }],
    subagents: [],
    events: [
      { type: 'run.started', ts: ts(-600000), detail: 'goal: 读取 config.yaml' },
      { type: 'tool.call_started', ts: ts(-598000), detail: 'read_file' },
      { type: 'tool.call_failed', ts: ts(-580000), detail: 'read_file ✕ file not found: config.yaml' },
      { type: 'run.finished', ts: ts(-580000), detail: 'status: failed' },
    ],
  },
  {
    run_id: 'run-005',
    goal: '/summarize 压缩当前对话',
    status: 'running',
    started_at: ts(-300000),
    finished_at: '',
    duration_ms: 0,
    skill: 'summarize',
    tokens: { input_tokens: 5200, output_tokens: 600, context_pct: 0.28 },
    tools: [],
    subagents: [],
    events: [
      { type: 'skill.invoked', ts: ts(-300000), detail: 'summarize' },
      { type: 'context.compacted', ts: ts(-290000), detail: '5000 → 200 tokens' },
      { type: 'run.started', ts: ts(-289000), detail: 'goal: 压缩对话' },
    ],
  },
]

/* ── 工具输出样本 ──────────────────────────────────── */

const MOCK_FILE_CONTENT = `package main

import "fmt"

func main() {
\tfmt.Println("Hello, Numbat")
}`

const MOCK_BASH_OUTPUT = `total 24
drwxr-xr-x  2 root root 4096 Sep  9 23:00 .
drwxr-xr-x  8 root root 4096 Sep  9 22:00 ..
-rw-r--r--  1 root root  1024 Sep  9 23:00 main.go
-rw-r--r--  1 root root   120 Sep  9 22:00 go.mod`

const MOCK_DIR_LISTING = `main.go
go.mod
go.sum
internal/
cmd/
docs/
webui/
AGENTS.md`

const MOCK_REVIEW_RESULT = `## 代码审查结果

### 严重 (1)
- main.go:6 — 未处理 fmt.Println 返回的 error

### 建议 (2)
- main.go:5 — import 路径可用 gofmt 规范化
- main.go:6 — 建议使用 log.Println 替代 fmt.Println

### 可选 (1)
- 添加 README.md 说明项目用途`

const MOCK_SUBAGENT_RESULT = `项目架构分析完成：

1. **入口**: cmd/ 目录，标准 Go 项目布局
2. **核心包**: internal/ 包含 transport、session、llm、events、tools、skills、agents、subagent、compact、memory、loop
3. **传输层**: 统一 WebSocket 网关 (:7438)，JSON-RPC 2.0
4. **事件总线**: topic-based pub/sub + replay
5. **前端**: webui/ (React + TypeScript + Vite)

建议：考虑将 internal/transport 拆分为 gateway 和 rpc 子包。`

/* ── Mock 会话状态 ──────────────────────────────────── */

interface MockSession {
  session: Session
  messages: Message[]
}

/* ── MockClient ────────────────────────────────────── */

export class MockClient {
  private eventHandlers = new Set<EventHandler>()
  private statusHandlers = new Set<StatusHandler>()
  private status: ConnectionStatus = 'disconnected'

  // 会话状态
  private sessions = new Map<string, MockSession>()
  private sessionCounter = 1

  // ID 生成
  private runCounter = 1
  private subRunCounter = 1
  private toolUseCounter = 1
  private subCounter = 1
  private taskCounter = 1

  // 权限审批等待
  private permissionResolvers = new Map<string, (d: PermissionDecision) => void>()
  /** tool_use_id → run_id，便于中止 run 时结束其待审批请求 */
  private permissionRuns = new Map<string, string>()

  // 权限持久化决策（模拟后端 policy.toml）
  private permissionPolicy = new Map<string, 'allow' | 'deny'>()

  // 任务列表（Mock 全局，初始化为预设任务）
  private tasks: Task[] = MOCK_INITIAL_TASKS.map((t) => ({ ...t }))

  // 运行追踪
  private activeRuns = new Set<string>()

  /* ── 公开 API（与 WsClient 接口一致） ────────────── */

  connect(): void {
    this.setStatus('connecting')
    setTimeout(() => this.setStatus('connected'), 80)
  }

  disconnect(): void {
    this.activeRuns.clear()
    for (const resolver of this.permissionResolvers.values()) {
      resolver('deny_once')
    }
    this.permissionResolvers.clear()
    this.permissionRuns.clear()
    this.setStatus('disconnected')
  }

  getStatus(): ConnectionStatus {
    return this.status
  }

  onEvent(handler: EventHandler): () => void {
    this.eventHandlers.add(handler)
    return () => this.eventHandlers.delete(handler)
  }

  onStatus(handler: StatusHandler): () => void {
    this.statusHandlers.add(handler)
    handler(this.status)
    return () => this.statusHandlers.delete(handler)
  }

  call<M extends RPCMethod>(
    method: M,
    ...args: RPCParams<M> extends undefined ? [] : [params: RPCParams<M>]
  ): Promise<RPCResult<M>> {
    return this.handleCall(method, args[0]) as Promise<RPCResult<M>>
  }

  /* ── RPC 分发 ──────────────────────────────────── */

  private async handleCall(method: string, params: unknown): Promise<unknown> {
    switch (method) {
      case 'core.ping':
        return 'pong'

      case 'skill.list':
        return MOCK_SKILLS

      case 'agent.run': {
        const p = params as { goal: string }
        const runId = this.genRunId()
        this.activeRuns.add(runId)
        // 忠于后端：agent.run 同步阻塞，跑完整个循环后一并返回结果，
        // 期间的 run.started / llm.token / tool.* 事件在返回前已推送。
        const result = await this.simulateRun(runId, p.goal, '')
        if (result === null) throw new Error('context canceled')
        return { run_id: runId, status: 'success', result }
      }

      case 'agent.abort': {
        const p = params as { run_id: string }
        if (this.activeRuns.has(p.run_id)) {
          this.activeRuns.delete(p.run_id)
          // 结束该 run 下待审批的权限请求，否则 run 会一直卡到审批超时
          this.cancelPendingPermissions(p.run_id)
          return true
        }
        return false
      }

      case 'permission.respond': {
        const p = params as { tool_use_id: string; decision: PermissionDecision }
        const resolver = this.permissionResolvers.get(p.tool_use_id)
        if (resolver) {
          this.permissionResolvers.delete(p.tool_use_id)
          resolver(p.decision)
          return true
        }
        return false
      }

      case 'session.create': {
        const p = params as { mode: SessionMode; title: string }
        return this.handleSessionCreate(p.mode, p.title)
      }

      case 'session.send_message': {
        const p = params as { session_id: string; content: string }
        return this.handleSendMessage(p.session_id, p.content)
      }

      case 'session.get_history': {
        const p = params as { session_id: string }
        const ms = this.sessions.get(p.session_id)
        return ms ? ms.messages : []
      }

      case 'session.list':
        return Array.from(this.sessions.values()).map((s) => s.session)

      case 'session.clear': {
        const p = params as { session_id: string }
        const ms = this.sessions.get(p.session_id)
        if (ms) ms.messages = []
        return 'cleared'
      }

      case 'session.close': {
        const p = params as { session_id: string }
        const ms = this.sessions.get(p.session_id)
        if (ms) {
          ms.session.status = 'closed'
          ms.session.updated_at = new Date().toISOString()
          this.emit({ type: 'session.closed', session_id: p.session_id })
        }
        return 'closed'
      }

      case 'session.compact': {
        const p = params as { session_id: string }
        const ms = this.sessions.get(p.session_id)
        if (ms) {
          const original = ms.messages
          ms.messages = [
            { role: 'user', content: [{ type: 'text', text: '[上下文已压缩] 此前对话摘要：用户与 Numbat 讨论了项目架构和开发计划。' }] },
            { role: 'assistant', content: [{ type: 'text', text: '了解，我将从摘要继续。' }] },
          ]
          const originalTokens = original.reduce((n, m) => n + m.content.reduce((s, c) => s + (c.text?.length ?? c.content?.length ?? 0), 0), 0) / 4
          return { original_tokens: Math.round(originalTokens), summary_tokens: 200 }
        }
        return { original_tokens: 0, summary_tokens: 0 }
      }

      case 'run.list': {
        const p = params as { limit?: number }
        const limit = p.limit && p.limit > 0 ? p.limit : MOCK_RUN_HISTORY.length
        const runs = MOCK_RUN_HISTORY.slice(0, limit)
        return { runs, total: MOCK_RUN_HISTORY.length }
      }

      case 'event.subscribe':
        return {
          subscription_id: `mock-sub-${this.subCounter++}`,
          replayed_count: 0,
        }

      default:
        throw new Error(`MockClient: 未实现的方法 ${method}`)
    }
  }

  /* ── 会话管理 ──────────────────────────────────── */

  private handleSessionCreate(mode: SessionMode, title: string): Session {
    const now = new Date().toISOString()
    const id = `mock-session-${String(this.sessionCounter++).padStart(3, '0')}`
    const session: Session = {
      id,
      mode,
      status: 'active',
      title,
      created_at: now,
      updated_at: now,
      run_ids: [],
    }
    this.sessions.set(id, { session, messages: [] })
    this.emit({ type: 'session.created', session_id: id, mode })
    return session
  }

  private async handleSendMessage(
    sessionId: string,
    content: string,
  ): Promise<{ run_id: string; status: RunStatus; result: string }> {
    const ms = this.sessions.get(sessionId)
    if (!ms) throw new Error('session not found')
    if (ms.session.status === 'closed') throw new Error('session is closed')

    // 记录用户消息
    ms.messages.push({ role: 'user', content: [{ type: 'text', text: content }] })

    // 自动压缩检测（阈值 2000 字符，低于后端 6000，便于测试）
    const totalChars = ms.messages.reduce(
      (n, m) => n + m.content.reduce((s, c) => s + (c.text?.length ?? c.content?.length ?? 0), 0),
      0,
    )
    let compactedBefore = false
    if (totalChars > 2000) {
      ms.messages = [
        { role: 'user', content: [{ type: 'text', text: '[上下文已自动压缩] 此前对话摘要。' }] },
        { role: 'assistant', content: [{ type: 'text', text: '了解，继续。' }] },
      ]
      compactedBefore = true
    }

    const runId = this.genRunId()
    this.activeRuns.add(runId)

    // 忠于后端：session.send_message 同步阻塞，跑完整个 agent loop 后才返回
    // （见 internal/transport/handler.go 的 handlers["session.send_message"]）。
    // 期间的事件在 RPC 返回前已全部推送。
    const result = await this.simulateRun(runId, content, sessionId, compactedBefore)
    if (result === null) throw new Error('context canceled')

    return { run_id: runId, status: 'success', result }
  }

  /* ── 场景检测 ──────────────────────────────────── */

  private detectScenario(message: string): string {
    if (message.startsWith('/')) {
      const skillName = message.slice(1).split(' ')[0]
      if (['init', 'orchestrate', 'review', 'summarize'].includes(skillName)) return `skill:${skillName}`
      return 'skill:unknown'
    }
    if (/重构|refactor|多步|multi.?step|改造/i.test(message)) return 'multistep'
    if (/后台|background|异步/i.test(message)) return 'background_agent'
    if (/读取|读文件|read.*file|看看.*文件/i.test(message)) return 'tool_read'
    if (/写入|写文件|write.*file|保存.*文件/i.test(message)) return 'tool_write'
    if (/运行|执行|命令|bash|ls |cat /i.test(message)) return 'tool_bash'
    if (/分析|架构|复杂|规划|orchestrat/i.test(message)) return 'subagent'
    if (/任务|计划|task|plan/i.test(message)) return 'task'
    if (/错误|失败|error|fail/i.test(message)) return 'error'
    return 'text'
  }

  /* ── 运行模拟入口 ──────────────────────────────── */

  private async simulateRun(
    runId: string,
    userMessage: string,
    sessionId: string,
    compactedBefore = false,
  ): Promise<string | null> {
    // 返回 null 表示 run 被中止（对齐后端：ctx 取消时 RPC 以 context canceled 结束）

    // 自动压缩事件（在 run.started 之前发射）
    if (compactedBefore) {
      await this.delay(100)
      this.emit({ type: 'context.compacted', run_id: runId, session_id: sessionId, original_tokens: 5000, summary_tokens: 200 })
      await this.delay(100)
    }

    const scenario = this.detectScenario(userMessage)

    // Skill 触发
    if (scenario.startsWith('skill:')) {
      const skillName = scenario.split(':')[1]
      if (skillName !== 'unknown') {
        await this.delay(100)
        this.emit({ type: 'skill.invoked', skill_name: skillName, arguments: userMessage.slice(skillName.length + 2), run_id: runId })
        await this.delay(100)
      }
    }

    // run.started
    await this.delay(150)
    if (!this.activeRuns.has(runId)) return null
    this.emit({ type: 'run.started', run_id: runId, goal: userMessage })

    let result: string
    let blocks: ContentBlock[]

    switch (scenario) {
      case 'tool_read':
        [result, blocks] = await this.simToolRead(runId, userMessage)
        break
      case 'tool_bash':
        [result, blocks] = await this.simToolBash(runId, userMessage, sessionId)
        break
      case 'tool_write':
        [result, blocks] = await this.simToolWrite(runId, userMessage, sessionId)
        break
      case 'subagent':
        [result, blocks] = await this.simSubagent(runId, userMessage)
        break
      case 'skill:orchestrate':
        [result, blocks] = await this.simOrchestrate(runId, userMessage)
        break
      case 'multistep':
        [result, blocks] = await this.simMultiStep(runId, userMessage, sessionId)
        break
      case 'background_agent':
        [result, blocks] = await this.simBackgroundAgent(runId, userMessage)
        break
      case 'task':
        [result, blocks] = await this.simTask(runId, userMessage)
        break
      case 'error':
        [result, blocks] = await this.simError(runId, userMessage)
        break
      case 'skill:init':
        [result, blocks] = await this.simSkillInit(runId, userMessage, sessionId)
        break
      case 'skill:review':
        [result, blocks] = await this.simSkillReview(runId, userMessage)
        break
      case 'skill:summarize':
        [result, blocks] = await this.simSkillSummarize(runId, userMessage)
        break
      default:
        [result, blocks] = await this.simText(runId, userMessage)
    }

    // run.finished
    await this.delay(100)
    if (!this.activeRuns.has(runId)) {
      // 被中止：后端仍会发布 run.finished（ctx 取消 → loop 报错 → Status=failed、reason=cancelled），
      // 随后 RPC 才以 context canceled 结束。此时没有最终答复，但 run_id 与会话状态照常写回。
      this.emitRunFinished(runId, 'failed', '', 'cancelled')
      this.recordRunFinished(sessionId, runId)
      return null
    }
    // 后端 ExecContext.Status 取值为 running/success/failed（见 internal/context/context.go）
    this.emitRunFinished(runId, 'success', result)
    this.activeRuns.delete(runId)
    this.recordRunFinished(sessionId, runId, blocks)

    return result
  }

  // recordRunFinished 把一次 run 的结果写回会话，对齐后端 handler.go：
  // 无论成功、失败还是被中止，都要追加 run_id、刷新状态；one_shot 结束后关闭并发 session.closed。
  private recordRunFinished(sessionId: string, runId: string, blocks?: ContentBlock[]): void {
    const ms = this.sessions.get(sessionId)
    if (!ms) return
    if (blocks) ms.messages.push({ role: 'assistant', content: blocks })
    ms.session.run_ids.push(runId)
    ms.session.updated_at = new Date().toISOString()
    ms.session.status = ms.session.mode === 'one_shot' ? 'closed' : 'waiting_for_input'
    if (ms.session.status === 'closed') {
      this.emit({ type: 'session.closed', session_id: ms.session.id })
    }
  }

  /* ── 场景：纯文本对话 ──────────────────────────── */

  private async simText(runId: string, userMessage: string): Promise<[string, ContentBlock[]]> {
    const response = this.generateTextResponse(userMessage)
    this.emitLLMRequest(runId, 'You are Numbat, a helpful AI assistant.', [
      { role: 'user', content: [{ type: 'text', text: userMessage }] },
    ])
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    this.emitUsage(runId, userMessage.length, response.length)
    return [response, [{ type: 'text', text: response }]]
  }

  /* ── 场景：read_file 工具调用 ──────────────────── */

  private async simToolRead(runId: string, _userMessage: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const toolUseId = this.genToolUseId()

    // 思考文本
    const thinking = '让我读取文件内容。'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // tool.call_started
    await this.delay(200)
    this.emit({
      type: 'tool.call_started',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: 'read_file',
      params: { path: 'main.go' },
    })
    blocks.push({ type: 'tool_use', id: toolUseId, name: 'read_file', input: { path: 'main.go' } })

    // tool.call_finished
    await this.delay(500)
    this.emit({
      type: 'tool.call_finished',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: 'read_file',
      output: MOCK_FILE_CONTENT,
      elapsed_ms: 480,
    })
    blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: MOCK_FILE_CONTENT })

    // 响应文本
    const response = '文件内容如下：\n\n```go\n' + MOCK_FILE_CONTENT + '\n```\n\n这是一个简单的 Go main 包，导入了 fmt 并在 main 函数中打印 "Hello, Numbat"。'
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })

    this.emitUsage(runId, 200, response.length)
    return [response, blocks]
  }

  /* ── 场景：bash 命令（含权限审批） ─────────────── */

  private async simToolBash(runId: string, _userMessage: string, sessionId: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const toolUseId = this.genToolUseId()
    const command = 'ls -la'

    // 思考文本
    const thinking = '让我执行命令来查看目录内容。'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // tool.call_started (bash)
    await this.delay(200)
    this.emit({
      type: 'tool.call_started',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: 'bash',
      params: { command },
    })
    blocks.push({ type: 'tool_use', id: toolUseId, name: 'bash', input: { command } })

    // 权限审批（requestPermission 自动检查持久化策略）
    const allowed = await this.requestPermission(runId, toolUseId, 'bash', { command }, command, sessionId)

    if (allowed) {
      // 权限通过
      await this.delay(300)
      this.emit({
        type: 'tool.call_finished',
        run_id: runId,
        tool_use_id: toolUseId,
        tool_name: 'bash',
        output: MOCK_BASH_OUTPUT,
        elapsed_ms: 320,
      })
      blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: MOCK_BASH_OUTPUT })

      const response = '目录中有以下文件：\n- main.go\n- go.mod\n- go.sum\n- internal/\n- cmd/\n- docs/\n- webui/'
      await this.emitTokens(runId, response)
      this.emit({ type: 'llm.response', run_id: runId, text: response })
      blocks.push({ type: 'text', text: response })
      this.emitUsage(runId, 200, response.length)
      return [response, blocks]
    } else {
      // 权限拒绝
      await this.delay(200)
      this.emit({
        type: 'tool.call_failed',
        run_id: runId,
        tool_use_id: toolUseId,
        tool_name: 'bash',
        error: '用户拒绝执行该命令',
        error_type: 'permission_denied',
        elapsed_ms: 0,
        attempt: 1,
      })
      blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: '用户拒绝执行该命令', is_error: true })

      const response = '好的，我没有执行该命令。如果你需要查看目录内容，我可以尝试用其他方式。'
      await this.emitTokens(runId, response)
      this.emit({ type: 'llm.response', run_id: runId, text: response })
      blocks.push({ type: 'text', text: response })
      this.emitUsage(runId, 200, response.length)
      return [response, blocks]
    }
  }

  /* ── 场景：write_file（含权限审批） ────────────── */

  private async simToolWrite(runId: string, _userMessage: string, sessionId: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const toolUseId = this.genToolUseId()
    const filePath = 'output.txt'
    const fileContent = 'Numbat WebUI 测试输出\n生成时间: ' + new Date().toISOString()

    const thinking = '我来帮你创建文件。'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // tool.call_started (write_file)
    await this.delay(200)
    this.emit({
      type: 'tool.call_started',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: 'write_file',
      params: { path: filePath, content: fileContent },
    })
    blocks.push({ type: 'tool_use', id: toolUseId, name: 'write_file', input: { path: filePath, content: fileContent } })

    // 权限审批（write_file 默认需要审批）
    const allowed = await this.requestPermission(
      runId, toolUseId, 'write_file',
      { path: filePath, content: fileContent },
      `写入 ${filePath}`,
      sessionId,
    )

    if (allowed) {
      await this.delay(300)
      this.emit({
        type: 'tool.call_finished',
        run_id: runId,
        tool_use_id: toolUseId,
        tool_name: 'write_file',
        output: `已写入 ${filePath} (${fileContent.length} 字节)`,
        elapsed_ms: 150,
      })
      blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: `已写入 ${filePath}` })

      const response = `文件已成功写入：${filePath}，共 ${fileContent.length} 字节。`
      await this.emitTokens(runId, response)
      this.emit({ type: 'llm.response', run_id: runId, text: response })
      blocks.push({ type: 'text', text: response })
      this.emitUsage(runId, 200, response.length)
      return [response, blocks]
    } else {
      await this.delay(200)
      this.emit({
        type: 'tool.call_failed',
        run_id: runId,
        tool_use_id: toolUseId,
        tool_name: 'write_file',
        error: '用户拒绝写入文件',
        error_type: 'permission_denied',
        elapsed_ms: 0,
        attempt: 1,
      })
      blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: '用户拒绝写入文件', is_error: true })

      const response = '好的，我没有写入文件。'
      await this.emitTokens(runId, response)
      this.emit({ type: 'llm.response', run_id: runId, text: response })
      blocks.push({ type: 'text', text: response })
      this.emitUsage(runId, 200, response.length)
      return [response, blocks]
    }
  }

  /* ── 场景：子 Agent（spawn_agent） ─────────────── */

  private async simSubagent(runId: string, _userMessage: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const toolUseId = this.genToolUseId()
    const subRunId = `mock-subrun-${String(this.subRunCounter++).padStart(3, '0')}`

    const thinking = '这个任务比较复杂，我将启动子 Agent 来分析项目架构。'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // tool.call_started (spawn_agent)
    await this.delay(200)
    this.emit({
      type: 'tool.call_started',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: 'spawn_agent',
      params: { description: '分析项目架构', prompt: '分析项目目录结构和模块依赖', subagent_type: 'planner' },
    })
    blocks.push({ type: 'tool_use', id: toolUseId, name: 'spawn_agent', input: { description: '分析项目架构', prompt: '分析项目目录结构' } })

    // subagent.started
    await this.delay(200)
    this.emit({
      type: 'subagent.started',
      run_id: subRunId,
      parent_run_id: runId,
      description: '分析项目架构',
    })

    // 子 Agent 的事件流（嵌套 run）
    await this.delay(200)
    this.emit({ type: 'run.started', run_id: subRunId, goal: '分析项目目录结构' })

    // 子 Agent 的工具调用（list_dir）
    const subToolId = this.genToolUseId()
    await this.delay(300)
    this.emit({
      type: 'tool.call_started',
      run_id: subRunId,
      tool_use_id: subToolId,
      tool_name: 'list_dir',
      params: { path: '.' },
    })
    await this.delay(400)
    this.emit({
      type: 'tool.call_finished',
      run_id: subRunId,
      tool_use_id: subToolId,
      tool_name: 'list_dir',
      output: MOCK_DIR_LISTING,
      elapsed_ms: 380,
    })

    // 子 Agent 的文本响应
    const subResponse = MOCK_SUBAGENT_RESULT
    await this.emitTokens(subRunId, subResponse)
    this.emit({ type: 'llm.response', run_id: subRunId, text: subResponse })
    this.emitUsage(subRunId, 100, subResponse.length)

    // 子 Agent run.finished
    await this.delay(100)
    this.emitRunFinished(subRunId, 'success', subResponse)

    // subagent.finished
    await this.delay(100)
    this.emit({
      type: 'subagent.finished',
      run_id: subRunId,
      parent_run_id: runId,
      status: 'success',
    })

    // spawn_agent tool.call_finished
    await this.delay(200)
    this.emit({
      type: 'tool.call_finished',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: 'spawn_agent',
      output: subResponse,
      elapsed_ms: 1800,
    })
    blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: subResponse })

    // 父 Agent 响应
    const response = `子 Agent 已完成项目架构分析：\n\n${subResponse}\n\n建议后续可以深入查看各模块的实现。`
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 300, response.length)

    return [response, blocks]
  }

  /* ── 场景：/orchestrate 三阶段 multi-agent ──────── */

  private async simOrchestratePhase(
    parentRunId: string,
    profile: string,
    description: string,
    tools: { tool: string; params: Record<string, unknown>; output: string; elapsed: number }[],
    summary: string,
  ): Promise<ContentBlock[]> {
    const blocks: ContentBlock[] = []
    const toolUseId = this.genToolUseId()
    const subRunId = `mock-subrun-${String(this.subRunCounter++).padStart(3, '0')}`

    // spawn_agent (parent run)
    await this.delay(200)
    this.emit({
      type: 'tool.call_started', run_id: parentRunId, tool_use_id: toolUseId, tool_name: 'spawn_agent',
      params: { description, subagent_type: profile, prompt: `${profile} phase` },
    })
    blocks.push({ type: 'tool_use', id: toolUseId, name: 'spawn_agent', input: { description, subagent_type: profile } })

    // subagent.started
    await this.delay(200)
    this.emit({ type: 'subagent.started', run_id: subRunId, parent_run_id: parentRunId, description })

    // sub-agent run
    await this.delay(100)
    this.emit({ type: 'run.started', run_id: subRunId, goal: description })

    // sub-agent tools (仅非权限工具，避免子 run 权限对话框问题)
    for (const t of tools) {
      const subToolId = this.genToolUseId()
      await this.delay(200)
      this.emit({ type: 'tool.call_started', run_id: subRunId, tool_use_id: subToolId, tool_name: t.tool, params: t.params })
      await this.delay(t.elapsed)
      this.emit({ type: 'tool.call_finished', run_id: subRunId, tool_use_id: subToolId, tool_name: t.tool, output: t.output, elapsed_ms: t.elapsed })
    }

    // sub-agent response
    this.emitLLMRequest(subRunId, `You are a ${profile} agent.`, [])
    await this.emitTokens(subRunId, summary)
    this.emit({ type: 'llm.response', run_id: subRunId, text: summary })
    this.emitUsage(subRunId, 100, summary.length)

    await this.delay(100)
    this.emitRunFinished(subRunId, 'success', summary)

    await this.delay(100)
    this.emit({ type: 'subagent.finished', run_id: subRunId, parent_run_id: parentRunId, status: 'success' })

    // spawn_agent tool.call_finished (parent run)
    await this.delay(200)
    this.emit({ type: 'tool.call_finished', run_id: parentRunId, tool_use_id: toolUseId, tool_name: 'spawn_agent', output: summary, elapsed_ms: 1800 })
    blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: summary })

    return blocks
  }

  private async simOrchestrate(runId: string, _userMessage: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []

    const thinking = '我将使用 planner→executor→reviewer 三阶段 multi-agent 工作流来完成这个复杂任务。'
    this.emitLLMRequest(runId, 'You are Numbat orchestrator.', [])
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // Phase 1: Planner
    const p1 = await this.simOrchestratePhase(runId, 'planner', '规划任务步骤', [
      { tool: 'list_dir', params: { path: '.' }, output: MOCK_DIR_LISTING, elapsed: 380 },
      { tool: 'task_create', params: { subject: '分析需求' }, output: '{"id":1,"subject":"分析需求","status":"pending"}', elapsed: 120 },
      { tool: 'task_create', params: { subject: '实现功能' }, output: '{"id":2,"subject":"实现功能","status":"pending","blocked_by":[1]}', elapsed: 100 },
      { tool: 'task_create', params: { subject: '编写测试' }, output: '{"id":3,"subject":"编写测试","status":"pending","blocked_by":[2]}', elapsed: 100 },
    ], '规划完成：3 个任务已创建，按依赖顺序排列。')
    blocks.push(...p1)

    // Phase 2: Executor
    const p2 = await this.simOrchestratePhase(runId, 'executor', '执行实现阶段', [
      { tool: 'read_file', params: { path: 'main.go' }, output: MOCK_FILE_CONTENT, elapsed: 480 },
      { tool: 'task_update', params: { task_id: 1, status: 'in_progress' }, output: '{"id":1,"status":"in_progress"}', elapsed: 100 },
      { tool: 'task_update', params: { task_id: 1, status: 'completed' }, output: '{"id":1,"status":"completed"}', elapsed: 100 },
    ], '执行完成：代码已分析，任务 1 标记为完成。')
    blocks.push(...p2)

    // Phase 3: Reviewer
    const p3 = await this.simOrchestratePhase(runId, 'reviewer', '审查验证阶段', [
      { tool: 'read_file', params: { path: 'main.go' }, output: MOCK_FILE_CONTENT, elapsed: 480 },
      { tool: 'list_dir', params: { path: '.' }, output: MOCK_DIR_LISTING, elapsed: 380 },
    ], '审查完成：代码质量良好，无严重问题。1 个建议：添加错误处理。')
    blocks.push(...p3)

    // 总结
    const response = `## 三阶段 Multi-agent 工作流完成

### Phase 1 — Planner (规划)
- 分析项目结构，创建 3 个有序任务

### Phase 2 — Executor (执行)
- 读取代码，更新任务状态

### Phase 3 — Reviewer (审查)
- 审查代码质量，给出改进建议

所有阶段已完成，任务全流程结束。`
    this.emitLLMRequest(runId, 'You are Numbat orchestrator. Summarize the results.', [])
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 500, response.length)

    return [response, blocks]
  }

  /* ── 场景：多步 ReAct 循环 ─────────────────────── */

  private async simMultiStep(runId: string, userMessage: string, sessionId: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []

    // ── Round 1: 读取文件 ──
    this.emitLLMRequest(runId, 'You are Numbat. Analyze the request and use tools as needed.', [
      { role: 'user', content: [{ type: 'text', text: userMessage }] },
    ])
    const think1 = '让我先读取相关文件来理解当前代码。'
    await this.emitTokens(runId, think1)
    this.emit({ type: 'llm.response', run_id: runId, text: think1 })
    blocks.push({ type: 'text', text: think1 })

    const readToolId = this.genToolUseId()
    await this.delay(200)
    this.emit({ type: 'tool.call_started', run_id: runId, tool_use_id: readToolId, tool_name: 'read_file', params: { path: 'main.go' } })
    blocks.push({ type: 'tool_use', id: readToolId, name: 'read_file', input: { path: 'main.go' } })
    await this.delay(500)
    this.emit({ type: 'tool.call_finished', run_id: runId, tool_use_id: readToolId, tool_name: 'read_file', output: MOCK_FILE_CONTENT, elapsed_ms: 480 })
    blocks.push({ type: 'tool_result', tool_use_id: readToolId, content: MOCK_FILE_CONTENT })

    // ── Round 2: 执行命令（需权限） ──
    this.emitLLMRequest(runId, 'You are Numbat. Continue based on tool results.', [
      { role: 'assistant', content: [{ type: 'text', text: think1 }] },
      { role: 'user', content: [{ type: 'tool_result', tool_use_id: readToolId, content: MOCK_FILE_CONTENT }] },
    ])
    const think2 = '代码结构清晰。让我运行测试确认当前状态。'
    await this.emitTokens(runId, think2)
    this.emit({ type: 'llm.response', run_id: runId, text: think2 })
    blocks.push({ type: 'text', text: think2 })

    const bashToolId = this.genToolUseId()
    await this.delay(200)
    this.emit({ type: 'tool.call_started', run_id: runId, tool_use_id: bashToolId, tool_name: 'bash', params: { command: 'go test ./...' } })
    blocks.push({ type: 'tool_use', id: bashToolId, name: 'bash', input: { command: 'go test ./...' } })

    const bashAllowed = await this.requestPermission(runId, bashToolId, 'bash', { command: 'go test ./...' }, 'go test ./...', sessionId)
    if (bashAllowed) {
      await this.delay(300)
      this.emit({ type: 'tool.call_finished', run_id: runId, tool_use_id: bashToolId, tool_name: 'bash', output: 'ok  example.com/pkg  0.003s', elapsed_ms: 320 })
      blocks.push({ type: 'tool_result', tool_use_id: bashToolId, content: 'ok  example.com/pkg  0.003s' })
    } else {
      await this.delay(200)
      this.emit({ type: 'tool.call_failed', run_id: runId, tool_use_id: bashToolId, tool_name: 'bash', error: '用户拒绝', error_type: 'permission_denied', elapsed_ms: 0, attempt: 1 })
      blocks.push({ type: 'tool_result', tool_use_id: bashToolId, content: '用户拒绝', is_error: true })
    }

    // ── Round 3: 写入文件（需权限） ──
    this.emitLLMRequest(runId, 'You are Numbat. Continue based on tool results.', [
      { role: 'assistant', content: [{ type: 'text', text: think2 }] },
      { role: 'user', content: [{ type: 'tool_result', tool_use_id: bashToolId, content: 'ok' }] },
    ])
    const think3 = '测试通过。现在让我写入重构后的代码。'
    await this.emitTokens(runId, think3)
    this.emit({ type: 'llm.response', run_id: runId, text: think3 })
    blocks.push({ type: 'text', text: think3 })

    const writeToolId = this.genToolUseId()
    const newCode = 'package main\n\nimport "log"\n\nfunc main() {\n\tlog.Println("Hello, Numbat")\n}'
    await this.delay(200)
    this.emit({ type: 'tool.call_started', run_id: runId, tool_use_id: writeToolId, tool_name: 'write_file', params: { path: 'main.go', content: newCode } })
    blocks.push({ type: 'tool_use', id: writeToolId, name: 'write_file', input: { path: 'main.go', content: newCode } })

    const writeAllowed = await this.requestPermission(runId, writeToolId, 'write_file', { path: 'main.go', content: newCode }, '写入 main.go', sessionId)
    if (writeAllowed) {
      await this.delay(300)
      this.emit({ type: 'tool.call_finished', run_id: runId, tool_use_id: writeToolId, tool_name: 'write_file', output: '已写入 main.go (90 字节)', elapsed_ms: 150 })
      blocks.push({ type: 'tool_result', tool_use_id: writeToolId, content: '已写入 main.go' })
    } else {
      await this.delay(200)
      this.emit({ type: 'tool.call_failed', run_id: runId, tool_use_id: writeToolId, tool_name: 'write_file', error: '用户拒绝', error_type: 'permission_denied', elapsed_ms: 0, attempt: 1 })
      blocks.push({ type: 'tool_result', tool_use_id: writeToolId, content: '用户拒绝', is_error: true })
    }

    // ── 最终响应 ──
    this.emitLLMRequest(runId, 'You are Numbat. Summarize what was done.', [])
    const response = '重构完成！\n\n1. 读取了 main.go\n2. 运行了 `go test` — 测试通过\n3. 重写了 main.go，用 `log.Println` 替代 `fmt.Println`\n\n建议运行 `go build` 确认编译无误。'
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 400, response.length)

    return [response, blocks]
  }

  /* ── 场景：后台子 Agent + agent_result 轮询 ─────── */

  private async simBackgroundAgent(runId: string, _userMessage: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const spawnToolId = this.genToolUseId()
    const subRunId = `mock-subrun-${String(this.subRunCounter++).padStart(3, '0')}`

    const thinking = '我将启动一个后台子 Agent 来处理这个任务，同时可以继续其他工作。'
    this.emitLLMRequest(runId, 'You are Numbat.', [])
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // spawn_agent (run_in_background=true)
    await this.delay(200)
    this.emit({
      type: 'tool.call_started', run_id: runId, tool_use_id: spawnToolId, tool_name: 'spawn_agent',
      params: { description: '后台分析任务', subagent_type: 'planner', run_in_background: true },
    })
    blocks.push({ type: 'tool_use', id: spawnToolId, name: 'spawn_agent', input: { description: '后台分析任务', run_in_background: true } })

    await this.delay(200)
    this.emit({
      type: 'tool.call_finished', run_id: runId, tool_use_id: spawnToolId, tool_name: 'spawn_agent',
      output: `子 Agent 已在后台启动 (run_id: ${subRunId})`, elapsed_ms: 200,
    })
    blocks.push({ type: 'tool_result', tool_use_id: spawnToolId, content: `子 Agent 已在后台启动 (run_id: ${subRunId})` })

    // 第一次轮询 agent_result
    const poll1ToolId = this.genToolUseId()
    await this.delay(300)
    this.emit({
      type: 'tool.call_started', run_id: runId, tool_use_id: poll1ToolId, tool_name: 'agent_result',
      params: { run_id: subRunId },
    })
    blocks.push({ type: 'tool_use', id: poll1ToolId, name: 'agent_result', input: { run_id: subRunId } })
    await this.delay(200)
    this.emit({
      type: 'tool.call_finished', run_id: runId, tool_use_id: poll1ToolId, tool_name: 'agent_result',
      output: 'still running', elapsed_ms: 100,
    })
    blocks.push({ type: 'tool_result', tool_use_id: poll1ToolId, content: 'still running' })

    // 子 Agent 在后台工作（发射事件）
    await this.delay(200)
    this.emit({ type: 'subagent.started', run_id: subRunId, parent_run_id: runId, description: '后台分析任务' })
    await this.delay(100)
    this.emit({ type: 'run.started', run_id: subRunId, goal: '后台分析任务' })

    const subToolId = this.genToolUseId()
    await this.delay(200)
    this.emit({ type: 'tool.call_started', run_id: subRunId, tool_use_id: subToolId, tool_name: 'list_dir', params: { path: '.' } })
    await this.delay(400)
    this.emit({ type: 'tool.call_finished', run_id: subRunId, tool_use_id: subToolId, tool_name: 'list_dir', output: MOCK_DIR_LISTING, elapsed_ms: 380 })

    const subResponse = MOCK_SUBAGENT_RESULT
    this.emitLLMRequest(subRunId, 'You are a planner agent.', [])
    await this.emitTokens(subRunId, subResponse)
    this.emit({ type: 'llm.response', run_id: subRunId, text: subResponse })
    this.emitUsage(subRunId, 100, subResponse.length)

    await this.delay(100)
    this.emitRunFinished(subRunId, 'success', subResponse)
    await this.delay(100)
    this.emit({ type: 'subagent.finished', run_id: subRunId, parent_run_id: runId, status: 'success' })

    // 第二次轮询 agent_result — 获取结果
    const poll2ToolId = this.genToolUseId()
    await this.delay(300)
    this.emit({
      type: 'tool.call_started', run_id: runId, tool_use_id: poll2ToolId, tool_name: 'agent_result',
      params: { run_id: subRunId },
    })
    blocks.push({ type: 'tool_use', id: poll2ToolId, name: 'agent_result', input: { run_id: subRunId } })
    await this.delay(200)
    this.emit({
      type: 'tool.call_finished', run_id: runId, tool_use_id: poll2ToolId, tool_name: 'agent_result',
      output: subResponse, elapsed_ms: 100,
    })
    blocks.push({ type: 'tool_result', tool_use_id: poll2ToolId, content: subResponse })

    // 最终响应
    this.emitLLMRequest(runId, 'You are Numbat. Summarize the background agent results.', [])
    const response = `后台子 Agent 已完成分析：\n\n${subResponse}\n\n通过 \`agent_result\` 工具轮询获取到了最终结果。`
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 300, response.length)

    return [response, blocks]
  }

  /* ── 场景：任务/计划（task_create） ────────────── */

  private async simTask(runId: string, _userMessage: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []

    const thinking = '让我来规划任务，分解这个复杂目标。'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // 创建 3 个任务
    const taskSubjects = ['分析需求', '实现核心功能', '编写测试']
    for (const subject of taskSubjects) {
      const toolUseId = this.genToolUseId()
      await this.delay(200)
      this.emit({
        type: 'tool.call_started',
        run_id: runId,
        tool_use_id: toolUseId,
        tool_name: 'task_create',
        params: { subject },
      })
      blocks.push({ type: 'tool_use', id: toolUseId, name: 'task_create', input: { subject } })

      await this.delay(300)
      const task: Task = {
        id: this.taskCounter++,
        subject,
        description: `${subject}的详细描述`,
        status: 'pending',
        blocked_by: [],
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      }
      this.tasks.push(task)
      this.emit({
        type: 'tool.call_finished',
        run_id: runId,
        tool_use_id: toolUseId,
        tool_name: 'task_create',
        output: JSON.stringify(task, null, 2),
        elapsed_ms: 120,
      })
      blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: JSON.stringify(task, null, 2) })
    }

    // 更新第一个任务为 in_progress
    const updateToolId = this.genToolUseId()
    await this.delay(200)
    this.emit({
      type: 'tool.call_started',
      run_id: runId,
      tool_use_id: updateToolId,
      tool_name: 'task_update',
      params: { task_id: 1, status: 'in_progress' },
    })
    blocks.push({ type: 'tool_use', id: updateToolId, name: 'task_update', input: { task_id: 1, status: 'in_progress' } })

    await this.delay(300)
    const updatedTask = { ...this.tasks[0], status: 'in_progress' as const, updated_at: new Date().toISOString() }
    this.tasks[0] = updatedTask
    this.emit({
      type: 'tool.call_finished',
      run_id: runId,
      tool_use_id: updateToolId,
      tool_name: 'task_update',
      output: JSON.stringify(updatedTask, null, 2),
      elapsed_ms: 100,
    })
    blocks.push({ type: 'tool_result', tool_use_id: updateToolId, content: JSON.stringify(updatedTask, null, 2) })

    const response = `已创建 ${taskSubjects.length} 个任务并开始执行第 1 个：\n\n1. [进行中] 分析需求\n2. [待办] 实现核心功能\n3. [待办] 编写测试`
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 200, response.length)

    return [response, blocks]
  }

  /* ── 场景：错误（tool.call_failed） ─────────────── */

  private async simError(runId: string, _userMessage: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const toolUseId = this.genToolUseId()

    const thinking = '让我尝试执行这个操作。'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    await this.delay(200)
    this.emit({
      type: 'tool.call_started',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: 'bash',
      params: { command: 'nonexistent-command' },
    })
    blocks.push({ type: 'tool_use', id: toolUseId, name: 'bash', input: { command: 'nonexistent-command' } })

    await this.delay(500)
    this.emit({
      type: 'tool.call_failed',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: 'bash',
      error: 'bash: nonexistent-command: command not found',
      error_type: 'runtime_error',
      elapsed_ms: 320,
      attempt: 1,
    })
    blocks.push({ type: 'tool_result', tool_use_id: toolUseId, content: 'bash: nonexistent-command: command not found', is_error: true })

    const response = '命令执行失败：`nonexistent-command` 不存在。\n\n请确认命令名称是否正确，或安装对应的工具。'
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 200, response.length)

    return [response, blocks]
  }

  /* ── 场景：Skill /init ─────────────────────────── */

  private async simSkillInit(runId: string, _userMessage: string, sessionId: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const listToolId = this.genToolUseId()

    const thinking = '开始分析项目结构，生成 context.md。'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // list_dir
    await this.delay(200)
    this.emit({
      type: 'tool.call_started', run_id: runId, tool_use_id: listToolId, tool_name: 'list_dir', params: { path: '.' },
    })
    blocks.push({ type: 'tool_use', id: listToolId, name: 'list_dir', input: { path: '.' } })
    await this.delay(400)
    this.emit({
      type: 'tool.call_finished', run_id: runId, tool_use_id: listToolId, tool_name: 'list_dir', output: MOCK_DIR_LISTING, elapsed_ms: 380,
    })
    blocks.push({ type: 'tool_result', tool_use_id: listToolId, content: MOCK_DIR_LISTING })

    // write_file (with permission)
    const writeToolId = this.genToolUseId()
    const contextContent = '# Project Context\n\nNumbat: Go 后端 + React 前端的 AI Agent 系统。'
    await this.delay(200)
    this.emit({
      type: 'tool.call_started', run_id: runId, tool_use_id: writeToolId, tool_name: 'write_file',
      params: { path: '.numbat/context.md', content: contextContent },
    })
    blocks.push({ type: 'tool_use', id: writeToolId, name: 'write_file', input: { path: '.numbat/context.md', content: contextContent } })

    const allowed = await this.requestPermission(runId, writeToolId, 'write_file', { path: '.numbat/context.md' }, '写入 .numbat/context.md', sessionId)

    if (allowed) {
      await this.delay(300)
      this.emit({
        type: 'tool.call_finished', run_id: runId, tool_use_id: writeToolId, tool_name: 'write_file',
        output: '已写入 .numbat/context.md', elapsed_ms: 150,
      })
      blocks.push({ type: 'tool_result', tool_use_id: writeToolId, content: '已写入 .numbat/context.md' })
    } else {
      blocks.push({ type: 'tool_result', tool_use_id: writeToolId, content: '用户拒绝', is_error: true })
    }

    const response = '项目分析完成，已生成 .numbat/context.md 文件。'
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 300, response.length)

    return [response, blocks]
  }

  /* ── 场景：Skill /review ────────────────────────── */

  private async simSkillReview(runId: string, _userMessage: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const readToolId = this.genToolUseId()

    const thinking = '开始对指定路径进行代码审查。'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // read_file
    await this.delay(200)
    this.emit({
      type: 'tool.call_started', run_id: runId, tool_use_id: readToolId, tool_name: 'read_file', params: { path: 'main.go' },
    })
    blocks.push({ type: 'tool_use', id: readToolId, name: 'read_file', input: { path: 'main.go' } })
    await this.delay(500)
    this.emit({
      type: 'tool.call_finished', run_id: runId, tool_use_id: readToolId, tool_name: 'read_file', output: MOCK_FILE_CONTENT, elapsed_ms: 480,
    })
    blocks.push({ type: 'tool_result', tool_use_id: readToolId, content: MOCK_FILE_CONTENT })

    const response = MOCK_REVIEW_RESULT
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 200, response.length)

    return [response, blocks]
  }

  /* ── 场景：Skill /summarize ─────────────────────── */

  private async simSkillSummarize(runId: string, _userMessage: string): Promise<[string, ContentBlock[]]> {
    const blocks: ContentBlock[] = []
    const noteToolId = this.genToolUseId()

    const thinking = '正在生成本次对话的摘要…'
    await this.emitTokens(runId, thinking)
    this.emit({ type: 'llm.response', run_id: runId, text: thinking })
    blocks.push({ type: 'text', text: thinking })

    // note_save
    await this.delay(200)
    const noteContent = '对话摘要：用户与 Numbat 进行了多轮对话，讨论了项目开发相关话题。'
    this.emit({
      type: 'tool.call_started', run_id: runId, tool_use_id: noteToolId, tool_name: 'note_save', params: { content: noteContent },
    })
    blocks.push({ type: 'tool_use', id: noteToolId, name: 'note_save', input: { content: noteContent } })
    await this.delay(300)
    this.emit({
      type: 'tool.call_finished', run_id: runId, tool_use_id: noteToolId, tool_name: 'note_save', output: '笔记已保存', elapsed_ms: 120,
    })
    blocks.push({ type: 'tool_result', tool_use_id: noteToolId, content: '笔记已保存' })

    const response = '对话摘要已生成并保存为会话笔记。'
    await this.emitTokens(runId, response)
    this.emit({ type: 'llm.response', run_id: runId, text: response })
    blocks.push({ type: 'text', text: response })
    this.emitUsage(runId, 200, response.length)

    return [response, blocks]
  }

  /* ── 辅助方法 ──────────────────────────────────── */

  private generateTextResponse(userMessage: string): string {
    const msg = userMessage.trim()
    if (/你好|hello|hi|嗨/i.test(msg)) {
      return '你好！我是 Numbat，很高兴为你服务。有什么我能帮助你的吗？'
    }
    if (/你是谁|who are you|介绍/i.test(msg)) {
      return '我是 Numbat，一个由 Go 后端驱动的 AI 助手。\n\n当前你看到的是 Mock 模式下的模拟响应——联调后这里会替换为真实的 LLM 回复。'
    }
    if (/工具|tool/i.test(msg)) {
      return '在真实后端中，我可以通过工具调用来执行操作（如读文件、运行命令等）。\n\nMock 模式下不模拟工具调用，但 UI 已经支持渲染 tool_use / tool_result 内容块。\n\n试试输入「读取文件」或「运行命令」来体验工具调用场景。'
    }
    if (/天气|weather/i.test(msg)) {
      return '抱歉，我目前是 Mock 模式，无法查询实时天气。\n\n联调后可以接入真实工具（tool.call_started → tool.call_finished 事件）。'
    }
    return `我收到了你的消息："${userMessage}"\n\n这是一个 Mock 响应。试试这些关键词触发不同场景：\n- 「读取文件」→ read_file 工具调用\n- 「运行命令」→ bash + 权限审批\n- 「写入文件」→ write_file + 权限审批\n- 「分析架构」→ 子 Agent\n- 「重构代码」→ 多步 ReAct 循环\n- 「后台分析」→ 后台子 Agent + 轮询\n- 「任务计划」→ Task 创建\n- 「错误测试」→ 工具失败\n- /orchestrate → 三阶段 multi-agent\n- /review → Skill 代码审查\n- /init → Skill 项目初始化\n- /summarize → Skill 对话摘要`
  }

  /** 流式发射 token 事件 */
  private async emitTokens(runId: string, text: string): Promise<void> {
    const tokens = this.tokenize(text)
    for (const token of tokens) {
      if (!this.activeRuns.has(runId)) return
      this.emit({ type: 'llm.token', run_id: runId, token })
      await this.delay(50 + Math.random() * 40)
    }
  }

  /** 发射 usage 事件 */
  private emitUsage(runId: string, inputLen: number, outputLen: number): void {
    // 契约：context_pct 为 0.0–1.0 的小数（后端 = inputTokens / contextWindow）
    const inputTokens = Math.round(inputLen / 4) + 50
    const outputTokens = Math.round(outputLen / 4) + 30
    const contextWindow = 200000
    this.emit({
      type: 'llm.usage',
      run_id: runId,
      input_tokens: inputTokens,
      output_tokens: outputTokens,
      context_pct: Math.min(0.95, (inputTokens + outputTokens) / contextWindow),
      cache_read_input_tokens: 0,
      cache_creation_input_tokens: 0,
    })
  }

  /** 将文本拆成小片段模拟流式 token */
  private tokenize(text: string): string[] {
    const chunks: string[] = []
    let i = 0
    while (i < text.length) {
      const size = Math.min(3, text.length - i)
      chunks.push(text.slice(i, i + size))
      i += size
    }
    return chunks
  }

  /** 等待权限审批 */
  private waitForPermission(toolUseId: string, runId: string): Promise<PermissionDecision> {
    return new Promise((resolve) => {
      // 30 秒超时自动拒绝
      const timeout = setTimeout(() => {
        this.permissionResolvers.delete(toolUseId)
        this.permissionRuns.delete(toolUseId)
        resolve('deny_once')
      }, 30000)
      this.permissionRuns.set(toolUseId, runId)
      this.permissionResolvers.set(toolUseId, (decision) => {
        clearTimeout(timeout)
        this.permissionRuns.delete(toolUseId)
        resolve(decision)
      })
    })
  }

  /** 结束某个 run 下所有待审批的权限请求（对齐后端：ctx 取消使 CheckAndWait 立即返回）。 */
  private cancelPendingPermissions(runId: string): void {
    const pending = [...this.permissionRuns.entries()].filter(([, rid]) => rid === runId)
    for (const [toolUseId] of pending) {
      this.permissionResolvers.get(toolUseId)?.('deny_once')
    }
  }

  /**
   * 完整权限审批流程：检查持久化策略 → 发射 permission.requested → 等待响应 → 持久化 → 发射 granted/denied
   * 返回 true=允许, false=拒绝
   */
  private async requestPermission(
    runId: string,
    toolUseId: string,
    toolName: string,
    params: Record<string, unknown>,
    preview: string,
    sessionId = '',
  ): Promise<boolean> {
    // 检查持久化策略（模拟后端 policy.toml 缓存）
    const policy = this.permissionPolicy.get(toolName)
    if (policy === 'allow') return true
    if (policy === 'deny') return false

    // 发射 permission.requested
    this.emit({
      type: 'permission.requested',
      run_id: runId,
      tool_use_id: toolUseId,
      tool_name: toolName,
      params,
      preview,
      session_id: sessionId,
    })

    const decision = await this.waitForPermission(toolUseId, runId)

    // 持久化 always_allow / always_deny（按 tool_name 存储）
    if (decision === 'always_allow') {
      this.permissionPolicy.set(toolName, 'allow')
    } else if (decision === 'always_deny') {
      this.permissionPolicy.set(toolName, 'deny')
    }

    // 发射 granted / denied（后端只在非自动判定时发射，并带上 decision）
    if (decision.startsWith('allow')) {
      this.emit({ type: 'permission.granted', run_id: runId, tool_use_id: toolUseId, tool_name: toolName, decision })
      return true
    }
    this.emit({ type: 'permission.denied', run_id: runId, tool_use_id: toolUseId, tool_name: toolName, decision })
    return false
  }

  /** 发射 llm.request 事件（每轮 ReAct 循环开始前） */
  private emitLLMRequest(runId: string, system: string, messages: Message[]): void {
    this.emit({ type: 'llm.request', run_id: runId, messages, system })
  }

  /* ── ID 生成 ──────────────────────────────────── */

  private genRunId(): string {
    return `mock-run-${String(this.runCounter++).padStart(3, '0')}`
  }

  private genToolUseId(): string {
    return `toolu_mock_${String(this.toolUseCounter++).padStart(3, '0')}`
  }

  /* ── 事件与状态 ────────────────────────────────── */

  private delay(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms))
  }

  private emit(event: Event): void {
    for (const handler of this.eventHandlers) {
      try {
        handler(event)
      } catch {
        // 单个处理器异常不影响其他
      }
    }
  }

  /**
   * 发射 run.finished。Mock 每个场景只模拟一轮 LLM 响应，故 steps 固定为 1。
   * 契约字段 reason/steps 见 internal/events/events.go 的 RunFinished。
   */
  private emitRunFinished(runId: string, status: RunStatus, result: string, reason = ''): void {
    this.emit({ type: 'run.finished', run_id: runId, status, result, reason, steps: 1 })
  }

  private setStatus(s: ConnectionStatus): void {
    this.status = s
    for (const handler of this.statusHandlers) {
      handler(s)
    }
  }
}
