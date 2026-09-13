import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MockClient } from './mock-client'
import { WsClient } from './ws-client'
import type { Event, RPCMethod, RunStatus, Session } from '@/types/api'

/**
 * 后端真实 RPC 清单（唯一事实来源）：
 *   - internal/transport/handler.go 的 handlers map
 *   - internal/transport/server.go 的 event.subscribe 分支
 * Mock 既不能缺（前端会撞空），也不能多（造后端没有的能力，见 AGENTS.md §5）。
 */
const BACKEND_RPC_METHODS: readonly RPCMethod[] = [
  'core.ping',
  'agent.run',
  'agent.abort',
  'permission.respond',
  'session.create',
  'session.send_message',
  'session.get_history',
  'session.list',
  'session.clear',
  'session.close',
  'session.compact',
  'run.list',
  'event.subscribe',
]

/** 后端事件 topic 清单：internal/events/events.go 的 topicNames。 */
const BACKEND_EVENT_TYPES: readonly Event['type'][] = [
  'run.started',
  'run.finished',
  'llm.request',
  'llm.response',
  'llm.token',
  'llm.usage',
  'tool.call_started',
  'tool.call_finished',
  'tool.call_failed',
  'permission.requested',
  'permission.granted',
  'permission.denied',
  'context.compacted',
  'session.created',
  'session.closed',
  'subagent.started',
  'subagent.finished',
  'skill.invoked',
]

/** PRD §7 明确"后端待实现"的能力，Mock 不得自行提供。 */
const RPC_NOT_IN_BACKEND = [
  'task.list',
  'skills.list',
  'agents.list',
  'policy.get',
  'policy.set',
  'session.delete',
]

const RUN_STATUSES: readonly RunStatus[] = ['running', 'success', 'failed']

let client: MockClient
let emitted: Event[]

type CallAny = (method: string, params?: unknown) => Promise<unknown>

interface Outcome<T> {
  ok: boolean
  value?: T
  error?: string
}

/**
 * 立即挂上 then/catch，避免在推进定时器期间出现"未处理拒绝"。
 * Mock 的模拟由 setTimeout 驱动，拒绝往往发生在 await 别的东西的时候。
 */
function track<T>(promise: Promise<T>): Promise<Outcome<T>> {
  return promise.then(
    (value) => ({ ok: true, value }),
    (e: unknown) => ({ ok: false, error: e instanceof Error ? e.message : String(e) }),
  )
}

/** 推进所有定时器直至调用落定；失败则抛出，便于断言成功路径。 */
async function callOk<T>(promise: Promise<T>): Promise<T> {
  const settled = track(promise)
  await vi.runAllTimersAsync()
  const outcome = await settled
  if (!outcome.ok) throw new Error(outcome.error)
  return outcome.value as T
}

/** 按 type 查找事件并获得收窄后的类型。 */
function findEvent<T extends Event['type']>(type: T) {
  return emitted.find((e) => e.type === type) as Extract<Event, { type: T }> | undefined
}

/** 带 run_id 的事件（run 级事件）。 */
function runScopedEvents() {
  return emitted.filter((e): e is Extract<Event, { run_id: string }> => 'run_id' in e)
}

async function createSession(): Promise<Session> {
  return client.call('session.create', { mode: 'chat', title: '契约测试' })
}

/**
 * 调用任意方法并推进定时器（Mock 的模拟由 setTimeout 驱动）。
 * 返回是否成功、错误信息与结果，便于区分「未实现」与其他业务错误。
 */
async function invoke(
  method: string,
  params?: unknown,
): Promise<{ ok: boolean; error?: string; value?: unknown }> {
  const settled = track((client.call as unknown as CallAny)(method, params))
  await vi.runAllTimersAsync()
  return settled
}

function isUnimplemented(result: { ok: boolean; error?: string }): boolean {
  return !result.ok && (result.error ?? '').includes('未实现')
}

