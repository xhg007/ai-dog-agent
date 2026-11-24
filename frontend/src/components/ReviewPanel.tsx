import { useCallback, useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { RefreshCw, Undo2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
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

// git status 单字符分类 → Badge 配色
const statusVariant: Record<string, { label: string; cls: string }> = {
  M: { label: 'M', cls: 'mod' },      // 修改
  A: { label: 'A', cls: 'add' },      // 新增
  D: { label: 'D', cls: 'del' },      // 删除
  '?': { label: '?', cls: 'untracked' },
  U: { label: 'U', cls: 'untracked' },
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
        <Button variant="ghost" size="icon-sm" onClick={refresh} title="刷新"><RefreshCw /></Button>
      </div>
      <Tabs value={tab} onValueChange={v => setTab(v as Tab)}>
        <TabsList variant="line" className="w-full justify-start px-2">
          <TabsTrigger value="diff" className="text-xs">Diff</TabsTrigger>
          <TabsTrigger value="files" className="text-xs">
            文件{changes.length > 0 ? ` (${changes.length})` : ''}
          </TabsTrigger>
        </TabsList>
      </Tabs>
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
                {changes.map((c, i) => {
                  const meta = statusVariant[c.status[0]] ?? { label: c.status, cls: 'untracked' }
                  return (
                    <li key={i} className="file-item">
                      <Badge variant="outline" className={`status-${meta.cls} font-mono`}>{meta.label}</Badge>
                      <span className="file-path" title={c.path}>{c.path}</span>
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-muted-foreground hover:text-destructive"
                        title="回滚到写前备份（仅对本会话中被修改过的文件有效）"
                        onClick={() => rollback(c.path)}
                      ><Undo2 className="size-3" /> 回滚</Button>
                    </li>
                  )
                })}
              </ul>
            )
        )}
      </div>
    </aside>
  )
}
