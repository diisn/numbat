import { useCallback, useRef, useState } from 'react'
import type { ContentBlock } from '@/types/api'

export interface ToolCallState {
  status: 'running' | 'success' | 'failed' | 'aborted'
  output?: string
  error?: string
  errorType?: string
  elapsedMs?: number
}

interface ToolCallCardProps {
  toolUse: ContentBlock
  state?: ToolCallState
}

// 几何符号图标（避免 emoji 跨平台渲染不一致）
const TOOL_ICONS: Record<string, string> = {
  read_file: '▤',
  write_file: '✎',
  bash: '≻_',
  list_dir: '▦',
  spawn_agent: '◈',
  task_create: '+ ',
  task_update: '↻',
  note_save: '◫',
}

function formatParam(key: string, value: unknown): string {
  if (key === 'content' && typeof value === 'string') {
    return value.length > 100 ? value.slice(0, 100) + '…' : value
  }
  if (typeof value === 'string') return value
  if (typeof value === 'number') return String(value)
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

export function ToolCallCard({ toolUse, state }: ToolCallCardProps) {
  const [expanded, setExpanded] = useState(false)
  const name = toolUse.name ?? 'unknown'
  const input = toolUse.input ?? {}
  const icon = TOOL_ICONS[name] ?? '◆'
  const status = state?.status ?? 'running'

  const statusClass = `tool-status tool-status-${status}`
  const statusLabel =
    status === 'running'
      ? '执行中…'
      : status === 'success'
        ? '完成'
        : status === 'aborted'
          ? '已中止'
          : '失败'

  // 空输出（"" / null）也算有输出，展开后可见
  const output = state?.output ?? state?.error
  const hasOutput = output !== undefined && output !== null
  const hasParams = Object.keys(input).length > 0
  const expandable = hasOutput || hasParams
  const contentId = `tool-body-${toolUse.id ?? name}`

  // 输出内嵌滚动指示器（对齐 TUI 的 [start-end/total] 语义）
  const outputRef = useRef<HTMLPreElement>(null)
  const [scrollInfo, setScrollInfo] = useState<{ start: number; end: number; total: number } | null>(null)

  const handleOutputScroll = useCallback(() => {
    const el = outputRef.current
    if (!el) return
    const lineHeight = parseFloat(window.getComputedStyle(el).lineHeight) || 18
    const total = Math.max(1, Math.round(el.scrollHeight / lineHeight))
    const start = Math.floor(el.scrollTop / lineHeight) + 1
    const visible = Math.max(1, Math.ceil(el.clientHeight / lineHeight))
    setScrollInfo({
      start,
      end: Math.min(total, start + visible - 1),
      total,
    })
  }, [])

  return (
    <div className={`tool-card tool-card-${status}`}>
      <button
        type="button"
        className="tool-card-header"
        onClick={() => expandable && setExpanded((e) => !e)}
        disabled={!expandable}
        aria-expanded={expandable ? expanded : undefined}
        aria-controls={expandable ? contentId : undefined}
        title={expandable ? (expanded ? '折叠' : '展开详情') : '无详情'}
      >
        <span className="tool-icon">{icon}</span>
        <span className="tool-name">{name}</span>
        <span className={statusClass} role="status">
          {status === 'running' && <span className="tool-spinner" aria-label="执行中" role="status" />}
          {statusLabel}
        </span>
        {state?.elapsedMs != null && status !== 'running' && (
          <span className="tool-elapsed">{state.elapsedMs}ms</span>
        )}
        {expandable && (
          <span className="tool-expand" aria-hidden="true">
            {expanded ? '−' : '+'}
          </span>
        )}
      </button>

      {expanded && (
        <div className="tool-card-body" id={contentId}>
          {hasParams && (
            <div className="tool-params">
              {Object.entries(input).map(([k, v]) => (
                <div key={k} className="tool-param-row">
                  <span className="tool-param-key">{k}:</span>
                  <code className="tool-param-val">{formatParam(k, v)}</code>
                </div>
              ))}
            </div>
          )}
          {hasOutput && (
            <>
              <pre
                ref={outputRef}
                onScroll={handleOutputScroll}
                className={`tool-output ${state?.error ? 'tool-output-error' : ''}`}
              >
                {output}
              </pre>
              {scrollInfo && scrollInfo.total > 1 && (
                <div className="tool-scroll-indicator">
                  显示 {scrollInfo.start}-{scrollInfo.end} / 共 {scrollInfo.total} 行
                </div>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}