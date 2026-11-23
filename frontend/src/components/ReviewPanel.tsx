import { useCallback, useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { ApprovalService, WorkspaceService } from '../../bindings/ai-dog-agent/internal/service/index.js'

type Tab = 'diff' | 'files'

interface FileChange {
  status: string
  path: string
}

function parseStatus(out: string): FileChange[] {
  return (out || '').split('\n').filter(l => l.trim()).map(l => ({
    status: l.slice(0, 2).trim() || '?',
    path: l.slice(3).trim(),
  }))
}

export default function ReviewPanel({ wsPath, refreshTick }: { wsPath: string; refreshTick: number }) {
  const [tab, setTab] = useState<Tab>('diff')
  const [diff, setDiff] = useState('')
  const [status, setStatus] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    if (!wsPath) { setDiff(''); setStatus(''); setError('未设置工作区'); return }
    setLoading(true)
    setError('')
    try {
      const [d, s] = await Promise.all([
        WorkspaceService.GitDiff(),
        WorkspaceService.GitStatus(),
      ])
      setDiff(d ?? '')
      setStatus(s ?? '')
    } catch (err: any) {
      setError(typeof err === 'string' ? err : err?.message ?? String(err))
    } finally {
      setLoading(false)
    }
  }, [wsPath])

  // 工作区切换、对话结束、Agent 写操作后刷新
  useEffect(() => { refresh() }, [refresh, refreshTick])
  useEffect(() => {
    const off = Events.On('agent:event', (ev: any) => {
      const e = ev.data
      if (e?.type === 'chat_done' || e?.type === 'tool_result') refresh()
    })
    return () => { off() }
  }, [refresh])

  // 失败回滚（FR-4.5）：从写前备份恢复单个文件
  const rollback = async (path: string) => {
    if (!confirm(`将 ${path} 回滚到写前备份？\n（该文件必须在本会话中被 Agent 修改过，否则无备份可回）`)) return
    setError('')
    try {
      await ApprovalService.RollbackFile(path)
      refresh()
    } catch (err: any) {
      setError(typeof err === 'string' ? err : err?.message ?? String(err))
    }
  }

  const changes = parseStatus(status)

  return (
    <aside className="review">
      <div className="review-header">
        <span className="review-title">审查</span>
        <button className="side-add" onClick={refresh} title="刷新">⟳</button>
      </div>
      <div className="review-tabs">
        <button className={`review-tab${tab === 'diff' ? ' active' : ''}`} onClick={() => setTab('diff')}>Diff</button>
        <button className={`review-tab${tab === 'files' ? ' active' : ''}`} onClick={() => setTab('files')}>
          文件{changes.length > 0 ? ` (${changes.length})` : ''}
        </button>
      </div>
      <div className="review-body">
        {!wsPath && <div className="review-empty">未设置工作区</div>}
        {wsPath && loading && <div className="review-empty">加载中…</div>}
        {wsPath && !loading && error && <div className="review-empty">{error}</div>}
        {wsPath && !loading && !error && tab === 'diff' && (
          diff ? <pre className="diff-view">{diff}</pre> : <div className="review-empty">无未提交变更</div>
        )}
        {wsPath && !loading && !error && tab === 'files' && (
          changes.length === 0
            ? <div className="review-empty">工作区干净</div>
            : (
              <ul className="file-list">
                {changes.map((c, i) => (
                  <li key={i} className="file-item">
                    <span className={`status-badge s-${c.status}`}>{c.status}</span>
                    <span className="file-path" title={c.path}>{c.path}</span>
                    <button
                      className="rollback-btn"
                      title="回滚到写前备份（仅对本会话中被修改过的文件有效）"
                      onClick={() => rollback(c.path)}
                    >↩ 回滚</button>
                  </li>
                ))}
              </ul>
            )
        )}
      </div>
    </aside>
  )
}
