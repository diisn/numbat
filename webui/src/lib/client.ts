// 客户端工厂：唯一的数据源切换开关。
// USE_MOCK = true → MockClient（前端独立开发）
// USE_MOCK = false → WsClient（联调真实后端）
import { MockClient } from './mock-client'
import { WsClient } from './ws-client'

export type { ConnectionStatus, EventHandler, StatusHandler } from './ws-client'

/** 前端独立开发阶段用 Mock；联调时改为 false。 */
export const USE_MOCK = false

export type Client = WsClient | MockClient

export function createClient(): Client {
  return USE_MOCK ? new MockClient() : new WsClient()
}
