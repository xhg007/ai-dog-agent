import { describe, it, expect } from 'vitest'
import {
  applyEvent,
  applySnapshot,
  emptyState,
  activeSessions,
  formatTokens,
  formatDuration,
  MAX_EVENTS,
  sessionAgents,
  recentTasks,
  type AgentEvent,
  type SnapshotLike,
} from './observability'

const snap: SnapshotLike = {
  sessions: [
    { id: 's1', title: '线程一', agentId: 'main1', model: 'DeepSeek', running: false, lastActive: 1000 },
  ],
  agents: [{ sessionId: 's1', agentId: 'coder1', name: 'Coder', model: 'Qwen', tool: 'dispatch_coder' }],
  tasks: [],
  runningTasks: {},
  serverTime: 1000,
}

const ev = (type: string, data: any, sessionId = 's1'): AgentEvent => ({ type, sessionId, data })

describe('observability reducer', () => {
  it('快照建立会话、主 Agent 与子 Agent 节点', () => {
    const s = applySnapshot(emptyState(), snap, 1000)
    const { main, children } = sessionAgents(s, 's1')
    expect(main?.agentId).toBe('main1')
    expect(main?.isMain).toBe(true)
    expect(children.map(c => c.name)).toEqual(['Coder'])
    expect(children[0].tool).toBe('dispatch_coder')
    expect(s.totals.agents).toBe(2)
    expect(s.sessions.s1.title).toBe('线程一')
  })

  it('chat_started → dispatch 调用 → dispatch 返回：子 Agent 运行与耗时正确', () => {
    let s = applySnapshot(emptyState(), snap, 1000)
    s = applyEvent(s, ev('chat_started', { agentId: 'main1', model: 'DeepSeek' }), 2000)
    expect(s.agents['s1:main1'].status).toBe('running')

    s = applyEvent(s, ev('tool_call', { agentId: 'main1', tool: 'dispatch_coder', args: '{}' }), 3000)
    expect(s.agents['s1:main1'].currentTool).toBe('dispatch_coder')
    expect(s.agents['s1:coder1'].status).toBe('running')

    s = applyEvent(s, ev('tool_result', { tool: 'dispatch_coder', content: 'ok' }), 5000)
    expect(s.agents['s1:coder1'].status).toBe('done')
    expect(s.agents['s1:coder1'].elapsedMs).toBe(2000)
  })

  it('子 Agent 内部工具调用按 agentId 归因并清除当前工具', () => {
    let s = applySnapshot(emptyState(), snap, 1000)
    s = applyEvent(s, ev('tool_call', { agentId: 'coder1', tool: 'read_file', args: '{"path":"a.go"}' }), 2000)
    expect(s.agents['s1:coder1'].currentTool).toBe('read_file')
    s = applyEvent(s, ev('tool_result', { tool: 'read_file', content: 'package a' }), 2500)
    expect(s.agents['s1:coder1'].currentTool).toBeUndefined()
    expect(s.agents['s1:coder1'].status).toBe('running')
  })

  it('未注册 Registry 的 dispatch（如 explorer）会动态建节点', () => {
    let s = applySnapshot(emptyState(), snap, 1000)
    s = applyEvent(s, ev('tool_call', { agentId: 'main1', tool: 'dispatch_explorer' }), 2000)
    const explorer = Object.values(s.agents).find(a => a.tool === 'dispatch_explorer')
    expect(explorer).toBeTruthy()
    expect(explorer?.name).toBe('explorer')
    expect(explorer?.isMain).toBe(false)
    expect(explorer?.status).toBe('running')
  })

  it('审批请求标记阻塞，决策后恢复运行', () => {
    let s = applySnapshot(emptyState(), snap, 1000)
    s = applyEvent(s, ev('approval_request', { id: 'a1', agentId: 'coder1', tool: 'write_file', path: 'x.go' }), 3000)
    expect(s.agents['s1:coder1'].status).toBe('blocked')
    expect(s.totals.blocked).toBe(1)

    s = applyEvent(s, ev('approval_decided', { id: 'a1', approved: true }), 4000)
    expect(s.agents['s1:coder1'].status).toBe('running')
    expect(s.totals.blocked).toBe(0)
  })

  it('llm_usage 按 Agent 与全局累加 token 与成本', () => {
    let s = applySnapshot(emptyState(), snap, 1000)
    s = applyEvent(s, ev('llm_usage', {
      agentId: 'coder1', model: 'Qwen', inputTokens: 100, outputTokens: 20, costIn: 0.001, costOut: 0.002,
    }), 2000)
    s = applyEvent(s, ev('llm_usage', {
      agentId: 'coder1', model: 'Qwen', inputTokens: 50, outputTokens: 10, costIn: 0.001, costOut: 0.001,
    }), 3000)
    expect(s.agents['s1:coder1'].tokensIn).toBe(150)
    expect(s.agents['s1:coder1'].tokensOut).toBe(30)
    expect(s.totals.tokensIn).toBe(150)
    expect(s.totals.cost).toBeCloseTo(0.005)
  })

  it('Agent 私聊创建目标节点并写入事件流', () => {
    let s = applySnapshot(emptyState(), snap, 1000)
    s = applyEvent(s, ev('agent_message', {
      from: 'main1', fromName: '主 Agent', to: 'reviewer1', toName: 'Reviewer', content: '看一下', depth: 1,
    }), 4000)
    expect(s.agents['s1:reviewer1'].name).toBe('Reviewer')
    expect(s.agents['s1:reviewer1'].status).toBe('running')
    const last = s.events[s.events.length - 1]
    expect(last.kind).toBe('message')
    expect(last.text).toContain('主 Agent → Reviewer')
  })

  it('任务状态同步到任务表并驱动主 Agent 阻塞', () => {
    let s = applySnapshot(emptyState(), snap, 1000)
    s = applyEvent(s, ev('task_state', { taskId: 't1', status: 'running', title: '实现登录' }), 3000)
    expect(s.tasks.t1.status).toBe('running')
    expect(s.tasks.t1.title).toBe('实现登录')
    s = applyEvent(s, ev('task_state', { taskId: 't1', status: 'blocked', title: '实现登录' }), 4000)
    expect(s.agents['s1:main1'].status).toBe('blocked')
  })

  it('chat_delta 被忽略且不产生新状态对象', () => {
    const before = applySnapshot(emptyState(), snap, 1000)
    expect(applyEvent(before, ev('chat_delta', { delta: 'x' }), 2000)).toBe(before)
  })

  it('只展示有活动的会话，过滤历史空闲线程', () => {
    const withIdle: SnapshotLike = {
      ...snap,
      sessions: [
        ...(snap.sessions ?? []),
        { id: 's2', title: '历史线程', agentId: 'main2', model: 'DeepSeek', running: false, lastActive: 500 },
      ],
    }
    let s = applySnapshot(emptyState(), withIdle, 1000)
    expect(activeSessions(s)).toEqual([])

    s = applyEvent(s, ev('chat_started', { agentId: 'main2', model: 'DeepSeek' }, 's2'), 2000)
    expect(activeSessions(s).map(x => x.id)).toEqual(['s2'])
  })

  it('最近任务按创建时间降序，不依赖插入顺序', () => {
    const s = applySnapshot(emptyState(), {
      ...snap,
      tasks: [
        { id: 'old', sessionId: 's1', title: '旧', status: 'completed', createdAt: '2026-01-01T00:00:00Z' },
        { id: 'new', sessionId: 's1', title: '新', status: 'completed', createdAt: '2026-01-03T00:00:00Z' },
        { id: 'mid', sessionId: 's1', title: '中', status: 'failed', createdAt: '2026-01-02T00:00:00Z' },
      ],
    }, 1000)
    expect(recentTasks(s.tasks, 2).map(t => t.id)).toEqual(['new', 'mid'])
  })

  it('事件流最多保留 MAX_EVENTS 条并持续递增 id', () => {
    let s = applySnapshot(emptyState(), snap, 1000)
    for (let i = 0; i < MAX_EVENTS + 20; i++) {
      s = applyEvent(s, ev('tool_call', { agentId: 'main1', tool: 'read_file' }), 2000 + i)
    }
    expect(s.events.length).toBe(MAX_EVENTS)
    expect(s.events[s.events.length - 1].id).toBe(MAX_EVENTS + 20)
  })

  it('格式化工具输出稳定', () => {
    expect(formatDuration(1500)).toBe('1.5s')
    expect(formatDuration(65_000)).toBe('1m 5s')
    expect(formatTokens(1500)).toBe('1.5k')
    expect(formatTokens(2_500_000)).toBe('2.50M')
  })
})
