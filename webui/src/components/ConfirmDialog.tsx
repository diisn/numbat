import { useEffect } from 'react'

interface ConfirmDialogProps {
  title: string
  message: string
  confirmText?: string
  cancelText?: string
  onConfirm: () => void
  onCancel: () => void
}

// 自定义确认框：替代 window.confirm（避免与 Neo-Brutalism 主题脱节）
export function ConfirmDialog({
  title,
  message,
  confirmText = '确定',
  cancelText = '取消',
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  // Enter 确认 / Esc 取消
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Enter') onConfirm()
      if (e.key === 'Escape') onCancel()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onConfirm, onCancel])

  return (
    <div
      className="modal-overlay"
      onClick={onCancel}
      role="dialog"
      aria-modal="true"
      aria-labelledby="confirm-title"
    >
      <div className="modal-card confirm-card" onClick={(e) => e.stopPropagation()}>
        <h2 className="modal-title" id="confirm-title">{title}</h2>
        <p className="confirm-message">{message}</p>
        <div className="confirm-buttons">
          <button type="button" className="confirm-btn confirm-btn-danger" onClick={onConfirm}>
            {confirmText}
          </button>
          <button type="button" className="confirm-btn" onClick={onCancel}>
            {cancelText}
          </button>
        </div>
      </div>
    </div>
  )
}