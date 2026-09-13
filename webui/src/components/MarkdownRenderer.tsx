import { memo, useState, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeHighlight from 'rehype-highlight'

interface MarkdownRendererProps {
  content: string
}

// 代码块 + 复制按钮
function CodeBlock({ children, className }: { children?: React.ReactNode; className?: string }) {
  const [copied, setCopied] = useState(false)
  // className 形如 "language-ts"，提取语言名做标签
  const lang = className?.match(/language-([\w+-]+)/)?.[1] ?? 'text'

  const handleCopy = useCallback(() => {
    const text = typeof children === 'string' ? children : extractText(children)
    // 兜底复制：非安全上下文或 API 不可用时退回 execCommand
    const legacyCopy = () => {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      try {
        document.execCommand('copy')
      } catch (err) {
        console.warn('[copy] 兜底复制失败：', err)
      }
      document.body.removeChild(ta)
    }
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).catch(legacyCopy)
    } else {
      legacyCopy()
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [children])

  return (
    <div className="code-block">
      <div className="code-block-bar">
        <span className="code-block-lang">{lang}</span>
        <button type="button" className="code-copy-btn" onClick={handleCopy} aria-label="复制代码到剪贴板">
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <pre className={className}>
        <code>{children}</code>
      </pre>
    </div>
  )
}

// 从 React 子节点提取纯文本
function extractText(node: React.ReactNode): string {
  if (typeof node === 'string') return node
  if (Array.isArray(node)) return node.map(extractText).join('')
  if (node && typeof node === 'object' && 'props' in node) {
    const props = (node as { props?: { children?: React.ReactNode } }).props
    return extractText(props?.children)
  }
  return ''
}

export const MarkdownRenderer = memo(function MarkdownRenderer({ content }: MarkdownRendererProps) {
  return (
    <div className="markdown-body">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeHighlight]}
        components={{
          pre: ({ children }) => <>{children}</>,
          code: ({ className, children, ...props }) => {
            const isBlock = className?.includes('language-')
            if (isBlock) {
              return <CodeBlock className={className}>{children}</CodeBlock>
            }
            return (
              <code className="inline-code" {...props}>
                {children}
              </code>
            )
          },
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noopener noreferrer">
              {children}
            </a>
          ),
          table: ({ children }) => <table className="md-table">{children}</table>,
          blockquote: ({ children }) => <blockquote className="md-quote">{children}</blockquote>,
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
})
