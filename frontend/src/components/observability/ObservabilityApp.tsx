import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Events, Window } from '@wailsio/runtime'
import { Activity, Pause, Play, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  applyEvent,
  applySnapshot,
  emptyState,
  formatClock,
  formatCost,
  formatDuration,
  formatTokens,
  sessionAgents,
  activeSessions,
  recentTasks,
  STATUS_LABEL,
  TASK_STATUS_LABEL,
  type AgentEvent,
  type AgentNode,
  type EventKind,
  type ObsState,
  type SessionNode,
  type StreamEvent,
  type TaskNode,
} from '@/lib/observability'
import { ObservabilityService } from '../../../bindings/ai-dog-agent/internal/service/index.js'

// 观测窗口：同一 bundle 由 main.tsx 按 ?view=observability 分流渲染。
// 实时数据订阅 Wails 全局事件 agent:event；打开时先拉一次 Snapshot 建立起点。

const FLUSH_MS = 100

export default function ObservabilityApp() {
  const { state, sync, lastSynced, syncError } = useObservability()
  return (
    <div className="obs-app">
      <ObsTitleBar />
      <ObsSummary state={state} sync={sync} lastSynced={lastSynced} syncError={syncError} />
      <div className="obs-main">
        <ObsTopology state={state} />
        <ObsStream events={state.events} />
      </div>
      <ObsTasks tasks={state.tasks} />
    </div>
  )
}

