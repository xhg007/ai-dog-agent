// 观测台状态模型：把后端 agent:event 归并为 Agent 节点、事件流、任务与汇总。
// 全部为纯函数，React 侧只负责订阅事件与批量提交，便于 vitest 覆盖。

export type AgentStatus = 'idle' | 'running' | 'blocked' | 'done' | 'failed'

export type EventKind =
  | 'tool'
  | 'message'
  | 'approval'
  | 'task'
  | 'model'
  | 'compress'
  | 'session'
  | 'smith'
  | 'error'

export interface AgentNode {
  key: string
  sessionId: string
  agentId: string
  name: string
  model: string
  /** dispatch_<name>；仅子 Agent 有，用于关联 dispatch 调用与结束 */
  tool?: string
  isMain: boolean
  status: AgentStatus
  currentTool?: string
  /** 最近一次运行开始时间（毫秒） */
  startedAt?: number
  /** 最近一次运行耗时（结束时写入） */
  elapsedMs?: number
  lastActive: number
  tokensIn: number
  tokensOut: number
  cost: number
  error?: string
}

export interface StreamEvent {
  id: number
  at: number
  kind: EventKind
  type: string
  sessionId: string
  text: string
}

export interface SessionNode {
  id: string
  title: string
  agentId: string
  model: string
  running: boolean
  currentTaskId?: string
  lastActive: number
}

export interface TaskNode {
  id: string
  sessionId: string
  title: string
  status: string
  error?: string
  createdAt: string
}

export interface ObsTotals {
  tokensIn: number
  tokensOut: number
  cost: number
  running: number
  blocked: number
  agents: number
}

export interface ObsState {
  sessions: Record<string, SessionNode>
  agents: Record<string, AgentNode>
  events: StreamEvent[]
  tasks: Record<string, TaskNode>
  totals: ObsTotals
  seq: number
  lastEventAt: number
}

/** 后端 agent:event 的统一载荷。 */
export interface AgentEvent {
  type: string
  sessionId: string
  data?: any
}

/** 与 ObservabilityService.Snapshot 的返回结构兼容（避免 lib 依赖生成的 bindings）。 */
export interface SnapshotLike {
  sessions?: Array<{
    id: string
    title: string
    agentId: string
    model: string
    running: boolean
    currentTaskId?: string
    lastActive: number
  }> | null
  agents?: Array<{
    sessionId: string
    agentId: string
    name: string
    model: string
    tool: string
  }> | null
  tasks?: Array<{
    id: string
    sessionId: string
    title: string
    status: string
    error?: string
    createdAt: string
  }> | null
  runningTasks?: Record<string, string | undefined> | null
  serverTime: number
}

export const MAX_EVENTS = 500
export const DISPATCH_PREFIX = 'dispatch_'

export function emptyState(): ObsState {
  return {
    sessions: {},
    agents: {},
    events: [],
    tasks: {},
    totals: { tokensIn: 0, tokensOut: 0, cost: 0, running: 0, blocked: 0, agents: 0 },
    seq: 0,
    lastEventAt: 0,
  }
}

const keyOf = (sessionId: string, agentId: string) => `${sessionId}:${agentId}`

/** 主 Agent ID：优先取快照；快照缺失时退回 sessionId 占位。 */
function mainAgentIdOf(state: ObsState, sessionId: string): string {
  return state.sessions[sessionId]?.agentId || sessionId
}

function withSession(
  state: ObsState,
  sessionId: string,
  update: (s: SessionNode) => SessionNode,
): ObsState {
  const cur = state.sessions[sessionId]
  if (!cur) return state
  return { ...state, sessions: { ...state.sessions, [sessionId]: update({ ...cur }) } }
}

