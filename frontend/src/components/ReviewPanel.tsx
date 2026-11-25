import { useCallback, useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { RefreshCw, Undo2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import ConfirmDialog from './ConfirmDialog'
import { ApprovalService, WorkspaceService } from '../../bindings/ai-dog-agent/internal/service/index.js'

type Tab = 'diff' | 'files'

interface FileChange {
  status: string
  path: string
}

function parseStatus(out: string): FileChange[] {
  return (out || '').split('\n').filter(l => l.trim()).map(l => {
    // porcelain 前两位是状态，随后一个空格 + 路径；首行可能带一个前导空格，
    // 用正则兼容「前导空格被 trim 掉」的情况，避免吃掉路径首字符。
    const m = l.match(/^\s*(\S{1,2})\s+(.*)$/)
    return m ? { status: m[1], path: m[2] } : { status: '?', path: l.trim() }
  })
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
  const [pendingRollback, setPendingRollback] = useState<string | null>(null)

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

  // 工作区切换、对话结束、Agent 写操作后刷新（只读工具调用不触发 git 子进程）
  useEffect(() => { refresh() }, [refresh, refreshTick])
  useEffect(() => {
    const writeTools = new Set(['write_file', 'edit_file', 'run_shell'])
    let timer: ReturnType<typeof setTimeout> | undefined
    const scheduleRefresh = () => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(refresh, 300)
    }
    const off = Events.On('agent:event', (ev: any) => {
      const e = ev?.data
      if (e?.type === 'chat_done') { scheduleRefresh(); return }
      if (e?.type === 'tool_result' && writeTools.has(e.data?.tool ?? '')) scheduleRefresh()
    })
    return () => {
      if (timer) clearTimeout(timer)
      off()
    }
  }, [refresh])

  // 失败回滚（FR-4.5）：从写前备份恢复单个文件
  const performRollback = async (path: string) => {
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
        <Button variant="ghost" size="icon-sm" onClick={refresh} title="刷新" aria-label="刷新审查面板"><RefreshCw /></Button>
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
                        variant="outline"
                        size="xs"
                        className="text-muted-foreground hover:text-destructive"
                        title="回滚到写前备份（仅对本会话中被修改过的文件有效）"
                        onClick={() => setPendingRollback(c.path)}
                      ><Undo2 /> 回滚</Button>
                    </li>
                  )
                })}
              </ul>
            )
        )}
      </div>
      <ConfirmDialog
        open={pendingRollback !== null}
        title="回滚文件？"
        description={pendingRollback
          ? `将 ${pendingRollback} 回滚到本次会话的写前备份。该文件必须在本会话中被 Agent 修改过，否则会失败。`
          : ''}
        confirmLabel="回滚"
        destructive
        onConfirm={() => {
          const p = pendingRollback
          setPendingRollback(null)
          if (p) performRollback(p)
        }}
        onCancel={() => setPendingRollback(null)}
      />
    </aside>
  )
}
