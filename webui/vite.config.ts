import { fileURLToPath, URL } from 'node:url'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig(({ command }) => ({
  // 生产构建产物挂在 numbat-core 网关的 /app/ 下（见 internal/transport/gateway.go），
  // 故构建时资源前缀必须是 /app/；开发期保持根路径，不改变本地访问地址。
  base: command === 'build' ? '/app/' : '/',
  plugins: [tailwindcss(), react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    // 直接产出到 Go 侧 go:embed 的目录，由 numbat-core 二进制内嵌
    outDir: fileURLToPath(new URL('../internal/webui/dist', import.meta.url)),
    emptyOutDir: true,
  },
  server: {
    // 开发期将 /ws、/health、/metrics 代理到本地 numbat-core 网关（默认 7438），
    // 前端直连真实后端，不建 mock server。
    proxy: {
      '/health': 'http://localhost:7438',
      '/metrics': 'http://localhost:7438',
      '/ws': { target: 'ws://localhost:7438', ws: true },
    },
  },
}))