function withAgent(
  state: ObsState,
  sessionId: string,
  agentId: string,
  update: (a: AgentNode) => AgentNode,
  defaults?: Partial<Pick<AgentNode, 'name' | 'model' | 'tool' | 'isMain'>>,
): ObsState {
  if (!sessionId || !agentId) return state
  const key = keyOf(sessionId, agentId)
  const cur: AgentNode = state.agents[key] ?? {
    key,
    sessionId,
    agentId,
    name: defaults?.name ?? agentId.slice(0, 8),
    model: defaults?.model ?? '',
    tool: defaults?.tool,
    isMain: defaults?.isMain ?? false,
    status: 'idle',
    lastActive: state.lastEventAt,
    tokensIn: 0,
    tokensOut: 0,
    cost: 0,
  }
  return { ...state, agents: { ...state.agents, [key]: update({ ...cur }) } }
}

function findChildByTool(state: ObsState, sessionId: string, tool: string): AgentNode | undefined {
  return Object.values(state.agents).find(
    a => a.sessionId === sessionId && !a.isMain && a.tool === tool,
  )
}

function findAgentByCurrentTool(state: ObsState, sessionId: string, tool: string): AgentNode | undefined {
  const candidates = Object.values(state.agents).filter(
    a => a.sessionId === sessionId && a.currentTool === tool,
  )
  candidates.sort((a, b) => b.lastActive - a.lastActive)
  return candidates[0]
}

function pushEvent(
  state: ObsState,
  ev: { at: number; kind: EventKind; type: string; sessionId: string; text: string },
): ObsState {
  const id = state.seq + 1
  const events =
    state.events.length >= MAX_EVENTS
      ? [...state.events.slice(state.events.length - MAX_EVENTS + 1), { ...ev, id }]
      : [...state.events, { ...ev, id }]
  return { ...state, events, seq: id }
}

export function computeTotals(s: ObsState): ObsTotals {
  let tokensIn = 0
  let tokensOut = 0
  let cost = 0
  let running = 0
  let blocked = 0
  let agents = 0
  for (const a of Object.values(s.agents)) {
    tokensIn += a.tokensIn
    tokensOut += a.tokensOut
    cost += a.cost
    agents++
    if (a.status === 'running') running++
    if (a.status === 'blocked') blocked++
  }
  return { tokensIn, tokensOut, cost, running, blocked, agents }
}

/** 用打开窗口时的快照建立起点；保留同一会话中由事件动态创建的子 Agent 节点。 */
export function applySnapshot(state: ObsState, snap: SnapshotLike, now = Date.now()): ObsState {
  const sessions: Record<string, SessionNode> = {}
  const agents: Record<string, AgentNode> = {}
  const tasks: Record<string, TaskNode> = {}

  const liveSessions = new Set<string>()
  for (const s of snap.sessions ?? []) {
    if (!s?.id) continue
    liveSessions.add(s.id)
    sessions[s.id] = {
      id: s.id,
      title: s.title,
      agentId: s.agentId,
      model: s.model,
      running: !!s.running,
      currentTaskId: snap.runningTasks?.[s.id] || s.currentTaskId || undefined,
      lastActive: s.lastActive,
    }
  }

  // 保留动态节点（如未注册 Registry 的内置 explorer），但仍属于快照中的活跃会话
  for (const [k, a] of Object.entries(state.agents)) {
    if (liveSessions.has(a.sessionId)) agents[k] = a
  }

  for (const s of snap.sessions ?? []) {
    if (!s?.id || !s.agentId) continue
    const k = keyOf(s.id, s.agentId)
    const prev = agents[k]
    agents[k] = {
      key: k,
      sessionId: s.id,
      agentId: s.agentId,
      name: '主 Agent',
      model: s.model || prev?.model || '',
      isMain: true,
      status: s.running ? (prev?.status === 'blocked' ? 'blocked' : 'running') : (prev?.status ?? 'idle'),
      currentTool: prev?.currentTool,
      startedAt: prev?.startedAt,
      elapsedMs: prev?.elapsedMs,
      lastActive: prev?.lastActive ?? s.lastActive,
      tokensIn: prev?.tokensIn ?? 0,
      tokensOut: prev?.tokensOut ?? 0,
      cost: prev?.cost ?? 0,
      error: prev?.error,
    }
  }

  for (const a of snap.agents ?? []) {
    if (!a?.sessionId || !a.agentId) continue
    const k = keyOf(a.sessionId, a.agentId)
    const prev = agents[k]
    agents[k] = {
      key: k,
      sessionId: a.sessionId,
      agentId: a.agentId,
      name: a.name || a.agentId.slice(0, 8),
      model: a.model || '',
      tool: a.tool,
      isMain: false,
      status: prev?.status ?? 'idle',
      currentTool: prev?.currentTool,
      startedAt: prev?.startedAt,
      elapsedMs: prev?.elapsedMs,
      lastActive: prev?.lastActive ?? now,
      tokensIn: prev?.tokensIn ?? 0,
      tokensOut: prev?.tokensOut ?? 0,
      cost: prev?.cost ?? 0,
      error: prev?.error,
    }
  }

  for (const t of snap.tasks ?? []) {
    if (!t?.id) continue
    tasks[t.id] = {
      id: t.id,
      sessionId: t.sessionId,
      title: t.title,
      status: t.status,
      error: t.error,
      createdAt: t.createdAt,
    }
  }

  const next: ObsState = {
    ...state,
    sessions,
    agents,
    tasks,
    lastEventAt: state.lastEventAt || now,
  }
  return { ...next, totals: computeTotals(next) }
}

