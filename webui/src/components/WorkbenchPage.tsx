import { useCallback, useEffect, useRef, useState } from 'react'
import type { Client } from '@/lib/client'
import type { ContentBlock, PermissionDecision } from '@/types/api'
import { ToolCallCard, type ToolCallState } from '@/components/ToolCallCard'
import { PermissionDialog } from '@/components/PermissionDialog'
import { MarkdownRenderer } from '@/components/MarkdownRenderer'

interface WorkbenchMessage {
  role: 'user' | 'assistant'
  content: ContentBlock[]
  runId?: string
}

interface PendingPermission {
  tool_use_id: string
  tool_name: string
  params: Record<string, unknown>
  preview: string
}

interface ArchivedRun {
  runId: string
  goal: string
  status: string
  summary: string
  timestamp: string
}

interface WorkbenchPageProps {
  client: Client
}

export function WorkbenchPage({ client }: WorkbenchPageProps) {
  const [goal, setGoal] = useState('')
  const [running, setRunning] = useState(false)
  const [messages, setMessages] = useState<WorkbenchMessage[]>([])
  const [toolStates, setToolStates] = useState<Record<string, ToolCallState>>({})
  const [archived, setArchived] = useState<ArchivedRun[]>([])
  const [pendingPermissions, setPendingPermissions] = useState<PendingPermission[]>([])

  const runIdRef = useRef<string>('')
  // 后端 agent.run 是同步阻塞的：run_id 要等 RPC 返回才拿到，
  // 但 run.started 早于此推送，因此先用 goal 匹配提前认领本次 run。
  const pendingGoalRef = useRef('')
  const runGoalRef = useRef('')
  const adoptedRef = useRef(false)
  // 累积本轮助手文本，供归档摘要使用（避免在 setState updater 内做副作用）
  const assistantTextRef = useRef('')
  const scrollRef = useRef<HTMLDivElement>(null)

  // 事件监听
  useEffect(() => {
    const offEvent = client.onEvent((event) => {
      // 未认领时，尝试用 goal 匹配 run.started 绑定 run_id
      if (!runIdRef.current) {
        if (
          event.type === 'run.started' &&
          pendingGoalRef.current &&
          event.goal === pendingGoalRef.current
        ) {
          runIdRef.current = event.run_id
          adoptedRef.current = true
        } else {
          return
        }
      }
      // 只处理当前工作台 run 的事件（session.* 等无 run_id 的事件直接忽略）
      if (!('run_id' in event)) return
      if (event.run_id !== runIdRef.current) return

      switch (event.type) {
        case 'run.started':
          setRunning(true)
          setMessages((prev) => [
            ...prev,
            { role: 'assistant', content: [], runId: event.run_id },
          ])
          break

        case 'llm.token':
          assistantTextRef.current += event.token
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (!last || last.role !== 'assistant') return prev
            const lastBlock = last.content[last.content.length - 1]
            if (lastBlock?.type === 'text') {
              return [
                ...prev.slice(0, -1),
                {
                  ...last,
                  content: [
                    ...last.content.slice(0, -1),
                    { ...lastBlock, text: (lastBlock.text ?? '') + event.token },
                  ],
                },
              ]
            }
            return [
              ...prev.slice(0, -1),
              {
                ...last,
                content: [...last.content, { type: 'text', text: event.token }],
              },
            ]
          })
          break

        case 'tool.call_started':
          setToolStates((prev) => ({
            ...prev,
            [event.tool_use_id]: { status: 'running' },
          }))
          setMessages((prev) => {
            const last = prev[prev.length - 1]
            if (!last || last.role !== 'assistant') return prev
            return [
              ...prev.slice(0, -1),
              {
                ...last,
                content: [
                  ...last.content,
                  { type: 'tool_use', id: event.tool_use_id, name: event.tool_name, input: event.params },
                ],
              },
            ]
          })
          break

        case 'tool.call_finished':
          setToolStates((prev) => ({
            ...prev,
            [event.tool_use_id]: {
              status: 'success',
              output: event.output,
              elapsedMs: event.elapsed_ms,
            },
          }))
          break

        case 'tool.call_failed':
          setToolStates((prev) => ({
            ...prev,
            [event.tool_use_id]: {
              status: 'failed',
              error: event.error,
              errorType: event.error_type,
              elapsedMs: event.elapsed_ms,
            },
          }))
          break

        case 'permission.requested':
          setPendingPermissions((prev) => [
            ...prev,
            {
              tool_use_id: event.tool_use_id,
              tool_name: event.tool_name,
              params: event.params,
              preview: event.preview,
            },
          ])
          break

        case 'permission.granted':
        case 'permission.denied':
          setPendingPermissions((prev) =>
            prev.filter((p) => p.tool_use_id !== event.tool_use_id),
          )
          break

        case 'run.finished': {
          const finishedRunId = runIdRef.current
          setRunning(false)
          setPendingPermissions([])
          if (finishedRunId) {
            setArchived((a) => [
              {
                runId: finishedRunId,
                goal: runGoalRef.current,
                status: event.status,
                summary: assistantTextRef.current.slice(0, 100),
                timestamp: new Date().toLocaleTimeString('zh-CN'),
              },
              ...a,
            ])
          }
          runIdRef.current = ''
          break
        }
      }
    })

    return () => offEvent()
  }, [client])

  // 自动滚动
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
  }, [messages])

  const handleSubmit = useCallback(async () => {
    const text = goal.trim()
    if (!text || running) return
    setGoal('')
    setMessages([{ role: 'user', content: [{ type: 'text', text }] }])
    setToolStates({})
    setPendingPermissions([])
    setRunning(true)
    // 供事件流认领与归档使用
    pendingGoalRef.current = text
    runGoalRef.current = text
    adoptedRef.current = false
    assistantTextRef.current = ''

    try {
      const result = await client.call('agent.run', { goal: text })
      // 兜底：事件流未认领（例如未收到 run.started）时，直接用 RPC 结果渲染
      if (!adoptedRef.current) {
        const out = result.result ?? '(无输出)'
        setMessages((prev) => [
          ...prev,
          { role: 'assistant', content: [{ type: 'text', text: out }], runId: result.run_id },
        ])
        setArchived((a) => [
          {
            runId: result.run_id,
            goal: text,
            status: result.status,
            summary: out.slice(0, 100),
            timestamp: new Date().toLocaleTimeString('zh-CN'),
          },
          ...a,
        ])
        setRunning(false)
      }
    } catch {
      setRunning(false)
      setMessages((prev) => [
        ...prev,
        { role: 'assistant', content: [{ type: 'text', text: '调用失败，请检查连接。' }] },
      ])
    } finally {
      pendingGoalRef.current = ''
    }
  }, [goal, running, client])

  // 权限审批响应
  const handlePermissionRespond = useCallback(
    async (toolUseId: string, decision: PermissionDecision) => {
      setPendingPermissions((prev) => prev.filter((p) => p.tool_use_id !== toolUseId))
      await client.call('permission.respond', { tool_use_id: toolUseId, decision })
    },
    [client],
  )

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSubmit()
    }
  }

  return (
    <div className="workbench-page">
      <h2 className="page-title">agent.run 工作台</h2>
      <p className="workbench-desc">
        无会话一次性任务。发送目标，Agent 执行完毕后归档。
      </p>

      <div className="workbench-input">
        <textarea
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="输入任务目标…（Enter 执行）"
          rows={2}
          disabled={running}
        />
        <button
          type="button"
          onClick={handleSubmit}
          disabled={!goal.trim() || running}
          className={running ? 'btn-running' : ''}
        >
          {running ? '执行中…' : '执行'}
        </button>
      </div>

      {messages.length > 0 && (
        <div className="workbench-messages" ref={scrollRef}>
          {messages.map((msg, i) => (
            <div key={i} className={`message message-${msg.role}`}>
              <span className="role-label">{msg.role === 'user' ? '目标' : 'Agent'}</span>
              {msg.role === 'user' ? (
                <div className="bubble">{msg.content.map((b, j) => <span key={j}>{b.text}</span>)}</div>
              ) : (
                <div className="assistant-content">
                  {msg.content.map((block, j) => {
                    if (block.type === 'text') {
                      return <MarkdownRenderer key={j} content={block.text ?? ''} />
                    }
                    if (block.type === 'tool_use') {
                      return (
                        <ToolCallCard
                          key={j}
                          toolUse={block}
                          state={toolStates[block.id ?? '']}
                        />
                      )
                    }
                    return null
                  })}
                  {running && i === messages.length - 1 && <span className="cursor" />}
                </div>
              )}
            </div>
          ))}

          {pendingPermissions.map((p) => (
            <PermissionDialog
              key={p.tool_use_id}
              toolName={p.tool_name}
              params={p.params}
              preview={p.preview}
              onRespond={(decision) => handlePermissionRespond(p.tool_use_id, decision)}
            />
          ))}
        </div>
      )}

      {archived.length > 0 && (
        <div className="workbench-archived">
          <h3>归档 ({archived.length})</h3>
          {archived.map((r) => (
            <div key={r.runId} className="archived-item">
              <div className="archived-header">
                <span className="archived-goal">{r.goal}</span>
                <span className={`run-status run-status-${r.status}`}>{r.status}</span>
                <span className="archived-time">{r.timestamp}</span>
              </div>
              <p className="archived-summary">{r.summary}</p>
              <span className="archived-runid mono">{r.runId}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
