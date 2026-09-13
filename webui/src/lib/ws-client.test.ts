import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WsClient, type ConnectionStatus } from './ws-client'
import { MockWebSocket } from '@/test-utils/mock-websocket'

const URL = 'ws://localhost:7438/ws'

let client: WsClient

/** 已连接状态的 client，供 RPC / 事件用例复用。 */
function connectClient(opts: { reconnect?: boolean } = {}): WsClient {
  const c = new WsClient({ url: URL, reconnect: opts.reconnect ?? false })
  c.connect()
  MockWebSocket.last.simulateOpen()
  return c
}

beforeEach(() => {
  MockWebSocket.reset()
  vi.stubGlobal('WebSocket', MockWebSocket)
  client = new WsClient({ url: URL, reconnect: false })
})

afterEach(() => {
  client.disconnect()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('连接管理', () => {
  it('connect() 创建连接并进入 connecting 状态', () => {
    expect(client.getStatus()).toBe('disconnected')

    client.connect()

    expect(client.getStatus()).toBe('connecting')
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(MockWebSocket.last.url).toBe(URL)
    expect(MockWebSocket.last.readyState).toBe(MockWebSocket.CONNECTING)
  })

  it('收到 open 后进入 connected 状态', () => {
    client.connect()
    MockWebSocket.last.simulateOpen()
    expect(client.getStatus()).toBe('connected')
  })

  it('连接中重复 connect() 不重复建连', () => {
    client.connect()
    client.connect()
    client.connect()
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('已连接时重复 connect() 不重复建连', () => {
    client.connect()
    MockWebSocket.last.simulateOpen()
    client.connect()
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('disconnect() 关闭连接并回到 disconnected', () => {
    client.connect()
    MockWebSocket.last.simulateOpen()

    client.disconnect()

    expect(client.getStatus()).toBe('disconnected')
    expect(MockWebSocket.last.readyState).toBe(MockWebSocket.CLOSED)
  })

  it('未指定地址时按页面协议推导为 ws://<host>/ws', () => {
    const c = new WsClient({ reconnect: false })
    c.connect()
    expect(MockWebSocket.last.url).toBe(`ws://${location.host}/ws`)
    c.disconnect()
  })

  it('显式 url 覆盖默认推导', () => {
    const c = new WsClient({ url: 'wss://example.com/ws', reconnect: false })
    c.connect()
    expect(MockWebSocket.last.url).toBe('wss://example.com/ws')
    c.disconnect()
  })
})

describe('状态订阅', () => {
  it('onStatus 订阅时立即回调当前状态', () => {
    const seen: ConnectionStatus[] = []
    client.onStatus((s) => seen.push(s))
    expect(seen).toEqual(['disconnected'])
  })

  it('状态变化按顺序通知订阅者', () => {
    const seen: ConnectionStatus[] = []
    client.onStatus((s) => seen.push(s))

    client.connect()
    MockWebSocket.last.simulateOpen()
    client.disconnect()

    expect(seen).toEqual(['disconnected', 'connecting', 'connected', 'disconnected'])
  })

  it('取消订阅后不再收到通知', () => {
    const seen: ConnectionStatus[] = []
    const off = client.onStatus((s) => seen.push(s))
    off()

    client.connect()

    expect(seen).toEqual(['disconnected'])
  })
})

describe('RPC 调用', () => {
  it('call() 发送符合 JSON-RPC 2.0 的 envelope', async () => {
    const c = connectClient()
    const pending = c.call('session.list')

    expect(MockWebSocket.last.sentEnvelopes[0]).toEqual({
      jsonrpc: '2.0',
      id: 1,
      method: 'session.list',
    })

    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', id: 1, result: [] })
    await expect(pending).resolves.toEqual([])
  })

  it('params 随 envelope 一并序列化', () => {
    const c = connectClient()
    void c.call('session.send_message', { session_id: 'sess-1', content: '你好' })

    expect(MockWebSocket.last.sentEnvelopes[0]).toEqual({
      jsonrpc: '2.0',
      id: 1,
      method: 'session.send_message',
      params: { session_id: 'sess-1', content: '你好' },
    })
  })

  it('id 自增，响应按 id 匹配到对应调用（支持乱序返回）', async () => {
    const c = connectClient()
    const first = c.call('core.ping')
    const second = c.call('core.ping')

    expect(MockWebSocket.last.sentEnvelopes.map((e) => e.id)).toEqual([1, 2])

    // 乱序返回：先回第二个
    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', id: 2, result: 'pong' })
    await expect(second).resolves.toBe('pong')

    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', id: 1, result: 'pong' })
    await expect(first).resolves.toBe('pong')
  })

  it('错误响应 reject 并透出 message', async () => {
    const c = connectClient()
    const pending = c.call('session.get_history', { session_id: 'sess-1' })

    MockWebSocket.last.simulateMessage({
      jsonrpc: '2.0',
      id: 1,
      error: { code: -32602, message: 'session_id is required' },
    })

    await expect(pending).rejects.toThrow('session_id is required')
  })

  it('未连接时 call() 立即 reject', async () => {
    await expect(client.call('core.ping')).rejects.toThrow('WebSocket not connected')
  })

  it('连接断开时在途请求被 reject，不会永久挂起', async () => {
    const c = connectClient()
    const pending = c.call('core.ping')

    MockWebSocket.last.simulateClose()

    await expect(pending).rejects.toThrow('connection closed')
  })

  it('未知 id 的响应被忽略，不影响在途请求', async () => {
    const c = connectClient()
    const pending = c.call('core.ping')

    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', id: 99, result: 'pong' })
    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', id: 1, result: 'pong' })

    await expect(pending).resolves.toBe('pong')
  })
})

describe('事件分发', () => {
  const runStarted = { type: 'run.started', run_id: 'run-1', goal: '你好' }

  it('无 id 且 result 带 type 的帧被当作事件分发', () => {
    const c = connectClient()
    const seen: unknown[] = []
    c.onEvent((e) => seen.push(e))

    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', result: runStarted })

    expect(seen).toEqual([runStarted])
  })

  it('多个订阅者都能收到同一事件', () => {
    const c = connectClient()
    const a: unknown[] = []
    const b: unknown[] = []
    c.onEvent((e) => a.push(e))
    c.onEvent((e) => b.push(e))

    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', result: runStarted })

    expect(a).toEqual([runStarted])
    expect(b).toEqual([runStarted])
  })

  it('RPC 响应不会被当作事件分发', () => {
    const c = connectClient()
    const seen: unknown[] = []
    c.onEvent((e) => seen.push(e))

    void c.call('session.list')
    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', id: 1, result: [] })

    expect(seen).toEqual([])
  })

  it('非 JSON 帧被防御性忽略', () => {
    const c = connectClient()
    const seen: unknown[] = []
    c.onEvent((e) => seen.push(e))

    expect(() => MockWebSocket.last.simulateRawMessage('not-json{')).not.toThrow()
    expect(seen).toEqual([])
  })

  it('缺少 type 字段的 result 不会污染事件流', () => {
    const c = connectClient()
    const seen: unknown[] = []
    c.onEvent((e) => seen.push(e))

    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', result: { foo: 'bar' } })
    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', result: 'plain-string' })

    expect(seen).toEqual([])
  })

  it('单个订阅者抛错不影响其他订阅者', () => {
    const c = connectClient()
    const seen: unknown[] = []
    c.onEvent(() => {
      throw new Error('boom')
    })
    c.onEvent((e) => seen.push(e))

    expect(() =>
      MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', result: runStarted }),
    ).not.toThrow()
    expect(seen).toEqual([runStarted])
  })

  it('取消订阅后不再收到事件', () => {
    const c = connectClient()
    const seen: unknown[] = []
    const off = c.onEvent((e) => seen.push(e))
    off()

    MockWebSocket.last.simulateMessage({ jsonrpc: '2.0', result: runStarted })

    expect(seen).toEqual([])
  })
})

describe('重连与指数退避', () => {
  /** 推进假定时器直到产生下一个连接，返回等待毫秒数（上限 guardMs）。 */
  async function waitForNextConnect(guardMs: number): Promise<number> {
    const before = MockWebSocket.instances.length
    let waited = 0
    while (MockWebSocket.instances.length === before && waited < guardMs) {
      await vi.advanceTimersByTimeAsync(100)
      waited += 100
    }
    return waited
  }

  it('异常断开后进入 reconnecting，并按 1s→2s→4s→8s→10s 退避', async () => {
    vi.useFakeTimers()
    const c = new WsClient({ url: URL })
    c.connect()
    MockWebSocket.last.simulateOpen()

    MockWebSocket.last.simulateClose()
    expect(c.getStatus()).toBe('reconnecting')

    const delays: number[] = []
    for (let i = 0; i < 5; i++) {
      delays.push(await waitForNextConnect(20000))
      // 不 open，让退避继续累积
      MockWebSocket.last.simulateClose()
    }

    expect(delays).toEqual([1000, 2000, 4000, 8000, 10000])
    c.disconnect()
  })

  it('退避上限为 maxReconnectDelay', async () => {
    vi.useFakeTimers()
    const c = new WsClient({ url: URL, maxReconnectDelay: 2000 })
    c.connect()
    MockWebSocket.last.simulateOpen()
    MockWebSocket.last.simulateClose()

    const delays: number[] = []
    for (let i = 0; i < 3; i++) {
      delays.push(await waitForNextConnect(10000))
      MockWebSocket.last.simulateClose()
    }

    expect(delays).toEqual([1000, 2000, 2000])
    c.disconnect()
  })

  it('重新连上后退避重置为 1s', async () => {
    vi.useFakeTimers()
    const c = new WsClient({ url: URL })
    c.connect()
    MockWebSocket.last.simulateOpen()

    // 连续两次失败，退避已增长到 4s
    MockWebSocket.last.simulateClose()
    expect(await waitForNextConnect(5000)).toBe(1000)
    MockWebSocket.last.simulateClose()
    expect(await waitForNextConnect(5000)).toBe(2000)

    // 这次成功连接，退避应重置回 1s
    MockWebSocket.last.simulateOpen()
    MockWebSocket.last.simulateClose()
    expect(await waitForNextConnect(5000)).toBe(1000)

    c.disconnect()
  })

  it('reconnect:false 时断开后保持 disconnected 且不再建连', async () => {
    vi.useFakeTimers()
    const c = new WsClient({ url: URL, reconnect: false })
    c.connect()
    MockWebSocket.last.simulateOpen()

    MockWebSocket.last.simulateClose()

    expect(c.getStatus()).toBe('disconnected')
    await vi.advanceTimersByTimeAsync(60000)
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  it('disconnect() 会取消已排期的重连', async () => {
    vi.useFakeTimers()
    const c = new WsClient({ url: URL })
    c.connect()
    MockWebSocket.last.simulateOpen()
    MockWebSocket.last.simulateClose()

    c.disconnect()
    await vi.advanceTimersByTimeAsync(60000)

    expect(MockWebSocket.instances).toHaveLength(1)
    expect(c.getStatus()).toBe('disconnected')
  })
})