/** 应用一条实时事件；chat_delta 直接忽略（观测台不需要逐 token）。 */
export function applyEvent(state: ObsState, ev: AgentEvent, now = Date.now()): ObsState {
  const type = ev?.type ?? ''
  if (!type || type === 'chat_delta') return state
  const sessionId = ev.sessionId ?? ''
  const data = ev.data ?? {}

  let next: ObsState = { ...state, lastEventAt: now }

  const kind = eventKind(type)
  if (kind) {
    next = pushEvent(next, {
      at: now,
      kind,
      type,
      sessionId,
      text: describeEvent(type, data),
    })
  }

  switch (type) {
    case 'chat_started': {
      const agentId = data.agentId || mainAgentIdOf(next, sessionId)
      next = withAgent(
        next,
        sessionId,
        agentId,
        a => ({
          ...a,
          isMain: true,
          status: 'running',
          startedAt: now,
          elapsedMs: undefined,
          currentTool: undefined,
          error: undefined,
          lastActive: now,
          model: data.model || a.model,
        }),
        { isMain: true, name: '主 Agent' },
      )
      next = withSession(next, sessionId, s => ({ ...s, running: true }))
      break
    }

    case 'chat_done': {
      const agentId = mainAgentIdOf(next, sessionId)
      next = withAgent(
        next,
        sessionId,
        agentId,
        a => ({
          ...a,
          status: 'done',
          currentTool: undefined,
          elapsedMs: a.startedAt ? now - a.startedAt : a.elapsedMs,
          lastActive: now,
        }),
        { isMain: true, name: '主 Agent' },
      )
      next = withSession(next, sessionId, s => ({ ...s, running: false }))
      break
    }

    case 'chat_error': {
      const agentId = mainAgentIdOf(next, sessionId)
      next = withAgent(
        next,
        sessionId,
        agentId,
        a => ({
          ...a,
          status: 'failed',
          error: String(data.message ?? '未知错误'),
          currentTool: undefined,
          lastActive: now,
        }),
        { isMain: true, name: '主 Agent' },
      )
      next = withSession(next, sessionId, s => ({ ...s, running: false }))
      break
    }

    case 'tool_call': {
      const agentId = data.agentId || mainAgentIdOf(next, sessionId)
      const tool = String(data.tool ?? '')
      next = withAgent(
        next,
        sessionId,
        agentId,
        a => ({
          ...a,
          status: a.status === 'blocked' ? 'blocked' : 'running',
          currentTool: tool,
          startedAt: a.startedAt ?? now,
          lastActive: now,
        }),
        { isMain: agentId === mainAgentIdOf(next, sessionId) },
      )
      if (tool.startsWith(DISPATCH_PREFIX)) {
        // 子 Agent 启动：优先按 dispatch 工具名匹配已注册节点；
        // 内置 explorer 未注册 Registry，此时动态建节点（name 取工具名后缀）。
        const child = findChildByTool(next, sessionId, tool)
        const fallbackName = tool.slice(DISPATCH_PREFIX.length).replace(/_/g, ' ') || tool
        next = withAgent(
          next,
          sessionId,
          child?.agentId ?? tool,
          a => ({
            ...a,
            name: child?.name || a.name || fallbackName,
            tool,
            status: 'running',
            startedAt: now,
            elapsedMs: undefined,
            currentTool: undefined,
            lastActive: now,
          }),
          { isMain: false, name: fallbackName, tool },
        )
      }
      break
    }

    case 'tool_result': {
      const tool = String(data.tool ?? '')
      if (tool.startsWith(DISPATCH_PREFIX)) {
        const child = findChildByTool(next, sessionId, tool)
        if (child) {
          next = withAgent(next, sessionId, child.agentId, a => ({
            ...a,
            status: a.status === 'failed' ? 'failed' : 'done',
            currentTool: undefined,
            elapsedMs: a.startedAt ? now - a.startedAt : a.elapsedMs,
            lastActive: now,
          }))
        }
      } else {
        // tool_result 不含 agentId：关闭最近一个正在执行同名工具的节点
        const owner = findAgentByCurrentTool(next, sessionId, tool)
        if (owner) {
          next = withAgent(next, sessionId, owner.agentId, a => ({
            ...a,
            currentTool: undefined,
            lastActive: now,
          }))
        }
      }
      break
    }

    case 'llm_usage': {
      const agentId = data.agentId || mainAgentIdOf(next, sessionId)
      const tin = Number(data.inputTokens) || 0
      const tout = Number(data.outputTokens) || 0
      const cost = (Number(data.costIn) || 0) + (Number(data.costOut) || 0)
      next = withAgent(
        next,
        sessionId,
        agentId,
        a => ({
          ...a,
          tokensIn: a.tokensIn + tin,
          tokensOut: a.tokensOut + tout,
          cost: a.cost + cost,
          model: a.model || String(data.model ?? ''),
          lastActive: now,
        }),
        { isMain: agentId === mainAgentIdOf(next, sessionId) },
      )
      break
    }

    case 'task_state': {
      const taskId = String(data.taskId ?? '')
      if (taskId) {
        const prev = next.tasks[taskId]
        next = {
          ...next,
          tasks: {
            ...next.tasks,
            [taskId]: {
              id: taskId,
              sessionId,
              title: String(data.title || prev?.title || taskId.slice(0, 8)),
              status: String(data.status || prev?.status || ''),
              error: data.error ? String(data.error) : prev?.error,
              createdAt: prev?.createdAt ?? new Date(now).toISOString(),
            },
          },
        }
      }
      const status = String(data.status ?? '')
      // 审批阻塞通常已由 approval_request 精确标到发起 Agent；只有无人被标记时才回落到主 Agent
      const anyBlocked = Object.values(next.agents).some(
        a => a.sessionId === sessionId && a.status === 'blocked',
      )
      if ((status === 'blocked' && !anyBlocked) || status === 'running') {
        const mainId = mainAgentIdOf(next, sessionId)
        next = withAgent(
          next,
          sessionId,
          mainId,
          a => {
            if (status === 'blocked') return { ...a, status: 'blocked', lastActive: now }
            return a.status === 'blocked' ? { ...a, status: 'running', lastActive: now } : a
          },
          { isMain: true, name: '主 Agent' },
        )
      }
      break
    }

    case 'approval_request': {
      const agentId = data.agentId || mainAgentIdOf(next, sessionId)
      next = withAgent(
        next,
        sessionId,
        agentId,
        a => ({ ...a, status: 'blocked', lastActive: now }),
        { isMain: agentId === mainAgentIdOf(next, sessionId) },
      )
      break
    }

    case 'approval_decided': {
      // 事件不含 agentId：恢复该会话所有处于阻塞态的 Agent
      const agents: Record<string, AgentNode> = { ...next.agents }
      for (const [k, a] of Object.entries(agents)) {
        if (a.sessionId === sessionId && a.status === 'blocked') {
          agents[k] = { ...a, status: 'running', lastActive: now }
        }
      }
      next = { ...next, agents }
      break
    }

    case 'agent_message': {
      const from = String(data.from ?? '')
      const to = String(data.to ?? '')
      const toName = String(data.toName || '')
      if (from) {
        const key = keyOf(sessionId, from)
        if (next.agents[key]) {
          next = withAgent(next, sessionId, from, a => ({ ...a, lastActive: now }))
        }
      }
      if (to) {
        next = withAgent(
          next,
          sessionId,
          to,
          a => ({
            ...a,
            name: a.name || toName || to.slice(0, 8),
            status: a.status === 'idle' || a.status === 'done' ? 'running' : a.status,
            lastActive: now,
          }),
          { isMain: false, name: toName || to.slice(0, 8) },
        )
      }
      break
    }

    case 'model_fallback': {
      const agentId = data.agentId
      if (agentId) {
        next = withAgent(next, sessionId, agentId, a => ({
          ...a,
          model: String(data.to || a.model),
          lastActive: now,
        }))
      }
      break
    }

    case 'context_compressed': {
      const agentId = data.agentId
      if (agentId) {
        next = withAgent(next, sessionId, agentId, a => ({ ...a, lastActive: now }))
      }
      break
    }

    case 'agent_smith': {
      if (data.error) {
        next = pushEvent(next, {
          at: now,
          kind: 'error',
          type,
          sessionId,
          text: `agent-smith 失败：${data.error}`,
        })
      }
      break
    }

    default:
      break
  }

  return { ...next, totals: computeTotals(next) }
}

