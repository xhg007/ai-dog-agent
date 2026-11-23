import { useCallback, useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { TaskService } from '../../bindings/ai-dog-agent/internal/service/index.js'
import { ChatService } from '../../bindings/ai-dog-agent/internal/agent/index.js'

export interface TaskInfo {
  id: string
  sessionId: string
  title: string
  status: string // running/completed/failed/interrupted
  error?: string
  createdAt: string
  finishedAt?: string
}

const statusMeta: Record<string, { label: string; cls: string }> = {
  running: { label: '运行', cls: 'running' },
  completed: { label: '完成', cls: 'done' },
  failed: { label: '失败', cls: 'fail' },
  interrupted: { label: '中断', cls: 'stop' },
}

function timeLabel(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const today = new Date()
  if (d.toDateString() === today.toDateString()) return d.toTimeString().slice(0, 5)
  return `${d.getMonth() + 1}/${d.getDate()}`
}

interface Props {
  onOpenThread: (sessionId: string) => void
}

export default function TaskPanel({ onOpenThread }: Props) {
  const [tasks, setTasks] = useState<TaskInfo[]>([])
  const [busy, setBusy] = useState('')
  const [msg, setMsg] = useState('')

  const refresh = useCallback(async () => {
    try {
      const list = await TaskService.ListTasks(100)
      setTasks((list as TaskInfo[]) ?? [])
    } catch { /* 忽略 */ }
  }, [])

  useEffect(() => { refresh() }, [refresh])

  // task_state 事件实时合并（避免全量拉取）
  useEffect(() => {
    const off = Events.On('agent:event', (ev: any) => {
      const e = ev?.data
      if (e?.type === 'task_state') {
        const d = e.data ?? {}
        setTasks(m => {
          const idx = m.findIndex(t => t.id === d.taskId)
          if (idx < 0) return m
          const next = [...m]
          next[idx] = { ...next[idx], status: d.status ?? next[idx].status, error: d.error ?? next[idx].error }
          return next
        })
      }
    })
    return () => { off() }
  }, [])

  const recover = async (t: TaskInfo) => {
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
              <div key={t.id} className="task-item" title={t.error || t.title} onClick={() => onOpenThread(t.sessionId)}>
                <span className={`task-badge b-${meta.cls}`}>
                  {meta.cls === 'running' && <span className="running-dot" />}
                  {meta.label}
                </span>
                <span className="task-title">{t.title || t.id.slice(0, 8)}</span>
                <span className="task-time">{timeLabel(t.createdAt)}</span>
                {(t.status === 'interrupted' || t.status === 'failed') && (
                  <button
                    className="task-recover"
                    disabled={busy === t.id}
                    onClick={e => { e.stopPropagation(); recover(t) }}
                    title="从最后检查点恢复续跑"
                  >恢复</button>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
