// 帮助弹窗：快捷键与斜杠命令说明（对齐 TUI 的 ? 帮助页语义）。
// Esc 或点击遮罩关闭。
import { useEffect } from 'react'

interface HelpModalProps {
  onClose: () => void
}

interface ShortcutRow {
  keys: string[]
  desc: string
}

const SHORTCUTS: ShortcutRow[] = [
  { keys: ['Enter'], desc: '发送消息' },
  { keys: ['Shift+Enter'], desc: '输入框内换行' },
  { keys: ['↑', '↓'], desc: '斜杠补全弹窗中移动选择' },
  { keys: ['Enter', 'Tab'], desc: '斜杠补全弹窗中确认选中' },
  { keys: ['Y', 'A', 'N', 'D'], desc: '权限请求：允许一次 / 总是允许 / 拒绝一次 / 总是拒绝' },
  { keys: ['Esc'], desc: '关闭弹窗 / 收起展开的工具卡片' },
  { keys: ['?'], desc: '打开本帮助页' },
]

const COMMANDS: ShortcutRow[] = [
  { keys: ['/new'], desc: '新建会话' },
  { keys: ['/compact'], desc: '压缩当前会话上下文' },
  { keys: ['/clear'], desc: '清空当前会话消息' },
  { keys: ['/技能名'], desc: '触发技能（如 /orchestrate）' },
]

export function HelpModal({ onClose }: HelpModalProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="modal-overlay" onClick={onClose} role="dialog" aria-modal="true" aria-labelledby="help-title">
      <div className="modal-card" onClick={(e) => e.stopPropagation()}>
        <h2 className="modal-title" id="help-title">帮助</h2>

        <h3 className="modal-section">快捷键</h3>
        <table className="help-table">
          <tbody>
            {SHORTCUTS.map((row) => (
              <tr key={row.keys.join('+')}>
                <td className="help-keys">
                  {row.keys.map((k) => (
                    <kbd key={k} className="help-kbd">
                      {k}
                    </kbd>
                  ))}
                </td>
                <td>{row.desc}</td>
              </tr>
            ))}
          </tbody>
        </table>

        <h3 className="modal-section">斜杠命令</h3>
        <table className="help-table">
          <tbody>
            {COMMANDS.map((row) => (
              <tr key={row.keys.join('+')}>
                <td className="help-keys">
                  {row.keys.map((k) => (
                    <kbd key={k} className="help-kbd">
                      {k}
                    </kbd>
                  ))}
                </td>
                <td>{row.desc}</td>
              </tr>
            ))}
          </tbody>
        </table>

        <p className="modal-note">
          运行时「发送」按钮会变为「停止」，点击即中止当前任务（agent.abort）。
        </p>

        <button type="button" className="help-close-btn" onClick={onClose}>
          关闭
        </button>
      </div>
    </div>
  )
}
