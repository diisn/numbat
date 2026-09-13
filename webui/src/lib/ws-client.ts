// 唯一的协议边界模块：持有全部 JSON-RPC + 事件流知识。
// UI 组件只消费 call() 与 onEvent()/onStatus()，不接触 WebSocket 与 envelope 细节。
import type { Envelope, Event, RPCMethod, RPCParams, RPCResult } from '@/types/api'

export type ConnectionStatus = 'connecting' | 'connected' | 'reconnecting' | 'disconnected'
export type EventHandler = (event: Event) => void
export type StatusHandler = (status: ConnectionStatus) => void

export interface WsClientOptions {
  /** WS 端点，默认按当前页面协议推导 ws/wss。 */
  url?: string
  /** 是否自动重连，默认 true。 */
  reconnect?: boolean
  /** 重连最大退避（ms），默认 10000。 */
  maxReconnectDelay?: number
}

interface Pending {
  resolve: (value: unknown) => void
  reject: (error: Error) => void
}

const DEFAULT_URL = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`

export class WsClient {
  private ws: WebSocket | null = null
  private nextId = 1
  private pending = new Map<number, Pending>()
  private eventHandlers = new Set<EventHandler>()
  private statusHandlers = new Set<StatusHandler>()
  private status: ConnectionStatus = 'disconnected'
  private reconnectDelay = 1000
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private readonly url: string
  private readonly shouldReconnect: boolean
  private readonly maxReconnectDelay: number

  constructor(opts: WsClientOptions = {}) {
    this.url = opts.url ?? DEFAULT_URL
    this.shouldReconnect = opts.reconnect ?? true
    this.maxReconnectDelay = opts.maxReconnectDelay ?? 10000
  }

  /** 订阅事件流，返回取消订阅函数。 */
  onEvent(handler: EventHandler): () => void {
    this.eventHandlers.add(handler)
    return () => this.eventHandlers.delete(handler)
  }

  /** 订阅连接状态变更，返回取消订阅函数。 */
  onStatus(handler: StatusHandler): () => void {
    this.statusHandlers.add(handler)
    handler(this.status)
    return () => this.statusHandlers.delete(handler)
  }

  getStatus(): ConnectionStatus {
    return this.status
  }

  /** 建立连接（已连接/连接中时幂等）。 */
  connect(): void {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
      return
    }
    this.setStatus(this.status === 'disconnected' ? 'connecting' : 'reconnecting')
    const ws = new WebSocket(this.url)
    this.ws = ws
    ws.onopen = () => {
      this.reconnectDelay = 1000
      this.setStatus('connected')
    }
    ws.onclose = () => {
      this.ws = null
      // 拒绝所有在途请求，避免 UI 永久挂起
      for (const p of this.pending.values()) {
        p.reject(new Error('connection closed'))
      }
      this.pending.clear()
      if (this.shouldReconnect) {
        this.scheduleReconnect()
      } else {
        this.setStatus('disconnected')
      }
    }
    ws.onerror = () => {
      // 错误后 onclose 会触发重连，这里不重复处理
    }
    ws.onmessage = (e) => {
      if (typeof e.data === 'string') {
        this.handleMessage(e.data)
      }
    }
  }

  /** 主动断开，不再自动重连。 */
  disconnect(): void {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.ws) {
      this.ws.onclose = null
      this.ws.close()
      this.ws = null
    }
    this.setStatus('disconnected')
  }

  /**
   * 发起 JSON-RPC 调用并等待响应。
   * params 为 undefined 的方法（core.ping / session.list）可省略参数。
   */
  call<M extends RPCMethod>(
    method: M,
    ...args: RPCParams<M> extends undefined ? [] : [params: RPCParams<M>]
  ): Promise<RPCResult<M>> {
    const params = args[0]
    return this.send(method, params) as Promise<RPCResult<M>>
  }

  private send(method: string, params: unknown): Promise<unknown> {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('WebSocket not connected'))
    }
    const id = this.nextId++
    const envelope: Envelope = { jsonrpc: '2.0', id, method, params }
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject })
      this.ws!.send(JSON.stringify(envelope))
    })
  }

  private handleMessage(data: string): void {
    let env: Envelope
    try {
      env = JSON.parse(data) as Envelope
    } catch {
      // 防御性忽略非 JSON 帧
      return
    }
    // RPC 响应：带 id 且有 result 或 error
    if (env.id !== undefined && (env.result !== undefined || env.error !== undefined)) {
      const pending = this.pending.get(Number(env.id))
      if (pending) {
        this.pending.delete(Number(env.id))
        if (env.error) {
          pending.reject(new Error(env.error.message))
        } else {
          pending.resolve(env.result)
        }
      }
      return
    }
    // 事件：无 id，result 为带 type 字段的事件对象
    if (env.result && typeof env.result === 'object' && 'type' in env.result) {
      const event = env.result as Event
      for (const handler of this.eventHandlers) {
        try {
          handler(event)
        } catch {
          // 单个处理器异常不影响其他
        }
      }
    }
  }

  private setStatus(status: ConnectionStatus): void {
    this.status = status
    for (const handler of this.statusHandlers) {
      handler(status)
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return
    this.setStatus('reconnecting')
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      this.connect()
    }, this.reconnectDelay)
    // 指数退避：1s → 2s → 4s → 8s → 10s（上限）
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxReconnectDelay)
  }
}
