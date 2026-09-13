// 测试用 WebSocket 替身：允许测试主动驱动 open / message / close，
// 从而在没有真实网络的前提下验证 WsClient 的状态机与编解码。
export class MockWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3

  /** 按创建顺序记录所有实例，便于断言重连次数与目标地址。 */
  static instances: MockWebSocket[] = []

  static reset(): void {
    MockWebSocket.instances = []
  }

  /** 最近创建的实例（断言不通过时给出明确错误）。 */
  static get last(): MockWebSocket {
    const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1]
    if (!ws) throw new Error('尚未创建任何 MockWebSocket 实例')
    return ws
  }

  readonly url: string
  readonly sent: string[] = []
  readyState: number = MockWebSocket.CONNECTING

  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
  }

  send(data: string): void {
    this.sent.push(data)
  }

  close(): void {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  }

  /* ── 以下为测试驱动方法，模拟真实网络事件 ── */

  simulateOpen(): void {
    this.readyState = MockWebSocket.OPEN
    this.onopen?.()
  }

  /** 发送一帧 JSON（RPC 响应或事件）。 */
  simulateMessage(payload: unknown): void {
    this.onmessage?.({ data: JSON.stringify(payload) })
  }

  /** 发送一帧原始文本，用于验证非 JSON 帧的容错。 */
  simulateRawMessage(data: string): void {
    this.onmessage?.({ data })
  }

  /** 模拟连接异常断开（非主动 disconnect）。 */
  simulateClose(): void {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  }

  /** 已发送帧解析后的对象列表。 */
  get sentEnvelopes(): Record<string, unknown>[] {
    return this.sent.map((raw) => JSON.parse(raw) as Record<string, unknown>)
  }
}
