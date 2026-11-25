import { useCallback, useEffect, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { TooltipProvider } from '@/components/ui/tooltip'
import TitleBar from './components/TitleBar'
import Sidebar, { SideView } from './components/Sidebar'
import ThreadPanel from './components/ThreadPanel'
import ReviewPanel from './components/ReviewPanel'
import SettingsPanel from './components/SettingsPanel'
import ConfirmDialog from './components/ConfirmDialog'
import { PendingApproval } from './components/ApprovalCard'
import { ChatService } from '../bindings/ai-dog-agent/internal/agent/index.js'
import { WorkspaceService, ApprovalService } from '../bindings/ai-dog-agent/internal/service/index.js'

export interface ThreadInfo {
  id: string
  title: string
  createdAt: number
  lastActive: number
  running: boolean
}

const clamp = (v: number, min: number, max: number) => Math.min(max, Math.max(min, v))

const readBool = (key: string, fallback: boolean) => {
  const v = localStorage.getItem(key)
  return v === null ? fallback : v === '1'
}

const readNum = (key: string, fallback: number) => {
  const v = Number(localStorage.getItem(key))
  return Number.isFinite(v) && v > 0 ? v : fallback
}

function App() {
  const [threads, setThreads] = useState<ThreadInfo[]>([])
  const [activeId, setActiveId] = useState<string>('')
  const [wsPath, setWsPath] = useState<string>('')
  const [page, setPage] = useState<'chat' | 'settings'>('chat')
  const [sideView, setSideView] = useState<SideView>('threads')
  const [reviewTick, setReviewTick] = useState(0)
  const [approvalsByThread, setApprovalsByThread] = useState<Record<string, PendingApproval[]>>({})
  const [settingsDirty, setSettingsDirty] = useState(false)
  const [leaveConfirmOpen, setLeaveConfirmOpen] = useState(false)
  // 面板开关与宽度（持久化到 localStorage）
  const [sidebarOpen, setSidebarOpen] = useState(() => readBool('aidog.sidebarOpen', true))
  const [reviewOpen, setReviewOpen] = useState(() => readBool('aidog.reviewOpen.v2', false))
  const [sidebarWidth, setSidebarWidth] = useState(() => readNum('aidog.sidebarWidth', 240))
  const [reviewWidth, setReviewWidth] = useState(() => readNum('aidog.reviewWidth', 340))
  // 用户如果从未手动设置过审查栏，工作区就绪后自动展开
  const reviewPrefSet = useRef(localStorage.getItem('aidog.reviewOpen.v2') !== null)

  useEffect(() => { localStorage.setItem('aidog.sidebarOpen', sidebarOpen ? '1' : '0') }, [sidebarOpen])
  // 只在用户手动切换过后才持久化审查栏偏好，避免默认值把「自动展开」永久关掉
  useEffect(() => {
    if (reviewPrefSet.current) localStorage.setItem('aidog.reviewOpen.v2', reviewOpen ? '1' : '0')
  }, [reviewOpen])
  useEffect(() => { localStorage.setItem('aidog.sidebarWidth', String(sidebarWidth)) }, [sidebarWidth])
  useEffect(() => { localStorage.setItem('aidog.reviewWidth', String(reviewWidth)) }, [reviewWidth])
  useEffect(() => {
    if (wsPath && !reviewPrefSet.current && window.innerWidth >= 1080) setReviewOpen(true)
  }, [wsPath])

  // 窄窗下（默认偏好未手动设置）自动收起审查栏，避免对话框被压到 300px
  useEffect(() => {
    const mq = window.matchMedia('(max-width: 1080px)')
    const apply = () => {
      if (reviewPrefSet.current) return
      setReviewOpen(mq.matches ? false : !!wsPath)
    }
    apply()
    mq.addEventListener('change', apply)
    return () => mq.removeEventListener('change', apply)
  }, [wsPath])

  const toggleSidebar = () => setSidebarOpen(v => !v)
  const toggleReview = () => { reviewPrefSet.current = true; setReviewOpen(v => !v) }

  // 拖拽分隔条调宽：侧栏向右增宽，审查栏向左增宽
  const startResize = (which: 'sidebar' | 'review', startWidth: number) => (e: React.PointerEvent) => {
    e.preventDefault()
    const startX = e.clientX
    const onMove = (ev: PointerEvent) => {
      if (which === 'sidebar') setSidebarWidth(clamp(startWidth + ev.clientX - startX, 180, 420))
      else setReviewWidth(clamp(startWidth - (ev.clientX - startX), 260, 640))
    }
    const onUp = () => {
      document.body.classList.remove('resizing')
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
    }
    document.body.classList.add('resizing')
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }

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

    // 审批卡片按线程归集；线程列表只在生命周期事件后刷新。
    // chat_delta 每个 token 都会来，绝不能在这里拉 ListSessions（会造成每 token 一次 RPC）。
    const off = Events.On('agent:event', (ev: any) => {
      const e = ev?.data
      if (!e) return
      if (e.type === 'approval_request') {
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
      } else if (e.type === 'approval_decided') {
        setApprovalsByThread(m => {
          const next: Record<string, PendingApproval[]> = {}
          for (const [sid, list] of Object.entries(m)) {
            const rest = list.filter(a => a.id !== e.data?.id)
            if (rest.length > 0) next[sid] = rest
          }
          return next
        })
      }
      if (e.type === 'chat_started' || e.type === 'chat_done' || e.type === 'chat_error' || e.type === 'task_state') {
        refreshThreads()
      }
    })
    return () => { off() }
  }, [refreshThreads])

  const newThread = async () => {
    const id = await ChatService.NewSession()
    setActiveId(id)
    setPage('chat')
    refreshThreads()
  }

  const requestCloseSettings = () => {
    if (settingsDirty) { setLeaveConfirmOpen(true); return }
    setSettingsDirty(false)
    setPage('chat')
  }

  const openSettings = () => {
    if (page === 'settings') requestCloseSettings()
    else setPage('settings')
  }

  const deleteThread = async (id: string) => {
    await ChatService.DeleteSession(id)
    const rest = threads.filter(t => t.id !== id)
    if (id === activeId) {
      if (rest.length > 0) setActiveId(rest[0].id)
      else await newThread()
    }
    setThreads(rest)
    // 删除后把焦点还给剩余的当前线程，避免焦点落空
    requestAnimationFrame(() => {
      (document.querySelector('.thread-item.active') as HTMLElement | null)?.focus()
    })
  }

  const [pendingDelete, setPendingDelete] = useState<ThreadInfo | null>(null)
  const requestDeleteThread = (id: string) => {
    setPendingDelete(threads.find(t => t.id === id) ?? { id, title: '', createdAt: 0, lastActive: 0, running: false })
  }

  // 全局快捷键：Ctrl/Cmd+N 新建线程，Ctrl/Cmd+, 打开/关闭设置，Esc 返回/关闭弹层
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (leaveConfirmOpen) { setLeaveConfirmOpen(false); return }
        if (pendingDelete) { setPendingDelete(null); return }
        if (page === 'settings') requestCloseSettings()
        return
      }
      if (!(e.ctrlKey || e.metaKey)) return
      if (e.key.toLowerCase() === 'n') {
        e.preventDefault()
        newThread()
      } else if (e.key === ',') {
        e.preventDefault()
        openSettings()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [page, settingsDirty, leaveConfirmOpen, pendingDelete])

  const browseWorkspace = async () => {
    try {
      const p = await WorkspaceService.BrowseWorkspace()
      if (p) { setWsPath(p); setReviewTick(t => t + 1) }
    } catch { /* 用户取消 */ }
  }

  return (
    <TooltipProvider>
      <div className="app">
        <TitleBar
          sidebarOpen={sidebarOpen}
          reviewOpen={reviewOpen}
          onToggleSidebar={toggleSidebar}
          onToggleReview={toggleReview}
        />
        <div
          className="app-body"
          style={{ '--sidebar-w': `${sidebarWidth}px`, '--review-w': `${reviewWidth}px` } as React.CSSProperties}
        >
          {page === 'settings' ? (
            <div className="settings-page">
              <header className="settings-page-header">
                <div className="settings-page-title">
                  <h2>设置</h2>
                  {settingsDirty && <span className="settings-unsaved">未保存</span>}
                </div>
                <Button variant="ghost" size="sm" onClick={requestCloseSettings}>
                  <ArrowLeft className="size-3.5" /> 返回对话
                </Button>
              </header>
              <div className="settings-page-body">
                <SettingsPanel onDirtyChange={setSettingsDirty} />
              </div>
            </div>
          ) : (
            <>
              {sidebarOpen && (
                <>
                  <Sidebar
                    threads={threads}
                    activeId={activeId}
                    wsPath={wsPath}
                    sideView={sideView}
                    onSideView={setSideView}
                    onSelect={id => { setActiveId(id); setPage('chat') }}
                    onNew={newThread}
                    onDelete={requestDeleteThread}
                    onBrowseWorkspace={browseWorkspace}
                    onOpenSettings={openSettings}
                  />
                  <div
                    className="resize-handle"
                    role="separator"
                    aria-orientation="vertical"
                    aria-label="调整侧栏宽度"
                    onPointerDown={startResize('sidebar', sidebarWidth)}
                  />
                </>
              )}
              <main className="thread-col">
                <ThreadPanel
                  activeId={activeId}
                  activeTitle={threads.find(t => t.id === activeId)?.title ?? ''}
                  wsPath={wsPath}
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
              {reviewOpen && (
                <>
                  <div
                    className="resize-handle"
                    role="separator"
                    aria-orientation="vertical"
                    aria-label="调整审查栏宽度"
                    onPointerDown={startResize('review', reviewWidth)}
                  />
                  <ReviewPanel wsPath={wsPath} refreshTick={reviewTick} />
                </>
              )}
            </>
          )}
        </div>
      </div>
      <ConfirmDialog
        open={pendingDelete !== null}
        title="删除线程？"
        description={pendingDelete
          ? `「${pendingDelete.title || '新线程'}」的对话记录将被删除，且无法恢复。`
          : ''}
        confirmLabel="删除"
        destructive
        onConfirm={() => {
          const t = pendingDelete
          setPendingDelete(null)
          if (t) deleteThread(t.id)
        }}
        onCancel={() => setPendingDelete(null)}
      />
      <ConfirmDialog
        open={leaveConfirmOpen}
        title="放弃未保存的设置？"
        description="设置页还有未保存的修改，返回对话后这些修改会丢失。"
        confirmLabel="放弃并返回"
        destructive
        onConfirm={() => {
          setLeaveConfirmOpen(false)
          setSettingsDirty(false)
          setPage('chat')
        }}
        onCancel={() => setLeaveConfirmOpen(false)}
      />
    </TooltipProvider>
  )
}

export default App
