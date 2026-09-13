// 主题工具：应用/读取/保存 Numbat WebUI 主题。
// 默认跟随系统（auto），用户选择持久化到 localStorage('numbat-theme')。

export type ThemePreference = 'dark' | 'light' | 'auto'

const THEME_KEY = 'numbat-theme'

// 将主题偏好应用到 <html data-theme>。
// auto 时按当前系统偏好决定，并保持后续不自动跟随（简单起见只在设置页监听变化）。
export function applyTheme(t: ThemePreference) {
  if (t === 'auto') {
    const prefersLight = window.matchMedia('(prefers-color-scheme: light)').matches
    document.documentElement.setAttribute('data-theme', prefersLight ? 'light' : 'dark')
  } else {
    document.documentElement.setAttribute('data-theme', t)
  }
}

export function loadThemePreference(): ThemePreference {
  const saved = localStorage.getItem(THEME_KEY)
  if (saved === 'dark' || saved === 'light' || saved === 'auto') return saved
  return 'auto'
}

export function saveThemePreference(t: ThemePreference) {
  localStorage.setItem(THEME_KEY, t)
}