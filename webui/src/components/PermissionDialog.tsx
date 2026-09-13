import type { PermissionDecision } from '@/types/api'

interface PermissionDialogProps {
  /** 供全局键盘快捷键定位对话框；可在 App.tsx 场景省略 */
  runId?: string
  toolUseId?: string
  toolName: string
  params: Record<string, unknown>
  preview: string
  onRespond: (decision: PermissionDecision) => void
}

// 长参数值截断，避免撑爆对话框
function formatParamValue(v: unknown): string {
  const s = typeof v === 'string' ? v : JSON.stringify(v)
  return s.length > 200 ? s.slice(0, 200) + '…' : s
}

export function PermissionDialog({ runId, toolUseId, toolName, params, preview, onRespond }: PermissionDialogProps) {
  const paramLines = Object.entries(params ?? {})
  const dialogId = toolUseId ?? `perm-${toolName}`

  return (
    <div
      className="perm-dialog"
      data-run-id={runId}
      data-tool-use-id={toolUseId}
      role="dialog"
      aria-modal="true"
      aria-labelledby={`perm-title-${dialogId}`}
    >
      <div className="perm-header">
        <span className="perm-icon">△</span>
        <span className="perm-title" id={`perm-title-${dialogId}`}>
          权限请求 — {toolName}
        </span>
      </div>
      <div className="perm-preview">
        <code>{preview}</code>
      </div>
      {paramLines.length > 0 && (
        <div className="perm-params">
          {paramLines.map(([k, v]) => (
            <div key={k} className="perm-param-row">
              <span className="perm-param-key">{k}:</span>
              <code className="perm-param-val">{formatParamValue(v)}</code>
            </div>
          ))}
        </div>
      )}
      <div className="perm-buttons">
        <button
          type="button"
          className="perm-btn perm-btn-allow"
          onClick={() => onRespond('allow_once')}
        >
          [Y] 允许一次
        </button>
        <button
          type="button"
          className="perm-btn perm-btn-allow-all"
          onClick={() => onRespond('always_allow')}
        >
          [A] 总是允许
        </button>
        <button
          type="button"
          className="perm-btn perm-btn-deny"
          onClick={() => onRespond('deny_once')}
        >
          [N] 拒绝一次
        </button>
        <button
          type="button"
          className="perm-btn perm-btn-deny-all"
          onClick={() => onRespond('always_deny')}
        >
          [D] 总是拒绝
        </button>
      </div>
    </div>
  )
}