export function eventKind(type: string): EventKind | null {
  switch (type) {
    case 'tool_call':
    case 'tool_result':
      return 'tool'
    case 'agent_message':
      return 'message'
    case 'approval_request':
    case 'approval_decided':
      return 'approval'
    case 'task_state':
      return 'task'
    case 'model_fallback':
      return 'model'
    case 'context_compressed':
      return 'compress'
    case 'chat_started':
    case 'chat_done':
      return 'session'
    case 'chat_error':
      return 'error'
    case 'agent_smith':
      return 'smith'
    default:
      return null
  }
}

export function describeEvent(type: string, data: any): string {
  switch (type) {
    case 'chat_started':
      return `主 Agent 开始运行${data.model ? ' · ' + data.model : ''}`
    case 'chat_done':
      return `主 Agent 完成${data.totalTokens ? ` · ${data.totalTokens} tokens` : ''}`
    case 'chat_error':
      return `错误：${data.message ?? ''}`
    case 'tool_call':
      return `${data.tool ?? '工具'}${data.args ? ' ' + truncate(data.args, 120) : ''}`
    case 'tool_result':
      return `${data.tool ?? '工具'} 返回${data.content ? '：' + truncate(data.content, 120) : ''}`
    case 'llm_usage':
      return `${data.model || '模型'} +${data.inputTokens ?? 0}/+${data.outputTokens ?? 0} tokens`
    case 'task_state':
      return `任务 ${data.title || data.taskId} → ${data.status}${data.error ? '（' + data.error + '）' : ''}`
    case 'approval_request':
      return `${data.tool ?? '工具'} 等待审批${data.path ? '：' + data.path : ''}`
    case 'approval_decided':
      return `审批${data.approved ? '通过' : '拒绝'}`
    case 'agent_message':
      return `${data.fromName || data.from} → ${data.toName || data.to}${data.reply ? '：' + truncate(data.reply, 100) : ''}`
    case 'model_fallback':
      return `${data.from} → ${data.to}${data.reason ? '（' + data.reason + '）' : ''}`
    case 'context_compressed':
      return `上下文压缩 ${data.beforeTokens ?? 0} → ${data.afterTokens ?? 0} tokens`
    case 'agent_smith':
      return `agent-smith ${data.phase}${data.name ? ' · ' + data.name : ''}${data.reason ? '：' + data.reason : ''}`
    default:
      return type
  }
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n) + '…' : s
}

