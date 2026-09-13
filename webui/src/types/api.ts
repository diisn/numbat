// 镜像 docs/webui-api-contract.md 的 TypeScript 类型，与 Go 结构体一一对应。
// 事实来源：internal/transport/handler.go / internal/events/events.go /
// internal/session/model.go / internal/llm/message.go

/* ── 传输层 envelope ──────────────────────────────── */

export interface RPCError {
  code: number
  message: string
  data?: unknown
}

/** JSON-RPC envelope：请求/响应/事件共用。 */
export interface Envelope {
  jsonrpc: '2.0'
  /** 请求/响应带 id；事件无 id。 */
  id?: string | number
  method?: string
  params?: unknown
  result?: unknown
  error?: RPCError
}

/* ── 会话 ─────────────────────────────────────────── */

export type SessionMode = 'one_shot' | 'chat'
export type SessionStatus = 'active' | 'waiting_for_input' | 'closed'

export interface Session {
  id: string
  mode: SessionMode
  status: SessionStatus
  title: string
  created_at: string
  updated_at: string
  run_ids: string[]
}

/* ── 历史消息（session.get_history → Message[]） ──── */

export interface ContentBlock {
  type: string // "text" | "tool_use" | "tool_result" | "thinking"
  text?: string
  id?: string // tool_use 块的工具调用 id
  name?: string // tool_use 块的工具名
  input?: Record<string, unknown>
  tool_use_id?: string
  content?: string // tool_result 块的内容
  is_error?: boolean
  thinking?: string
  signature?: string
}

export interface Message {
  role: 'user' | 'assistant'
  content: ContentBlock[]
}

/* ── 事件（envelope.result 即事件对象，带 type 字段） ── */

/**
 * run 终态。取值来源：external/context.ExecutionContext.Status
 * （internal/context/context.go 初始为 "running"，循环结束时置为 "success" / "failed"）。
 * 注意：后端没有 "completed" 这个取值。
 */
export type RunStatus = 'running' | 'success' | 'failed'

export interface RunStarted {
  type: 'run.started'
  run_id: string
  goal: string
}
export interface RunFinished {
  type: 'run.finished'
  run_id: string
  status: RunStatus
  result: string
  /** 失败原因：llm_error / cancelled / exceeded_max_steps；success 时为空串 */
  reason: string
  /** 本次 run 实际执行的轮数 */
  steps: number
}

/**
 * run.list 返回的单条 run 摘要 —— 由 trace 事件序列聚合而来。
 * 事实来源：internal/trace/reader.go 的 RunSummary。
 */
export interface RunSummary {
  run_id: string
  goal: string
  status: RunStatus
  started_at: string
  finished_at: string
  duration_ms: number
  skill?: string
  /** 取自该 run 最后一次 llm.usage（与实时徽章口径一致） */
  tokens: { input_tokens: number; output_tokens: number; context_pct: number }
  tools: { tool_name: string; tool_use_id: string; status: RunStatus; elapsed_ms: number }[]
  subagents: { run_id: string; description: string; status: string }[]
  events: { type: string; ts: string; detail: string }[]
}
export interface LLMRequest {
  type: 'llm.request'
  run_id: string
  messages: Message[]
  system: string
}
export interface LLMResponse {
  type: 'llm.response'
  run_id: string
  text: string
}
export interface LLMTokens {
  type: 'llm.token'
  run_id: string
  token: string
}
export interface LLMUsage {
  type: 'llm.usage'
  run_id: string
  input_tokens: number
  output_tokens: number
  /** 上下文占用比例，取值 0.0–1.0 的小数（后端 = inputTokens / contextWindow） */
  context_pct: number
  /** prompt cache 命中量；端点未返回时为 0 */
  cache_read_input_tokens: number
  /** prompt cache 写入量；端点未返回时为 0 */
  cache_creation_input_tokens: number
}
export interface ToolCallStarted {
  type: 'tool.call_started'
  run_id: string
  tool_use_id: string
  tool_name: string
  params: Record<string, unknown>
}
export interface ToolCallFinished {
  type: 'tool.call_finished'
  run_id: string
  tool_use_id: string
  tool_name: string
  output: string
  elapsed_ms: number
}
export interface ToolCallFailed {
  type: 'tool.call_failed'
  run_id: string
  tool_use_id: string
  tool_name: string
  error: string
  error_type: string
  elapsed_ms: number
  /** 第几次尝试（从 1 开始），用于观测重试 */
  attempt: number
}
export interface PermissionRequested {
  type: 'permission.requested'
  run_id: string
  tool_use_id: string
  tool_name: string
  params: Record<string, unknown>
  preview: string
  /** 所属会话 ID；无会话的 run 为空串 */
  session_id: string
}
export interface PermissionGranted {
  type: 'permission.granted'
  run_id: string
  tool_use_id: string
  tool_name: string
  /** 判定来源：allow_once / always_allow 等（auto_allow 不再发此事件） */
  decision: string
}
export interface PermissionDenied {
  type: 'permission.denied'
  run_id: string
  tool_use_id: string
  tool_name: string
  /** 判定来源：deny_once / always_deny / timeout / cancelled 等 */
  decision: string
}
export interface ContextCompacted {
  type: 'context.compacted'
  run_id: string
  /** 所属会话 ID；无会话的 run 为空串 */
  session_id: string
  original_tokens: number
  summary_tokens: number
}
export interface SessionCreated {
  type: 'session.created'
  session_id: string
  mode: string
}
export interface SessionClosed {
  type: 'session.closed'
  session_id: string
}
export interface SubagentStarted {
  type: 'subagent.started'
  run_id: string
  parent_run_id: string
  description: string
}
export interface SubagentFinished {
  type: 'subagent.finished'
  run_id: string
  parent_run_id: string
  /** 与 run.finished 同源：取子 run 的 ExecContext.Status */
  status: RunStatus
}
export interface SkillInvoked {
  type: 'skill.invoked'
  skill_name: string
  arguments: string
  run_id: string
}