beforeEach(() => {
  vi.useFakeTimers()
  client = new MockClient()
  emitted = []
  client.onEvent((e) => emitted.push(e))
  client.connect()
})

afterEach(() => {
  client.disconnect()
  vi.useRealTimers()
})

describe('RPC 表面与后端一致', () => {
  it('后端每个 RPC 在 Mock 中都有实现', async () => {
    const missing: string[] = []
    for (const method of BACKEND_RPC_METHODS) {
      const session = await createSession()
      // core.ping / session.list 无参数，故允许 undefined
      const paramsByMethod: Record<string, Record<string, unknown> | undefined> = {
        'agent.run': { goal: '你好' },
        'agent.abort': { run_id: 'run-not-exist' },
        'permission.respond': { tool_use_id: 'tu-not-exist', decision: 'allow_once' },
        'session.create': { mode: 'chat', title: 'T' },
        'session.send_message': { session_id: session.id, content: '你好' },
        'session.get_history': { session_id: session.id },
        'session.clear': { session_id: session.id },
        'session.close': { session_id: session.id },
        'session.compact': { session_id: session.id },
        'event.subscribe': {},
      }
      if (isUnimplemented(await invoke(method, paramsByMethod[method]))) missing.push(method)
    }
    expect(missing).toEqual([])
  })

  it('Mock 不提供后端没有的 RPC（不造后端没有的能力）', async () => {
    const invented: string[] = []
    for (const method of RPC_NOT_IN_BACKEND) {
      if (!isUnimplemented(await invoke(method, {}))) invented.push(method)
    }
    expect(invented).toEqual([])
  })

  it('未知方法一律抛「未实现」', async () => {
    const result = await invoke('totally.unknown.method', {})
    expect(isUnimplemented(result)).toBe(true)
  })
})

