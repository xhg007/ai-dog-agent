import { FolderOpen, MessageSquare, Plus, Settings, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ThreadInfo } from '../App'
import TaskPanel from './TaskPanel'

export type SideView = 'threads' | 'tasks'

interface Props {
  threads: ThreadInfo[]
  activeId: string
  wsPath: string
  sideView: SideView
  onSideView: (v: SideView) => void
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (id: string) => void
  onBrowseWorkspace: () => void
  onOpenSettings: () => void
}

function shortPath(p: string): string {
  if (!p) return ''
  const parts = p.split(/[\\/]/).filter(Boolean)
  return parts.length > 1 ? parts[parts.length - 1] : p
}

function timeLabel(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  const today = new Date()
  const sameDay = d.toDateString() === today.toDateString()
  if (sameDay) return d.toTimeString().slice(0, 5)
  return `${d.getMonth() + 1}/${d.getDate()}`
}

export default function Sidebar({ threads, activeId, wsPath, sideView, onSideView, onSelect, onNew, onDelete, onBrowseWorkspace, onOpenSettings }: Props) {
  return (
    <aside className="sidebar">
      <div className="brand">
        <div className="brand-logo">AI</div>
        <div className="brand-name">
          ai-dog-agent
          <small>多 Agent 编码助手</small>
        </div>
      </div>

      <div className="side-section">
        <div className="side-label">项目</div>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="outline" size="sm" className="w-full justify-start gap-2 px-2" onClick={onBrowseWorkspace}>
              <FolderOpen className="size-3.5 text-primary" />
              <span className="truncate font-normal">{wsPath ? shortPath(wsPath) : '选择项目目录'}</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent side="right">{wsPath || '选择项目目录'}</TooltipContent>
        </Tooltip>
      </div>

      <div className="side-section threads-section">
        <div className="flex items-center gap-1">
          <Tabs value={sideView} onValueChange={v => onSideView(v as SideView)} className="flex-1">
            <TabsList className="w-full">
              <TabsTrigger value="threads" className="gap-1 px-2 text-xs">
                <MessageSquare className="size-3" /> 线程
              </TabsTrigger>
              <TabsTrigger value="tasks" className="gap-1 px-2 text-xs">任务</TabsTrigger>
            </TabsList>
          </Tabs>
          {sideView === 'threads' && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon-sm" onClick={onNew} title="新建线程">
                  <Plus />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="right">新建线程</TooltipContent>
            </Tooltip>
          )}
        </div>

        {sideView === 'threads' ? (
          <>
            <div className="thread-list">
              {threads.map(t => (
                <div
                  key={t.id}
                  className={`thread-item${t.id === activeId ? ' active' : ''}`}
                  onClick={() => onSelect(t.id)}
                >
                  <div className="thread-title">
                    {t.running && <span className="running-dot" />}
                    {t.title || '新线程'}
                  </div>
                  <div className="thread-meta">
                    <span>{timeLabel(t.lastActive)}</span>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      className="text-muted-foreground hover:text-destructive"
                      title="删除线程"
                      onClick={e => { e.stopPropagation(); onDelete(t.id) }}
                    ><X /></Button>
                  </div>
                </div>
              ))}
            </div>
          </>
        ) : (
          <TaskPanel onOpenThread={onSelect} />
        )}
      </div>

      <div className="side-footer">
        <Button variant="ghost" size="sm" className="flex-1 justify-start gap-2" onClick={onOpenSettings}>
          <Settings className="size-3.5" /> 设置
        </Button>
        <span className="side-version">v0.2</span>
      </div>
    </aside>
  )
}