// useObservability 订阅事件并按 100ms 批量提交，避免逐 token 更新整棵 UI。
function useObservability() {
  const [state, setState] = useState<ObsState>(emptyState)
  const [lastSynced, setLastSynced] = useState(0)
  const [syncError, setSyncError] = useState('')
  const pending = useRef<AgentEvent[]>([])
  const timer = useRef<number | null>(null)

  const flush = useCallback(() => {
    const batch = pending.current
    pending.current = []
    if (!batch.length) return
    setState(prev => batch.reduce((acc, ev) => applyEvent(acc, ev), prev))
  }, [])

  useEffect(() => {
    const off = Events.On('agent:event', (raw: any) => {
      const ev = raw?.data as AgentEvent | undefined
      if (!ev?.type) return
      pending.current.push(ev)
      // 极端堆积时丢弃最旧的待处理事件，保证界面不落后于当前状态
      if (pending.current.length > 400) pending.current.shift()
      if (timer.current === null) {
        timer.current = window.setTimeout(() => {
          timer.current = null
          flush()
        }, FLUSH_MS)
      }
    })
    return () => {
      off()
      if (timer.current !== null) {
        window.clearTimeout(timer.current)
        timer.current = null
      }
      flush()
    }
  }, [flush])

  const sync = useCallback(async () => {
    try {
      const snap = await ObservabilityService.Snapshot()
      setState(prev => applySnapshot(prev, snap, Date.now()))
      setLastSynced(Date.now())
      setSyncError('')
    } catch (err: any) {
      setSyncError(typeof err === 'string' ? err : err?.message ?? String(err))
    }
  }, [])

  useEffect(() => { void sync() }, [sync])

  // 窗口重新获得焦点时对账一次（主窗口可能新建了线程或 Agent）
  useEffect(() => {
    const onFocus = () => { void sync() }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [sync])

  return { state, sync, lastSynced, syncError }
}

function ObsSummary({ state, sync, lastSynced, syncError }: {
  state: ObsState
  sync: () => void
  lastSynced: number
  syncError: string
}) {
  const tokens = state.totals.tokensIn + state.totals.tokensOut
  return (
    <div className="obs-summary" aria-label="观测汇总">
      <div className="obs-chip is-running">运行中 <strong>{state.totals.running}</strong></div>
      <div className="obs-chip is-blocked">阻塞 <strong>{state.totals.blocked}</strong></div>
      <div className="obs-chip">Agent <strong>{state.totals.agents}</strong></div>
      <div className="obs-chip">Token <strong>{formatTokens(tokens)}</strong></div>
      <div className="obs-chip">成本 <strong>{formatCost(state.totals.cost)}</strong></div>
      {syncError && <div className="obs-chip is-error">{syncError}</div>}
      <span className="obs-summary-spacer" />
      <span className="obs-session-meta">
        {lastSynced ? `同步于 ${formatClock(lastSynced)}` : '未同步'}
      </span>
      <Button
        variant="ghost"
        size="icon-sm"
        className="obs-refresh"
        title="重新同步快照"
        aria-label="重新同步快照"
        onClick={sync}
      >
        <RefreshCw />
      </Button>
    </div>
  )
}

function ObsTopology({ state }: { state: ObsState }) {
  const sessions = useMemo(() => activeSessions(state), [state])
  return (
    <section className="obs-topology" aria-label="Agent 协作拓扑">
      {sessions.length === 0 ? (
        <div className="obs-placeholder">暂无会话活动。在主窗口发起一轮对话后，这里会显示 Agent 协作状态。</div>
      ) : (
        sessions.map(s => <SessionGroup key={s.id} session={s} state={state} />)
      )}
    </section>
  )
}

function SessionGroup({ session, state }: { session: SessionNode; state: ObsState }) {
  const { main, children } = sessionAgents(state, session.id)
  return (
    <div className="obs-session">
      <div className="obs-session-head">
        <span className={`obs-status-dot is-${main?.status ?? (session.running ? 'running' : 'idle')}`} />
        <span className="obs-session-title">{session.title || '新线程'}</span>
        <span className="obs-session-meta">
          {session.running ? '运行中' : '空闲'} · {children.length} 个子 Agent
        </span>
      </div>
      <div className="obs-tree">
        {main && <AgentCard agent={main} />}
        {children.length > 0 && (
          <div className="obs-children">
            {children.map(c => <AgentCard key={c.key} agent={c} />)}
          </div>
        )}
        {!main && children.length === 0 && <div className="obs-placeholder">暂无 Agent 活动</div>}
      </div>
    </div>
  )
}

function AgentCard({ agent }: { agent: AgentNode }) {
  const status = agent.status
  const detail = status === 'blocked'
    ? '等待审批…'
    : agent.currentTool
      ? `当前工具：${agent.currentTool}`
      : status === 'running'
        ? '思考中…'
        : status === 'failed' && agent.error
          ? `错误：${agent.error}`
          : status === 'done' && agent.elapsedMs !== undefined
            ? `最近耗时 ${formatDuration(agent.elapsedMs)}`
            : '空闲'

  return (
    <div className={`obs-agent${agent.isMain ? ' main' : ''} is-${status}`}>
      <div className="obs-agent-head">
        <span className={`obs-status-dot is-${status}`} />
        <span className="obs-agent-name">{agent.isMain ? '主 Agent（Orchestrator）' : agent.name}</span>
        <span className="obs-agent-model">{agent.model || (agent.isMain ? '' : '跟随主模型')}</span>
      </div>
      <div className="obs-agent-tool">{detail}</div>
      <div className="obs-agent-stats">
        <span>{STATUS_LABEL[status]}</span>
        {status !== 'running' && agent.elapsedMs !== undefined && <span>{formatDuration(agent.elapsedMs)}</span>}
        <span>{formatTokens(agent.tokensIn)} / {formatTokens(agent.tokensOut)} tokens</span>
        <span>{formatCost(agent.cost)}</span>
      </div>
    </div>
  )
}

const FILTERS: Array<{ id: 'all' | 'other' | EventKind; label: string }> = [
  { id: 'all', label: '全部' },
  { id: 'tool', label: '工具' },
  { id: 'message', label: '私聊' },
  { id: 'approval', label: '审批' },
  { id: 'task', label: '任务' },
  { id: 'other', label: '其他' },
]

const OTHER_KINDS: EventKind[] = ['model', 'compress', 'session', 'smith', 'error']

const KIND_LABEL: Record<EventKind, string> = {
  tool: '工具',
  message: '私聊',
  approval: '审批',
  task: '任务',
  model: '模型',
  compress: '压缩',
  session: '会话',
  smith: '优化',
  error: '错误',
}

function ObsStream({ events }: { events: StreamEvent[] }) {
  const [filter, setFilter] = useState<'all' | 'other' | EventKind>('all')
  const [autoScroll, setAutoScroll] = useState(true)
  const bodyRef = useRef<HTMLDivElement | null>(null)

  const shown = useMemo(() => {
    if (filter === 'all') return events
    if (filter === 'other') return events.filter(e => OTHER_KINDS.includes(e.kind))
    return events.filter(e => e.kind === filter)
  }, [events, filter])

  useEffect(() => {
    if (!autoScroll) return
    const el = bodyRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [shown.length, autoScroll, filter])

  return (
    <section className="obs-stream" aria-label="实时事件流">
      <div className="obs-stream-head">
        <span className="obs-stream-title">事件流</span>
        <div className="obs-filters">
          {FILTERS.map(f => (
            <button
              key={f.id}
              className={`obs-filter${filter === f.id ? ' is-active' : ''}`}
              onClick={() => setFilter(f.id)}
              aria-pressed={filter === f.id}
            >
              {f.label}
            </button>
          ))}
        </div>
        <span className="obs-stream-spacer" />
        <Button
          variant="ghost"
          size="icon-sm"
          title={autoScroll ? '暂停自动滚动' : '恢复自动滚动'}
          aria-label={autoScroll ? '暂停自动滚动' : '恢复自动滚动'}
          onClick={() => setAutoScroll(v => !v)}
        >
          {autoScroll ? <Pause /> : <Play />}
        </Button>
      </div>
      <div className="obs-stream-body" ref={bodyRef}>
        {shown.length === 0 ? (
          <div className="obs-placeholder">暂无事件</div>
        ) : (
          shown.map(ev => (
            <div key={ev.id} className={`obs-event is-${ev.kind}`}>
              <span className="obs-event-time">{formatClock(ev.at)}</span>
              <span className="obs-event-type">{KIND_LABEL[ev.kind]}</span>
              <span className="obs-event-text">{ev.text}</span>
            </div>
          ))
        )}
      </div>
    </section>
  )
}

function ObsTasks({ tasks }: { tasks: Record<string, TaskNode> }) {
  const list = useMemo(() => {
    const active = Object.values(tasks).filter(t => t.status === 'running' || t.status === 'blocked')
    if (active.length > 0) return active.slice(0, 8)
    return recentTasks(tasks, 4)
  }, [tasks])

  return (
    <div className="obs-tasks" aria-label="任务状态">
      {list.length === 0 ? (
        <span className="obs-placeholder">暂无任务</span>
      ) : (
        list.map(t => (
          <span key={t.id} className={`obs-task ${taskClass(t.status)}`} title={t.error || t.title}>
            {TASK_STATUS_LABEL[t.status] ?? t.status} · {t.title || t.id.slice(0, 8)}
          </span>
        ))
      )}
    </div>
  )
}

function taskClass(status: string): string {
  if (status === 'running' || status === 'review' || status === 'assigned' || status === 'ready' || status === 'pending') {
    return 'is-running'
  }
  if (status === 'blocked') return 'is-blocked'
  if (status === 'failed' || status === 'rejected') return 'is-failed'
  return ''
}

// ObsTitleBar 无边框窗口标题栏：整条为拖拽区，按钮区 no-drag。
// 窗口操作走 @wailsio/runtime Window API（作用于当前窗口）。
function ObsTitleBar() {
  const [maxed, setMaxed] = useState(false)

  useEffect(() => {
    Window.IsMaximised().then(setMaxed).catch(() => { /* 忽略 */ })
  }, [])

  const toggleMaximise = async () => {
    try {
      if (await Window.IsMaximised()) await Window.Restore()
      else await Window.Maximise()
      setMaxed(await Window.IsMaximised())
    } catch { /* 忽略 */ }
  }

  return (
    <header
      className="obs-titlebar"
      onDoubleClick={e => {
        if ((e.target as HTMLElement).closest('button')) return
        toggleMaximise()
      }}
    >
      <div className="obs-titlebar-left">
        <Activity className="size-3.5" />
        <span className="titlebar-title">Agent 协作观测台</span>
      </div>
      <div className="titlebar-controls">
        <button className="win-btn" title="最小化" aria-label="最小化" onClick={() => Window.Minimise().catch(() => { /* 忽略 */ })}>
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 5 H10" stroke="currentColor" strokeWidth="1" /></svg>
        </button>
        <button className="win-btn" title={maxed ? '向下还原' : '最大化'} aria-label={maxed ? '向下还原' : '最大化'} onClick={toggleMaximise}>
          {maxed
            ? <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor">
                <rect x="0.5" y="2.5" width="7" height="7" />
                <path d="M2.5 2.5 V0.5 H9.5 V7.5 H7.5" />
              </svg>
            : <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor">
                <rect x="0.5" y="0.5" width="9" height="9" />
              </svg>}
        </button>
        <button className="win-btn close" title="关闭" aria-label="关闭" onClick={() => Window.Close().catch(() => { /* 忽略 */ })}>
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 0 L10 10 M10 0 L0 10" stroke="currentColor" strokeWidth="1" /></svg>
        </button>
      </div>
    </header>
  )
}
