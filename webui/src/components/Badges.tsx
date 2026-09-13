interface UsageBadgeProps {
  inputTokens: number
  outputTokens: number
  contextPct: number
}

export function UsageBadge({ inputTokens, outputTokens, contextPct }: UsageBadgeProps) {
  // 契约：context_pct 为 0.0–1.0 的小数（后端 anthropic.go: inputTokens/contextWindow）
  const ctxColor = contextPct >= 0.85 ? 'var(--err)' : contextPct >= 0.7 ? 'var(--warn)' : 'var(--ok)'
  return (
    <span
      className="usage-badge"
      title={`输入 ${inputTokens} tokens，输出 ${outputTokens} tokens，上下文占用 ${(contextPct * 100).toFixed(1)}%`}
    >
      <span className="usage-tokens">IN {inputTokens} · OUT {outputTokens}</span>
      <span className="usage-ctx" style={{ color: ctxColor }}>
        CTX {(contextPct * 100).toFixed(1)}%
      </span>
    </span>
  )
}

interface SkillBadgeProps {
  skillName: string
}

export function SkillBadge({ skillName }: SkillBadgeProps) {
  return (
    <span className="skill-badge">
      <span className="skill-icon">→</span>
      Skill: {skillName}
    </span>
  )
}

interface CompactionBadgeProps {
  originalTokens: number
  summaryTokens: number
}

export function CompactionBadge({ originalTokens, summaryTokens }: CompactionBadgeProps) {
  // originalTokens 为 0 时跳过比例计算，避免除零显示 −Infinity%
  const ratio =
    summaryTokens > 0 && originalTokens > 0
      ? Math.round((1 - summaryTokens / originalTokens) * 100)
      : 0
  return (
    <span className="compaction-badge">
      <span className="compaction-icon">▣</span>
      上下文已压缩 {originalTokens} → {summaryTokens} tokens (−{ratio}%)
    </span>
  )
}