export type Event =
  | RunStarted
  | RunFinished
  | LLMRequest
  | LLMResponse
  | LLMTokens
  | LLMUsage
  | ToolCallStarted
  | ToolCallFinished
  | ToolCallFailed
  | PermissionRequested
  | PermissionGranted
  | PermissionDenied
  | ContextCompacted
  | SessionCreated
  | SessionClosed
  | SubagentStarted
  | SubagentFinished
  | SkillInvoked

/* ── Task/Plan（task_create/task_list 工具输出中内嵌） ── */

export type TaskStatus = 'pending' | 'in_progress' | 'completed'

export interface Task {
  id: number
  subject: string
  description: string
  status: TaskStatus
  blocked_by: number[]
  created_at: string
  updated_at: string
}

/* ── Skill 元数据（后端 internal/skills/loader.go） ──── */

export interface SkillMeta {
  name: string
  description: string
  allowed_tools: string[]
}

/* ── Agent 角色配置（后端 internal/agents/loader.go） ── */

export interface AgentProfile {
  name: string
  description: string
  allowed_tools: string[]
  model: string
}

/* ── RPC 方法参数与结果 ───────────────────────────── */

export type PermissionDecision = 'allow_once' | 'always_allow' | 'deny_once' | 'always_deny'

export interface RPC {
  'core.ping': { params: undefined; result: 'pong' }
  'skill.list': { params: undefined; result: SkillMeta[] }
  'agent.run': { params: { goal: string }; result: { run_id: string; status: RunStatus; result: string } }
  'agent.abort': { params: { run_id: string }; result: boolean }
  'permission.respond': {
    params: { tool_use_id: string; decision: PermissionDecision }
    result: boolean
  }
  'session.create': { params: { mode: SessionMode; title: string }; result: Session }
  'session.send_message': {
    params: { session_id: string; content: string }
    result: { run_id: string; status: RunStatus; result: string }
  }
  'session.get_history': { params: { session_id: string }; result: Message[] }
  'session.list': { params: undefined; result: Session[] }
  'session.clear': { params: { session_id: string }; result: 'cleared' }
  'session.close': { params: { session_id: string }; result: 'closed' }
  'session.compact': {
    params: { session_id: string }
    result: { original_tokens: number; summary_tokens: number }
  }
  'run.list': {
    params: { limit?: number }
    result: { runs: RunSummary[]; total: number }
  }
  'event.subscribe': {
    params: {
      topics?: string[]
      scope?: 'global' | `run:${string}`
      replay_from_run?: string
    }
    result: { subscription_id: string; replayed_count: number }
  }
}

export type RPCMethod = keyof RPC
export type RPCParams<M extends RPCMethod> = RPC[M]['params']
export type RPCResult<M extends RPCMethod> = RPC[M]['result']