describe('RPC 返回结构', () => {
  it('core.ping 返回 pong', async () => {
    expect(await client.call('core.ping')).toBe('pong')
  })

  it('session.create 返回完整 Session 结构', async () => {
    const session = await createSession()
    expect(Object.keys(session).sort()).toEqual(
      ['created_at', 'id', 'mode', 'run_ids', 'status', 'title', 'updated_at'].sort(),
    )
    expect(session.mode).toBe('chat')
    expect(session.status).toBe('active')
    expect(session.title).toBe('契约测试')
    expect(session.run_ids).toEqual([])
    expect(Number.isNaN(Date.parse(session.created_at))).toBe(false)
    expect(Number.isNaN(Date.parse(session.updated_at))).toBe(false)
  })

  it('session.list 返回 Session[]', async () => {
    await createSession()
    const list = await client.call('session.list')
    expect(Array.isArray(list)).toBe(true)
    expect(list).toHaveLength(1)
    expect(list[0]).toHaveProperty('id')
  })

  it('session.get_history 返回 Message[]', async () => {
    const session = await createSession()
    const history = await client.call('session.get_history', { session_id: session.id })
    expect(Array.isArray(history)).toBe(true)
  })

  it('session.clear 返回 cleared', async () => {
    const session = await createSession()
    expect(await client.call('session.clear', { session_id: session.id })).toBe('cleared')
  })

  it('session.close 返回 closed 并发射 session.closed', async () => {
    const session = await createSession()
    expect(await client.call('session.close', { session_id: session.id })).toBe('closed')

    const closed = findEvent('session.closed')
    expect(closed?.session_id).toBe(session.id)
  })

  it('session.compact 返回 { original_tokens, summary_tokens }', async () => {
    const session = await createSession()
    const result = await client.call('session.compact', { session_id: session.id })
    expect(Object.keys(result).sort()).toEqual(['original_tokens', 'summary_tokens'])
    expect(typeof result.original_tokens).toBe('number')
    expect(typeof result.summary_tokens).toBe('number')
  })

  it('event.subscribe 返回 { subscription_id, replayed_count }', async () => {
    const result = await client.call('event.subscribe', {})
    expect(Object.keys(result).sort()).toEqual(['replayed_count', 'subscription_id'])
    expect(typeof result.subscription_id).toBe('string')
    expect(typeof result.replayed_count).toBe('number')
  })

  it('run.list 返回 { runs, total }，字段与 RunSummary 契约一致', async () => {
    const result = await client.call('run.list', {})
    expect(Object.keys(result).sort()).toEqual(['runs', 'total'])
    expect(result.total).toBe(result.runs.length)
    expect(result.runs.length).toBeGreaterThan(0)

    const EXPECTED_KEYS = [
      'duration_ms',
      'events',
      'finished_at',
      'goal',
      'run_id',
      'started_at',
      'status',
      'subagents',
      'tokens',
      'tools',
    ]
    for (const run of result.runs) {
      // 允许可选的 skill 字段
      const keys = Object.keys(run).filter((k) => k !== 'skill').sort()
      expect(keys).toEqual(EXPECTED_KEYS)
      // 状态词表：不得出现 'completed'
      expect(RUN_STATUSES).toContain(run.status)
      // tokens 为线缆形状，不是 input/output
      expect(Object.keys(run.tokens).sort()).toEqual([
        'context_pct',
        'input_tokens',
        'output_tokens',
      ])
      expect(run.tokens.context_pct).toBeLessThanOrEqual(1)
    }
  })

  it('run.list 遵守 limit 参数', async () => {
    const all = await client.call('run.list', {})
    const limited = await client.call('run.list', { limit: 2 })
    expect(limited.runs).toHaveLength(2)
    expect(limited.total).toBe(all.total)
  })

  it('agent.abort 对不存在的 run 返回 false', async () => {
    expect(await client.call('agent.abort', { run_id: 'run-not-exist' })).toBe(false)
  })

  it('permission.respond 对不存在的 tool_use_id 返回 false', async () => {
    expect(
      await client.call('permission.respond', { tool_use_id: 'tu-not-exist', decision: 'allow_once' }),
    ).toBe(false)
  })

  it('session.send_message 返回 { run_id, status, result }', async () => {
    const session = await createSession()
    const result = await callOk(
      client.call('session.send_message', {
        session_id: session.id,
        content: '你好',
      }),
    )

    expect(Object.keys(result).sort()).toEqual(['result', 'run_id', 'status'])
    expect(typeof result.run_id).toBe('string')
    expect(result.run_id.length).toBeGreaterThan(0)
    expect(RUN_STATUSES).toContain(result.status)
  })
})

describe('状态词表与后端一致（联调踩坑回归）', () => {
  it('run.finished.status 属于 running/success/failed，而不是 completed', async () => {
    const session = await createSession()
    await callOk(client.call('session.send_message', { session_id: session.id, content: '你好' }))

    const finished = findEvent('run.finished')
    expect(finished).toBeDefined()
    expect(RUN_STATUSES).toContain(finished!.status)
    expect(finished!.status).not.toBe('completed')
  })

  it('agent.run 的 status 属于 running/success/failed', async () => {
    const result = await callOk(client.call('agent.run', { goal: '你好' }))
    expect(RUN_STATUSES).toContain(result.status)
  })

  it('subagent.finished.status 属于 running/success/failed', async () => {
    const session = await createSession()
    await callOk(
      client.call('session.send_message', {
        session_id: session.id,
        content: '分析这个项目的架构',
      }),
    )

    const finished = findEvent('subagent.finished')
    expect(finished).toBeDefined()
    expect(RUN_STATUSES).toContain(finished!.status)
  })

  it('llm.usage.context_pct 是 0–1 的小数，而不是 0–100 的百分数', async () => {
    const session = await createSession()
    await callOk(client.call('session.send_message', { session_id: session.id, content: '你好' }))

    const usage = findEvent('llm.usage')
    expect(usage).toBeDefined()
    expect(usage!.context_pct).toBeGreaterThanOrEqual(0)
    expect(usage!.context_pct).toBeLessThanOrEqual(1)
  })
})

