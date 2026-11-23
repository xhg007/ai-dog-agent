import { ThreadInfo } from '../App'
import TaskPanel from './TaskPanel'

export type SideView = 'threads' | 'tasks'

interface Props {
  threads: ThreadInfo[]
  activeId: string
  wsPath: string
  sideView: SideView
  onSideView: (v: SideView) => void
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (id: string) => void
  onBrowseWorkspace: () => void
  onOpenSettings: () => void
  configCounts: { skills: number; mcp: number; agents: number }
}

function shortPath(p: string): string {
  if (!p) return ''
  const parts = p.split(/[\\/]/).filter(Boolean)
  return parts.length > 1 ? parts[parts.length - 1] : p
}

function timeLabel(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  const today = new Date()
  const sameDay = d.toDateString() === today.toDateString()
  if (sameDay) return d.toTimeString().slice(0, 5)
  return `${d.getMonth() + 1}/${d.getDate()}`
}

export default function Sidebar({ threads, activeId, wsPath, sideView, onSideView, onSelect, onNew, onDelete, onBrowseWorkspace, onOpenSettings, configCounts }: Props) {
  return (
    <aside className="sidebar">
      <div className="brand">
        <div className="brand-logo">AI</div>
        <div className="brand-name">
          ai-dog-agent
          <small>多 Agent 编码助手</small>
        </div>
      </div>

      <div className="side-section">
        <div className="side-label">项目</div>
        <button className="project-chip" onClick={onBrowseWorkspace} title={wsPath || '选择项目目录'}>
          <span className="project-dot" />
          <span className="project-name">{wsPath ? shortPath(wsPath) : '选择项目目录'}</span>
        </button>
      </div>

      <div className="side-section threads-section">
        <div className="side-view-tabs">
          <button
            className={`side-view-tab${sideView === 'threads' ? ' active' : ''}`}
            onClick={() => onSideView('threads')}
          >线程</button>
          <button
            className={`side-view-tab${sideView === 'tasks' ? ' active' : ''}`}
            onClick={() => onSideView('tasks')}
          >任务</button>
          {sideView === 'threads' && (
            <button className="side-add" onClick={onNew} title="新建线程">＋</button>
          )}
        </div>

        {sideView === 'threads' ? (
        <>
        <div className="thread-list">
          {threads.map(t => (
            <div
              key={t.id}
              className={`thread-item${t.id === activeId ? ' active' : ''}`}
              onClick={() => onSelect(t.id)}
            >
              <div className="thread-title">
                {t.running && <span className="running-dot" />}
                {t.title || '新线程'}
              </div>
              <div className="thread-meta">
                <span>{timeLabel(t.lastActive)}</span>
                <button
                  className="thread-del"
                  title="删除线程"
                  onClick={e => { e.stopPropagation(); onDelete(t.id) }}
                >✕</button>
              </div>
            </div>
          ))}
        </div>
        <div className="side-placeholder" title="在设置中配置">
          Skills {configCounts.skills} · MCP {configCounts.mcp} · Agents {configCounts.agents}
        </div>
        </>
        ) : (
          <TaskPanel onOpenThread={onSelect} />
        )}
      </div>

      <div className="side-footer">
        <button className="side-btn" onClick={onOpenSettings}>⚙ 设置</button>
        <span className="side-version">v0.2</span>
      </div>
    </aside>
  )
}