/** 会话视图：主 Agent + 子 Agent（按名称排序）。 */
export function sessionAgents(state: ObsState, sessionId: string): { main?: AgentNode; children: AgentNode[] } {
  let main: AgentNode | undefined
  const children: AgentNode[] = []
  for (const a of Object.values(state.agents)) {
    if (a.sessionId !== sessionId) continue
    if (a.isMain) main = a
    else children.push(a)
  }
  children.sort((a, b) => a.name.localeCompare(b.name))
  return { main, children }
}

/** 有活动的会话：快照中的会话 + 事件先到、快照尚未覆盖的会话。 */
export function visibleSessions(state: ObsState): SessionNode[] {
  const list = Object.values(state.sessions)
  const known = new Set(list.map(s => s.id))
  for (const a of Object.values(state.agents)) {
    if (known.has(a.sessionId)) continue
    known.add(a.sessionId)
    list.push({
      id: a.sessionId,
      title: '',
      agentId: a.isMain ? a.agentId : '',
      model: a.model,
      running: a.status === 'running' || a.status === 'blocked',
      lastActive: a.lastActive,
    })
  }
  return list.sort((a, b) => b.lastActive - a.lastActive)
}

/** 观测台展示的会话：运行中，或本轮已有 Agent 活动（避免列出全部历史空闲线程）。 */
export function activeSessions(state: ObsState): SessionNode[] {
  return visibleSessions(state).filter(s => {
    if (s.running) return true
    const { main, children } = sessionAgents(state, s.id)
    if (main && main.status !== 'idle') return true
    return children.some(c => c.status !== 'idle')
  })
}