describe('同步/异步语义与后端一致', () => {
  it('session.send_message 返回时 run.finished 已发出（后端为同步阻塞）', async () => {
    const session = await createSession()
    const result = await callOk(
      client.call('session.send_message', { session_id: session.id, content: '你好' }),
    )

    const finished = findEvent('run.finished')
    expect(finished).toBeDefined()
    // 事件里的 run_id 必须与 RPC 返回的一致
    expect(finished!.run_id).toBe(result.run_id)
  })

  it('agent.run 返回时 run.finished 已发出（后端为同步阻塞）', async () => {
    const result = await callOk(client.call('agent.run', { goal: '你好' }))

    const finished = findEvent('run.finished')
    expect(finished).toBeDefined()
    expect(finished!.run_id).toBe(result.run_id)
  })

  it('中止 run：先发 run.finished，随后 RPC 以 context canceled 结束', async () => {
    const session = await createSession()
    // 先挂上结算回调，避免推进定时器时出现未处理拒绝
    const settled = track(
      client.call('session.send_message', { session_id: session.id, content: '你好' }),
    )

    // 推进到 run 已开始、token 仍在流式
    await vi.advanceTimersByTimeAsync(200)
    const started = findEvent('run.started')
    expect(started).toBeDefined()

    expect(await client.call('agent.abort', { run_id: started!.run_id })).toBe(true)

    await vi.runAllTimersAsync()
    const outcome = await settled
    expect(outcome.ok).toBe(false)
    expect(outcome.error).toBe('context canceled')

    const finished = findEvent('run.finished')
    expect(finished).toBeDefined()
    expect(finished!.run_id).toBe(started!.run_id)
    expect(finished!.status).toBe('failed')
  })

  it('中止 run 会立即结束其待审批的权限请求，不会卡到审批超时', async () => {
    const session = await createSession()
    const settled = track(
      client.call('session.send_message', { session_id: session.id, content: '运行 ls 命令' }),
    )

    await vi.advanceTimersByTimeAsync(800)
    expect(findEvent('permission.requested')).toBeDefined()
    const started = findEvent('run.started')
    expect(started).toBeDefined()

    expect(await client.call('agent.abort', { run_id: started!.run_id })).toBe(true)

    // 无需推进 30s 审批超时，run 就应能收口
    await vi.runAllTimersAsync()
    const outcome = await settled
    expect(outcome.ok).toBe(false)
    expect(outcome.error).toBe('context canceled')
  })
})

describe('错误语义与后端一致', () => {
  it('向不存在的会话发送消息抛错', async () => {
    await expect(
      client.call('session.send_message', { session_id: 'sess-not-exist', content: '你好' }),
    ).rejects.toThrow('session not found')
  })

  it('向已关闭的会话发送消息抛错', async () => {
    const session = await createSession()
    await client.call('session.close', { session_id: session.id })

    await expect(
      client.call('session.send_message', { session_id: session.id, content: '你好' }),
    ).rejects.toThrow('session is closed')
  })
})

