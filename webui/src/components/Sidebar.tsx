import { useState } from 'react'
import type { Session, SkillMeta } from '@/types/api'

interface SidebarProps {
  sessions: Session[]
  activeSessionId: string | null
  skills: SkillMeta[]
  onSelectSession: (id: string) => void
  onNewSession: () => void
  onCloseSession: (id: string) => void
  onClearSession: () => void
  onSkillClick: (skillName: string) => void
}

type Tab = 'sessions' | 'skills'

export function Sidebar({
  sessions,
  activeSessionId,
  skills,
  onSelectSession,
  onNewSession,
  onCloseSession,
  onClearSession,
  onSkillClick,
}: SidebarProps) {
  const [tab, setTab] = useState<Tab>('sessions')
  const [search, setSearch] = useState('')

  const filteredSessions = search
    ? sessions.filter((s) =>
        s.title.toLowerCase().includes(search.toLowerCase()),
      )
    : sessions

  return (
    <aside className="sidebar">
      <div className="sidebar-tabs">
        <button
          type="button"
          className={tab === 'sessions' ? 'tab active' : 'tab'}
          onClick={() => setTab('sessions')}
        >
          会话 ({sessions.length})
        </button>
        <button
          type="button"
          className={tab === 'skills' ? 'tab active' : 'tab'}
          onClick={() => setTab('skills')}
        >
          Skills ({skills.length})
        </button>
      </div>

      <div className="sidebar-body">
        {tab === 'sessions' && (
          <div className="session-list">
            <div className="session-toolbar">
              <button type="button" className="new-session-btn" onClick={onNewSession}>
                + 新建会话
              </button>
              {activeSessionId && (
                <button
                  type="button"
                  className="clear-session-btn"
                  onClick={onClearSession}
                  title="清空当前会话消息"
                >
                  清空
                </button>
              )}
            </div>
            <input
              type="text"
              className="session-search"
              placeholder="搜索会话…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            {filteredSessions.length === 0 && (
              <div className="sidebar-empty">
                {search ? '无匹配会话' : '暂无会话'}
              </div>
            )}
            {filteredSessions.map((s) => (
              <div
                key={s.id}
                className={`session-item ${s.id === activeSessionId ? 'active' : ''}`}
                onClick={() => onSelectSession(s.id)}
              >
                <div className="session-row">
                  <span className="session-title">{s.title}</span>
                  {s.status !== 'closed' && (
                    <button
                      type="button"
                      className="session-close"
                      onClick={(e) => {
                        e.stopPropagation()
                        onCloseSession(s.id)
                      }}
                    >
                      x
                    </button>
                  )}
                </div>
                <div className="session-meta">
                  <span className={`session-status-dot session-status-${s.status}`} />
                  <span className="session-mode">{s.mode}</span>
                  <span className="session-updated">
                    {new Date(s.updated_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}

        {tab === 'skills' && (
          <div className="skill-list">
            {skills.map((s) => (
              <div
                key={s.name}
                className="skill-item skill-clickable"
                onClick={() => onSkillClick(s.name)}
                title={`点击以 /${s.name} 发送`}
              >
                <div className="skill-header">
                  <span className="skill-name">/{s.name}</span>
                </div>
                <p className="skill-desc">{s.description}</p>
                <div className="skill-tools">
                  {s.allowed_tools.map((t) => (
                    <span key={t} className="skill-tool-tag">{t}</span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </aside>
  )
}