/** 最近任务：按创建时间降序。快照与实时事件混合插入，不能依赖对象插入顺序。 */
export function recentTasks(tasks: Record<string, TaskNode>, n: number): TaskNode[] {
  return Object.values(tasks)
    .sort((a, b) => (b.createdAt || '').localeCompare(a.createdAt || ''))
    .slice(0, n)
}

export function formatClock(ms: number): string {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

export function formatDuration(ms?: number): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return ''
  if (ms < 1000) return `${Math.round(ms)}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  const m = Math.floor(ms / 60_000)
  const s = Math.round((ms % 60_000) / 1000)
  return `${m}m ${s}s`
}

export function formatTokens(n: number): string {
  if (!n) return '0'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`
  return `${(n / 1_000_000).toFixed(2)}M`
}

export function formatCost(n: number): string {
  if (!n) return '¥0'
  if (n < 0.01) return `¥${n.toFixed(4)}`
  return `¥${n.toFixed(2)}`
}

export const STATUS_LABEL: Record<AgentStatus, string> = {
  idle: '空闲',
  running: '运行中',
  blocked: '等待审批',
  done: '完成',
  failed: '失败',
}

export const TASK_STATUS_LABEL: Record<string, string> = {
  pending: '待调度',
  ready: '就绪',
  assigned: '已分配',
  running: '运行',
  blocked: '等待审批',
  review: '校验中',
  rejected: '已拒绝',
  completed: '完成',
  cancelled: '已取消',
  failed: '失败',
}
