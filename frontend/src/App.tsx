import { useCallback, useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import TitleBar from './components/TitleBar'
import Sidebar, { SideView } from './components/Sidebar'
import ThreadPanel from './components/ThreadPanel'
import ReviewPanel from './components/ReviewPanel'
import SettingsPanel from './components/SettingsPanel'
import { PendingApproval } from './components/ApprovalCard'
import { ChatService } from '../bindings/ai-dog-agent/internal/agent/index.js'
import { WorkspaceService, ApprovalService, ConfigService } from '../bindings/ai-dog-agent/internal/service/index.js'

export interface ThreadInfo {
  id: string
  title: string
  createdAt: number
  lastActive: number
  running: boolean
}

function App() {
  const [threads, setThreads] = useState<ThreadInfo[]>([])
  const [activeId, setActiveId] = useState<string>('')
  const [wsPath, setWsPath] = useState<string>('')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [sideView, setSideView] = useState<SideView>('threads')
  const [reviewTick, setReviewTick] = useState(0)
  const [approvalsByThread, setApprovalsByThread] = useState<Record<string, PendingApproval[]>>({})
  const [configCounts, setConfigCounts] = useState({ skills: 0, mcp: 0, agents: 0 })

  const refreshThreads = useCallback(async () => {
    try {
      const list = await ChatService.ListSessions()
      setThreads(list as ThreadInfo[])
    } catch { /* 忽略 */ }
  }, [])

  useEffect(() => {
    // 初始化：创建首个线程并载入历史
    ChatService.ListSessions().then(async (list: ThreadInfo[] | null) => {
      if (list && list.length > 0) {
        setThreads(list)
        setActiveId(list[0].id)
      } else {
        const id = await ChatService.NewSession()
        setActiveId(id)
        refreshThreads()
      }
    })
    WorkspaceService.GetWorkspace().then(setWsPath)
    // 对账：恢复重启/重载前遗留的待审批请求
    ApprovalService.ListPending().then((pending: any[] | null) => {
      if (!pending?.length) return
      setApprovalsByThread(m => {
        const next = { ...m }
        for (const p of pending) {
          const sid = p.sessionId ?? ''
          if (!sid) continue
          const list = next[sid] ?? []
          if (!list.some(a => a.id === p.id)) {
            next[sid] = [...list, { id: p.id, tool: p.tool, path: p.path, preview: p.preview }]
          }
        }
        return next
      })
    })

    // 任意 Agent 事件后刷新线程列表（running 状态、活跃时间）+ 审批卡片按线程归集
    const off = Events.On('agent:event', (ev: any) => {
      const e = ev?.data
      if (e?.type === 'approval_request') {
        const item: PendingApproval = {
          id: e.data?.id ?? '',
          tool: e.data?.tool ?? '',
          path: e.data?.path ?? '',
          preview: e.data?.preview ?? '',
        }
        setApprovalsByThread(m => {
          const list = m[e.sessionId] ?? []
          if (list.some(a => a.id === item.id)) return m
          return { ...m, [e.sessionId]: [...list, item] }
        })
      } else if (e?.type === 'approval_decided') {
        setApprovalsByThread(m => {
          const next: Record<string, PendingApproval[]> = {}
          for (const [sid, list] of Object.entries(m)) {
            const rest = list.filter(a => a.id !== e.data?.id)
            if (rest.length > 0) next[sid] = rest
          }
          return next
        })
      }
      refreshThreads()
    })
    return () => { off() }
  }, [refreshThreads])

  const newThread = async () => {
    const id = await ChatService.NewSession()
    setActiveId(id)
    refreshThreads()
  }

  const deleteThread = async (id: string) => {
    await ChatService.DeleteSession(id)
    const rest = threads.filter(t => t.id !== id)
    if (id === activeId) {
      if (rest.length > 0) setActiveId(rest[0].id)
      else await newThread()
    }
    setThreads(rest)
  }

  // 设置弹层关闭 / 工作区切换后刷新侧边栏配置计数
  useEffect(() => {
    if (settingsOpen) return
    Promise.all([
      ConfigService.GetConfig(),
      ConfigService.GetProjectAgents().catch(() => []),
    ]).then(([c, agents]: [any, any]) => setConfigCounts({
      skills: (c?.skills ?? []).filter((s: any) => s.enabled).length,
      mcp: (c?.mcpServers ?? []).filter((m: any) => m.enabled).length,
      agents: (agents ?? []).length,
    })).catch(() => { /* 忽略 */ })
  }, [settingsOpen, wsPath])

  const browseWorkspace = async () => {
    try {
      const p = await WorkspaceService.BrowseWorkspace()
      if (p) { setWsPath(p); setReviewTick(t => t + 1) }
    } catch { /* 用户取消 */ }
  }

  return (
    <div className="app">
      <TitleBar />
      <Sidebar
        threads={threads}
        activeId={activeId}
        wsPath={wsPath}
        sideView={sideView}
        onSideView={setSideView}
        onSelect={setActiveId}
        onNew={newThread}
        onDelete={deleteThread}
        onBrowseWorkspace={browseWorkspace}
        onOpenSettings={() => setSettingsOpen(true)}
        configCounts={configCounts}
      />
      <main className="thread-col">
        <ThreadPanel
          activeId={activeId}
          activeTitle={threads.find(t => t.id === activeId)?.title ?? ''}
          wsPath={wsPath}
          onThreadsChanged={refreshThreads}
          pendingApprovals={approvalsByThread[activeId] ?? []}
          onApprovalDone={id => setApprovalsByThread(m => {
            const next: Record<string, PendingApproval[]> = {}
            for (const [sid, list] of Object.entries(m)) {
              const rest = list.filter(a => a.id !== id)
              if (rest.length > 0) next[sid] = rest
            }
            return next
          })}
        />
      </main>
      <ReviewPanel wsPath={wsPath} refreshTick={reviewTick} />
      {settingsOpen && (
        <div className="overlay" onClick={() => setSettingsOpen(false)}>
          <div className="overlay-panel" onClick={e => e.stopPropagation()}>
            <div className="overlay-header">
              <span>设置</span>
              <button className="btn ghost" onClick={() => setSettingsOpen(false)}>✕ 关闭</button>
            </div>
            <div className="overlay-body">
              <SettingsPanel />
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default App
