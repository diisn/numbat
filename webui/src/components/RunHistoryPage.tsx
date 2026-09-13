// 运行记录页：统计卡 + 可展开 run 列表（合并原 RunHistoryPage + UsageDashboard）
import { useState } from 'react'
import type { RunSummary } from '@/types/api'

interface RunHistoryPageProps {
  runs: RunSummary[]
}

const STATUS_STYLES: Record<string, string> = {
  success: 'run-status-success',
  failed: 'run-status-failed',
  running: 'run-status-running',
}

const STATUS_LABELS: Record<string, string> = {
  success: '已完成',
  failed: '失败',
  running: '运行中',
}

function formatTime(ts: string): string {
  return new Date(ts).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  return `${(ms / 60000).toFixed(1)}min`
}

export function RunHistoryPage({ runs }: RunHistoryPageProps) {
  // 防御：后端 run.list 在无记录/目录缺失时可能返回 null，一律兜底为空数组
  const safeRuns = runs ?? []
  const [expandedId, setExpandedId] = useState<string | null>(safeRuns[0]?.run_id ?? null)

  // 统计汇总
  const totalInput = safeRuns.reduce((sum, r) => sum + r.tokens.input_tokens, 0)
  const totalOutput = safeRuns.reduce((sum, r) => sum + r.tokens.output_tokens, 0)
  const totalToolCalls = safeRuns.reduce((sum, r) => sum + r.tools.length, 0)
  const totalSubagents = safeRuns.reduce((sum, r) => sum + r.subagents.length, 0)
  const avgContext = safeRuns.length
    ? safeRuns.reduce((sum, r) => sum + r.tokens.context_pct, 0) / safeRuns.length
    : 0

  return (
    <div className="page-container">
      <header className="page-header">
        <h2>运行记录</h2>
        <div className="page-stats">
          <span className="page-stat">{safeRuns.length} 次运行</span>
          <span className="page-stat">{totalToolCalls} 次工具调用</span>
          <span className="page-stat">{totalSubagents} 个子 Agent</span>
          <span className="page-stat">{(totalInput + totalOutput).toLocaleString()} tokens</span>
        </div>
      </header>

      {/* 统计卡 */}
      <div className="usage-stats">
        <div className="stat-card">
          <span className="stat-label">输入 TOKEN</span>
          <span className="stat-value">{totalInput.toLocaleString()}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">输出 TOKEN</span>
          <span className="stat-value">{totalOutput.toLocaleString()}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">工具调用</span>
          <span className="stat-value">{totalToolCalls}</span>
        </div>
        <div className="stat-card">
          <span className="stat-label">平均 CTX</span>
          <span className="stat-value">{(avgContext * 100).toFixed(1)}%</span>
        </div>
      </div>

      {/* Context 占比进度条 */}
      <div className="context-bar-container">
        <h3>CONTEXT 占比</h3>
        <div className="context-bar-track">
          <div
            className="context-bar-fill"
            style={{ width: `${avgContext * 100}%` }}
            data-warning={avgContext > 0.7}
          />
        </div>
        {avgContext > 0.7 && (
          <p className="context-warning">Context 接近阈值，建议压缩对话历史</p>
        )}
      </div>

      {/* 按 Run 明细表 */}
      <div className="usage-run-list">
        <h3>按 RUN 明细</h3>
        <table className="usage-table">
          <thead>
            <tr>
              <th>Run ID</th>
              <th>状态</th>
              <th>输入</th>
              <th>输出</th>
              <th>Context%</th>
              <th>工具数</th>
              <th>耗时</th>
            </tr>
          </thead>
          <tbody>
            {safeRuns.map((r) => (
              <tr key={r.run_id}>
                <td className="mono">{r.run_id}</td>
                <td>
                  <span className={`run-status run-status-${r.status}`}>
                    {r.status}
                  </span>
                </td>
                <td className="mono">{r.tokens.input_tokens.toLocaleString()}</td>
                <td className="mono">{r.tokens.output_tokens.toLocaleString()}</td>
                <td>
                  <div className="mini-bar">
                    <div
                      className="mini-bar-fill"
                      style={{ width: `${r.tokens.context_pct * 100}%` }}
                    />
                  </div>
                </td>
                <td className="mono">{r.tools.length}</td>
                <td className="mono">{r.duration_ms ? `${(r.duration_ms / 1000).toFixed(1)}s` : '--'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* 可展开 run 列表 */}
      <div className="run-list">
        {safeRuns.map((run) => {
          const isExpanded = expandedId === run.run_id
          return (
            <div key={run.run_id} className={`run-card ${isExpanded ? 'expanded' : ''}`}>
              <div
                className="run-card-header"
                onClick={() => setExpandedId(isExpanded ? null : run.run_id)}
              >
                <div className="run-card-left">
                  <span className={`run-status-dot ${STATUS_STYLES[run.status]}`} />
                  <span className="run-goal">{run.goal}</span>
                  {run.skill && <span className="run-skill-tag">/{run.skill}</span>}
                </div>
                <div className="run-card-right">
                  <span className="run-card-time">{formatTime(run.started_at)}</span>
                  <span className="run-card-duration">{formatDuration(run.duration_ms)}</span>
                  <span className="run-card-tokens">
                    {(run.tokens.input_tokens + run.tokens.output_tokens).toLocaleString()} tok
                  </span>
                  <span className="run-card-tools">{run.tools.length} tools</span>
                  <span className={`run-card-status ${STATUS_STYLES[run.status]}`}>
                    {STATUS_LABELS[run.status]}
                  </span>
                  <span className="run-card-chevron">{isExpanded ? 'v' : '>'}</span>
                </div>
              </div>

              {isExpanded && (
                <div className="run-detail">
                  {/* 工具调用 */}
                  {run.tools.length > 0 && (
                    <div className="run-section">
                      <h4>工具调用 ({run.tools.length})</h4>
                      <div className="run-tools-grid">
                        {run.tools.map((t) => (
                          <div key={t.tool_use_id} className="run-tool-chip">
                            <span className={`tool-chip-status ${t.status}`} />
                            <span className="tool-chip-name">{t.tool_name}</span>
                            <span className="tool-chip-time">{formatDuration(t.elapsed_ms)}</span>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* 子 Agent */}
                  {run.subagents.length > 0 && (
                    <div className="run-section">
                      <h4>子 Agent ({run.subagents.length})</h4>
                      <div className="run-subagents">
                        {run.subagents.map((s) => (
                          <div key={s.run_id} className="run-subagent-chip">
                            <span className="subagent-status-dot" />
                            <span className="subagent-desc">{s.description}</span>
                            <span className="subagent-status-text">{s.status}</span>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* 事件时间线 */}
                  <div className="run-section">
                    <h4>事件时间线 ({run.events.length})</h4>
                    <div className="event-timeline">
                      {run.events.map((e, i) => (
                        <div key={i} className="event-row">
                          <span className="event-time">{formatTime(e.ts)}</span>
                          <span className="event-type">{e.type}</span>
                          <span className="event-detail">{e.detail}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
