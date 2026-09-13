// 斜杠命令自动补全弹窗（对齐 TUI 的 slash 补全语义）。
// 输入以 "/" 开头且不含空格时弹出候选列表，↑/↓ 选择，Enter/Tab 选中，Esc 关闭。
import { useEffect, useRef } from 'react'

export interface SlashItem {
  name: string
  desc: string
  kind: 'command' | 'skill'
}

interface SlashAutocompleteProps {
  visible: boolean
  items: SlashItem[]
  cursor: number
  onSelect: (item: SlashItem) => void
}

const MAX_VISIBLE = 8

export function SlashAutocomplete({
  visible,
  items,
  cursor,
  onSelect,
}: SlashAutocompleteProps) {
  const listRef = useRef<HTMLDivElement>(null)

  // 光标移动时确保选中项在可视区内
  useEffect(() => {
    if (!visible) return
    const el = listRef.current?.querySelector<HTMLElement>('.slash-item.active')
    el?.scrollIntoView({ block: 'nearest' })
  }, [cursor, visible])

  if (!visible) return null

  return (
    <div className="slash-popup">
      <div className="slash-popup-header">斜杠命令 / 技能（↑↓ 选择，Enter/Tab 确认，Esc 关闭）</div>
      <div className="slash-popup-list" ref={listRef}>
        {items.map((item, i) => (
          <div
            key={`${item.kind}:${item.name}`}
            className={`slash-item ${i === cursor ? 'active' : ''}`}
            onMouseDown={(e) => {
              // 用 onMouseDown 防止 textarea 先失焦导致 onKeyDown 分支失效
              e.preventDefault()
              onSelect(item)
            }}
          >
            <span className={`slash-item-kind slash-item-kind-${item.kind}`}>
              {item.kind === 'command' ? '命令' : '技能'}
            </span>
            <span className="slash-item-name">/{item.name}</span>
            <span className="slash-item-desc">{item.desc}</span>
          </div>
        ))}
        {items.length > MAX_VISIBLE && (
          <div className="slash-popup-more">… 共 {items.length} 项</div>
        )}
      </div>
    </div>
  )
}
