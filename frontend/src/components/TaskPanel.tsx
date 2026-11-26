import { Fragment, useCallback, useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { ListTree, RotateCcw, Wand2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { TaskService } from '../../bindings/ai-dog-agent/internal/service/index.js'
import { ChatService } from '../../bindings/ai-dog-agent/internal/agent/index.js'
import type { Task, TaskEvent } from '../../bindings/ai-dog-agent/internal/store/models.js'
import ConfirmDialog from './ConfirmDialog'

// 任务类型直接复用 Wails 生成的强类型（TaskService 已不再返回 any）。
export type TaskInfo = Task

const statusMeta: Record<string, { label: string; cls: string }> = {
  pending: { label: '待调度', cls: 'stop' },
  ready: { label: '就绪', cls: 'stop' },
  assigned: { label: '已分配', cls: 'stop' },
  running: { label: '运行', cls: 'running' },
  blocked: { label: '等待审批', cls: 'running' },
  review: { label: '校验中', cls: 'running' },
  rejected: { label: '已拒绝', cls: 'fail' },
  completed: { label: '完成', cls: 'done' },
  cancelled: { label: '已取消', cls: 'stop' },
  failed: { label: '失败', cls: 'fail' },
  interrupted: { label: '中断', cls: 'stop' }, // 旧数据兼容
}

function timeLabel(iso?: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const today = new Date()
  if (d.toDateString() === today.toDateString()) return d.toTimeString().slice(0, 5)
  return `${d.getMonth() + 1}/${d.getDate()}`
}

function formatEvent(ev: TaskEvent): string {
  const t = timeLabel(ev.createdAt)
  const payload = (ev.payload ?? '').length > 300 ? ev.payload.slice(0, 300) + '…' : ev.payload
  return `${t ? t + '  ' : ''}${ev.type}${payload ? '  ' + payload : ''}`
}

interface Props {
  onOpenThread: (sessionId: string) => void
}

export default function TaskPanel({ onOpenThread }: Props) {
  const [tasks, setTasks] = useState<Task[]>([])
  const [busy, setBusy] = useState('')
  const [smithBusy, setSmithBusy] = useState('')
  const [msg, setMsg] = useState('')
  // 恢复前确认：恢复会把会话历史回滚到断点之前，需要用户知情
  const [pendingRecover, setPendingRecover] = useState<Task | null>(null)
  // 事件流（按需拉取，只在展开的任务上保留）
  const [openEvents, setOpenEvents] = useState('')
  const [events, setEvents] = useState<Record<string, TaskEvent[]>>({})

  const refresh = useCallback(async () => {
    try {
      setTasks((await TaskService.ListTasks(100)) ?? [])
    } catch { /* 忽略 */ }
  }, [])

  useEffect(() => { refresh() }, [refresh])

  // task_state 事件实时合并；未知任务（刚创建）触发一次列表刷新
  useEffect(() => {
    const off = Events.On('agent:event', (ev: any) => {
      const e = ev?.data
      if (e?.type !== 'task_state') return
      const d = e.data ?? {}
      setTasks(m => {
        const idx = m.findIndex(t => t.id === d.taskId)
        if (idx < 0) {
          refresh()
          return m
        }
        const next = [...m]
        next[idx] = { ...next[idx], status: d.status ?? next[idx].status, error: d.error ?? next[idx].error }
        return next
      })
    })
    return () => { off() }
  }, [refresh])

  const toggleEvents = async (t: Task) => {
    if (openEvents === t.id) {
      setOpenEvents('')
      return
    }
    setMsg('')
    try {
      const evs = await TaskService.GetTaskEvents(t.id, 200)
      setEvents(m => ({ ...m, [t.id]: evs ?? [] }))
      setOpenEvents(t.id)
    } catch (err: any) {
      setMsg(typeof err === 'string' ? err : err?.message ?? String(err))
    }
  }

  const recover = async (t: Task) => {
    setMsg('')
    setBusy(t.id)
    try {
      await ChatService.RecoverTask(t.id)
      onOpenThread(t.sessionId)
      setMsg(`正在从断点恢复：${t.title || t.id}`)
    } catch (err: any) {
      setMsg(typeof err === 'string' ? err : err?.message ?? String(err))
    } finally {
      setBusy('')
    }
  }

  // 对失败/取消任务运行 agent-smith（异步，结果通过 agent_smith 事件展示）
  const optimize = async (t: Task) => {
    setMsg('')
    setSmithBusy(t.id)
    try {
      await ChatService.OptimizeAgents(t.sessionId)
      setMsg(`agent-smith 已启动：${t.title || t.id.slice(0, 8)}（结果见对话事件流）`)
    } catch (err: any) {
      setMsg(typeof err === 'string' ? err : err?.message ?? String(err))
    } finally {
      setSmithBusy('')
    }
  }

  return (
    <div className="task-panel">
      {msg && <p className="task-hint">{msg}</p>}
      {tasks.length === 0 ? (
        <div className="side-placeholder">暂无任务</div>
      ) : (
        <div className="task-list">
          {tasks.map(t => {
            const meta = statusMeta[t.status] ?? { label: t.status, cls: 'fail' }
            return (
              <Fragment key={t.id}>
                <div
                  className="task-item"
                  title={t.error || t.title}
                  role="button"
                  tabIndex={0}
                  onClick={() => onOpenThread(t.sessionId)}
                  onKeyDown={e => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      onOpenThread(t.sessionId)
                    }
                  }}
                >
                  <Badge variant="outline" className={`task-badge b-${meta.cls}`}>
                    {meta.cls === 'running' && <span className="running-dot" />}
                    {meta.label}
                  </Badge>
                  <span className="task-title">{t.title || t.id.slice(0, 8)}</span>
                  <span className="task-time">{timeLabel(t.createdAt)}</span>
                  <Button
                    variant="outline"
                    size="xs"
                    onClick={e => { e.stopPropagation(); toggleEvents(t) }}
                    title="查看任务事件流"
                    aria-label={`查看任务事件 ${t.title || t.id.slice(0, 8)}`}
                  ><ListTree className="size-3" /> 事件</Button>
                  {['interrupted', 'cancelled', 'failed', 'rejected'].includes(t.status) && (
                    <>
                      <Button
                        variant="outline"
                        size="xs"
                        disabled={busy === t.id}
                        onClick={e => { e.stopPropagation(); setPendingRecover(t) }}
                        title="从最后检查点恢复续跑"
                        aria-label={`恢复任务 ${t.title || t.id.slice(0, 8)}`}
                      ><RotateCcw className="size-3" /> 恢复</Button>
                      <Button
                        variant="outline"
                        size="xs"
                        disabled={smithBusy === t.id}
                        onClick={e => { e.stopPropagation(); optimize(t) }}
                        title="运行 agent-smith 优化项目 Agent"
                        aria-label={`优化 Agent ${t.title || t.id.slice(0, 8)}`}
                      ><Wand2 className="size-3" /> 优化</Button>
                    </>
                  )}
                </div>
                {openEvents === t.id && (
                  <pre className="task-events">
                    {(events[t.id] ?? []).map(formatEvent).join('\n') || '暂无事件记录'}
                  </pre>
                )}
              </Fragment>
            )
          })}
        </div>
      )}
      <ConfirmDialog
        open={pendingRecover !== null}
        title="从断点恢复任务？"
        description={pendingRecover
          ? `将从「${pendingRecover.title || pendingRecover.id.slice(0, 8)}」的最后一个工具检查点续跑。为保证上下文一致，该会话的消息历史会回滚到断点之前，断点之后的对话记录不会保留。`
          : ''}
        confirmLabel="恢复"
        onConfirm={() => {
          const t = pendingRecover
          setPendingRecover(null)
          if (t) recover(t)
        }}
        onCancel={() => setPendingRecover(null)}
      />
    </div>
  )
}
