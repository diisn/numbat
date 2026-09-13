import { useState, useEffect } from 'react'
import {
  type ThemePreference,
  applyTheme,
  loadThemePreference,
  saveThemePreference,
} from '@/lib/theme'

interface SettingsPageProps {
  isMock: boolean
}

export function SettingsPage({ isMock }: SettingsPageProps) {
  // 从 localStorage 恢复用户偏好；未设置过则跟随系统
  const [theme, setTheme] = useState<ThemePreference>(loadThemePreference)
  const [reduceMotion, setReduceMotion] = useState(false)
  const [batchInterval, setBatchInterval] = useState(16)
  const [virtualThreshold, setVirtualThreshold] = useState(500)

  // 主题切换：应用到 documentElement 并持久化
  useEffect(() => {
    applyTheme(theme)
    saveThemePreference(theme)
    if (theme === 'auto') {
      const mq = window.matchMedia('(prefers-color-scheme: light)')
      const handler = () => applyTheme('auto')
      mq.addEventListener('change', handler)
      return () => mq.removeEventListener('change', handler)
    }
  }, [theme])

  return (
    <div className="settings-page">
      <h2 className="page-title">设置</h2>

      <section className="settings-section">
        <h3>外观</h3>
        <div className="settings-row">
          <label>主题</label>
          <select value={theme} onChange={(e) => setTheme(e.target.value as 'dark' | 'light' | 'auto')}>
            <option value="dark">暗色</option>
            <option value="light">亮色</option>
            <option value="auto">跟随系统</option>
          </select>
        </div>
        <div className="settings-row">
          <label>减少动画</label>
          <input
            type="checkbox"
            checked={reduceMotion}
            onChange={(e) => setReduceMotion(e.target.checked)}
          />
          <span className="settings-hint">尊重 prefers-reduced-motion</span>
        </div>
      </section>

      <section className="settings-section">
        <h3>流式渲染</h3>
        <div className="settings-row">
          <label>批量合并间隔</label>
          <input
            type="number"
            value={batchInterval}
            min={0}
            max={100}
            onChange={(e) => setBatchInterval(Number(e.target.value))}
          />
          <span className="settings-hint">ms（requestAnimationFrame）</span>
        </div>
        <div className="settings-row">
          <label>虚拟列表阈值</label>
          <input
            type="number"
            value={virtualThreshold}
            min={100}
            step={100}
            onChange={(e) => setVirtualThreshold(Number(e.target.value))}
          />
          <span className="settings-hint">消息数超过此值启用虚拟滚动</span>
        </div>
      </section>

      <section className="settings-section">
        <h3>调试</h3>
        <div className="settings-row">
          <label>调试模式</label>
          <span className="settings-hint">在对话页头部点击「调试」按钮切换</span>
        </div>
      </section>

      {isMock && (
        <div className="settings-mock-notice">
          当前为 Mock 模式。切换 USE_MOCK = false 后生效。
        </div>
      )}
    </div>
  )
}
