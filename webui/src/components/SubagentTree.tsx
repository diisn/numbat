import { useState } from 'react'

interface SubagentInfo {
  runId: string
  description: string
  /** 与后端事件一致：running | success | failed（见 internal/context/context.go 的 Status） */
  status: string
}

interface SubagentTreeProps {
  subagents: SubagentInfo[]
}

const STATUS_ICONS: Record<string, string> = {
  running: '◐',
  success: '●',
  failed: '✕',
}

const STATUS_COLORS: Record<string, string> = {
  running: 'subagent-running',
  success: 'subagent-completed',
  failed: 'subagent-failed',
}

export function SubagentTree({ subagents }: SubagentTreeProps) {
  const [expanded, setExpanded] = useState(true)

  if (subagents.length === 0) return null

  const running = subagents.filter((s) => s.status === 'running').length
  const succeeded = subagents.filter((s) => s.status === 'success').length
  const failed = subagents.filter((s) => s.status === 'failed').length

  return (
    <div className="subagent-tree">
      <button
        type="button"
        className="subagent-tree-header"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
      >
        <span className="tree-toggle" aria-hidden="true">{expanded ? '▾' : '▸'}</span>
        <span className="tree-label">子 Agent ({subagents.length})</span>
        {running > 0 && <span className="tree-count tree-running">{running} 运行中</span>}
        {succeeded > 0 && <span className="tree-count tree-completed">{succeeded} 完成</span>}
        {failed > 0 && <span className="tree-count tree-failed">{failed} 失败</span>}
      </button>
      {expanded && (
        <div className="subagent-tree-body">
          <div className="tree-connector" />
          {subagents.map((s, i) => (
            <div key={i} className={`tree-node ${STATUS_COLORS[s.status] ?? ''}`}>
              <span className="tree-node-icon">{STATUS_ICONS[s.status] ?? '○'}</span>
              <div className="tree-node-content">
                <span className="tree-node-desc">{s.description}</span>
                <span className="tree-node-runid">{s.runId}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