describe('事件流契约', () => {
  async function runTextScenario() {
    const session = await createSession()
    await callOk(client.call('session.send_message', { session_id: session.id, content: '你好' }))
  }

  it('发射的事件 type 全部属于后端事件清单', async () => {
    await runTextScenario()
    expect(emitted.length).toBeGreaterThan(0)

    const unknown = emitted
      .map((e) => e.type)
      .filter((t) => !BACKEND_EVENT_TYPES.includes(t))
    expect(unknown).toEqual([])
  })

  it('事件顺序：run.started → llm.token* → run.finished', async () => {
    await runTextScenario()

    const indexOf = (t: Event['type']) => emitted.findIndex((e) => e.type === t)
    const startedAt = indexOf('run.started')
    const finishedAt = indexOf('run.finished')
    const tokenIndexes = emitted
      .map((e, i) => (e.type === 'llm.token' ? i : -1))
      .filter((i) => i >= 0)

    expect(startedAt).toBeGreaterThanOrEqual(0)
    expect(tokenIndexes.length).toBeGreaterThan(0)
    expect(Math.min(...tokenIndexes)).toBeGreaterThan(startedAt)
    expect(finishedAt).toBeGreaterThan(Math.max(...tokenIndexes))
  })

  it('run.started 的 goal 等于用户消息（前端据此认领 run）', async () => {
    await runTextScenario()

    const started = findEvent('run.started')
    expect(started).toBeDefined()
    expect(started!.goal).toBe('你好')
  })

  it('同一轮 run 的 run 级事件共用同一个 run_id', async () => {
    await runTextScenario()

    const runIds = new Set(runScopedEvents().map((e) => e.run_id))
    expect(runIds.size).toBe(1)
  })

  it('session.create 发射 session.created，含 session_id 与 mode', async () => {
    const session = await createSession()

    const created = findEvent('session.created')
    expect(created).toBeDefined()
    expect(created!.session_id).toBe(session.id)
    expect(created!.mode).toBe('chat')
  })

  it('需要审批的工具会发射 permission.requested 并挂起等待 respond', async () => {
    const session = await createSession()
    const settled = track(
      client.call('session.send_message', {
        session_id: session.id,
        content: '运行 ls 命令',
      }),
    )

    await vi.advanceTimersByTimeAsync(800)
    const requested = findEvent('permission.requested')
    expect(requested).toBeDefined()
    expect(requested!.tool_name).toBe('bash')
    expect(typeof requested!.preview).toBe('string')

    // 审批前 run 不应完成
    const finishedBefore = emitted.some((e) => e.type === 'run.finished')
    expect(finishedBefore).toBe(false)

    expect(
      await client.call('permission.respond', {
        tool_use_id: requested!.tool_use_id,
        decision: 'allow_once',
      }),
    ).toBe(true)

    await vi.runAllTimersAsync()
    const outcome = await settled
    expect(outcome.ok).toBe(true)

    expect(findEvent('permission.granted')).toBeDefined()
    expect(findEvent('run.finished')).toBeDefined()
  })
})

describe('接口与 WsClient 一致（AGENTS.md §3）', () => {
  const PUBLIC_API = ['connect', 'disconnect', 'getStatus', 'onEvent', 'onStatus', 'call'] as const

  it('两者暴露相同的公开方法', () => {
    const mockProto = MockClient.prototype as unknown as Record<string, unknown>
    const wsProto = WsClient.prototype as unknown as Record<string, unknown>
    for (const method of PUBLIC_API) {
      expect(typeof mockProto[method], `MockClient.${method}`).toBe('function')
      expect(typeof wsProto[method], `WsClient.${method}`).toBe('function')
    }
  })

  it('getStatus 初值为 disconnected，connect 后进入 connecting', () => {
    const fresh = new MockClient()
    expect(fresh.getStatus()).toBe('disconnected')
    fresh.connect()
    expect(fresh.getStatus()).toBe('connecting')
    fresh.disconnect()
  })

  it('onStatus 订阅时立即回调，取消订阅后不再回调', () => {
    const fresh = new MockClient()
    const seen: string[] = []
    const off = fresh.onStatus((s) => seen.push(s))
    expect(seen).toEqual(['disconnected'])
    off()
    fresh.connect()
    fresh.disconnect()
    expect(seen).toEqual(['disconnected'])
  })

  it('onEvent 取消订阅后不再收到事件', async () => {
    const fresh = new MockClient()
    const seen: Event[] = []
    const off = fresh.onEvent((e) => seen.push(e))
    off()
    fresh.connect()

    await fresh.call('session.create', { mode: 'chat', title: 'T' })

    expect(seen).toEqual([])
    fresh.disconnect()
  })
})
