import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vitest/config'

// ws-client 依赖浏览器环境的 location 与 WebSocket，
// 因此用 jsdom 提供 location，WebSocket 由测试内的 MockWebSocket 替换。
export default defineConfig({
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    restoreMocks: true,
  },
})
