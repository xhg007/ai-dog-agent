import { useEffect, useRef, useState } from 'react'
import type { ComponentType } from 'react'
import { Events } from '@wailsio/runtime'
import {
  ArrowDown, Bot, Compass, FileText, FlaskConical, Folder, GitBranch, Hammer,
  Microscope, Pencil, Search, SendHorizontal, Square, Terminal, Wrench,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ChatService } from '../../bindings/ai-dog-agent/internal/agent/index.js'
import { ConfigService } from '../../bindings/ai-dog-agent/internal/service/index.js'
import ApprovalCard, { PendingApproval } from './ApprovalCard'
import MarkdownBody from './MarkdownBody'

interface Msg {
  role: 'user' | 'assistant' | 'error' | 'tool' | 'system'
  content: string
  tool?: string
  running?: boolean // 工具调用已开始、结果未回（tool_call 事件已到）
}

interface AgentEvent {
  type: string
  sessionId: string
  data: any
}

interface Usage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
}

const toolIcons: Record<string, ComponentType<{ className?: string }>> = {
  read_file: FileText, list_files: Folder, search_code: Search,
  git_diff: GitBranch, git_status: GitBranch, project_summary: Compass,
  dispatch_explorer: Compass,
  write_file: Pencil, edit_file: Pencil,
  run_shell: Terminal, go_test: FlaskConical, go_vet: Microscope, go_build: Hammer,
}

// settleRunningCards 把没等到结果的工具卡片标记为结束。
// 中断 / panic / 事件丢失时工具不会回 tool_result，不处理会一直显示「执行中」。
function settleRunningCards(m: Msg[]): Msg[] {
  let changed = false
  const next = m.map(x => {
    if (!x.running) return x
    changed = true
    return { ...x, running: false, content: x.content ? `${x.content}\n（未返回结果）` : '（未返回结果）' }
  })
  return changed ? next : m
}

// mergeToolResult 把工具结果合并进对应的「执行中」卡片；同名并行调用取最近的未完成项，
// 找不到匹配（例如事件丢失或历史回放）时追加一条结果卡片。
function mergeToolResult(m: Msg[], tool: string, content: string): Msg[] {
  for (let i = m.length - 1; i >= 0; i--) {
    if (m[i].role === 'tool' && m[i].running && m[i].tool === tool) {
      const next = [...m]
      next[i] = { role: 'tool', tool, content }
      return next
    }
  }
  return [...m, { role: 'tool', tool, content }]
}

export default function ThreadPanel({ activeId, activeTitle, wsPath, runningHint, pendingApprovals, onApprovalDone }: {
  activeId: string
  activeTitle: string
  wsPath: string
  runningHint: boolean
  pendingApprovals: PendingApproval[]
  onApprovalDone: (id: string) => void
}) {
  const [messages, setMessages] = useState<Msg[]>([])
  const [input, setInput] = useState('')
  const [running, setRunning] = useState(false)
  const [usage, setUsage] = useState<Usage | null>(null)
  const [modelName, setModelName] = useState('')
  const [agentId, setAgentId] = useState('')
  // 本轮累计估算成本（llm_usage 事件按调用粒度累加，单位元）
  const [cost, setCost] = useState(0)
  const [threadTitle, setThreadTitle] = useState('新线程')
  const [openSteps, setOpenSteps] = useState<Record<number, boolean>>({})
  // 仅当用户停在底部时自动跟随流式输出；向上翻阅时不抢滚动位置
  const [atBottom, setAtBottom] = useState(true)
  const atBottomRef = useRef(true)

  // 模型列表（Composer 工具条）
  const [models, setModels] = useState<any[]>([])
  const [activeModelId, setActiveModelId] = useState('')

  const scrollRef = useRef<HTMLDivElement>(null)
  const streamingRef = useRef(false)
  // 中文输入法：Enter 是选词确认，不是发送。部分浏览器 compositionend 早于 keydown，
  // 所以同时用 composition 事件与 nativeEvent.isComposing 双重判断。
  const composingRef = useRef(false)

  // 切换线程时清空并载入该会话的持久化消息历史（重启应用后可恢复）
  useEffect(() => {
    let cancelled = false
    setMessages([])
    setUsage(null)
    setCost(0)
    setModelName('')
    setAgentId('')
    setRunning(false)
    setOpenSteps({})
    atBottomRef.current = true
    setAtBottom(true)
    if (activeId) {
      ChatService.GetMessages(activeId).then((hist: any[] | null) => {
        if (cancelled) return
        setMessages((hist ?? []).map(m => ({
          role: m.role as Msg['role'],
          content: m.content ?? '',
          tool: m.tool,
        })))
      }).catch(() => { /* 会话可能刚被删除 */ })
    }
    return () => { cancelled = true }
  }, [activeId])

  // 后端仍在执行该线程（例如切回运行中的线程）：恢复「执行中/中断」状态，
  // 否则会出现可点发送但被后端拒绝、也点不到中断的情况。
  useEffect(() => {
    if (runningHint) setRunning(true)
  }, [activeId, runningHint])

  useEffect(() => {
    ConfigService.GetConfig().then((c: any) => {
      setModels(c.models ?? [])
      setActiveModelId(c.activeModelId ?? '')
      const active = (c.models ?? []).find((m: any) => m.id === c.activeModelId)
      setModelName(active?.name ?? '')
    })
  }, [])

  useEffect(() => {
    const off = Events.On('agent:event', (ev: any) => {
      const e: AgentEvent = ev.data
      if (!e || e.sessionId !== activeId) return
      switch (e.type) {
        case 'chat_started':
          streamingRef.current = true
          setCost(0)
          if (e.data?.agentId) setAgentId(e.data.agentId)
          if (e.data?.model) setModelName(e.data.model)
          break
        case 'chat_delta':
          streamingRef.current = true
          setMessages(m => {
            const last = m[m.length - 1]
            if (!last || last.role !== 'assistant') {
              return [...m, { role: 'assistant', content: e.data?.delta ?? '' }]
            }
            return [...m.slice(0, -1), { ...last, content: last.content + (e.data?.delta ?? '') }]
          })
          break
        case 'tool_call': {
          // 工具调用开始：先落一张「执行中」卡片，tool_result 到达时就地替换
          const tool = e.data?.tool ?? ''
          const args = e.data?.args ?? ''
          setMessages(m => [...m, { role: 'tool', tool, content: args, running: true }])
          break
        }
        case 'tool_result': {
          streamingRef.current = false
          setMessages(m => mergeToolResult(m, e.data?.tool ?? '', e.data?.content ?? ''))
          break
        }
        case 'llm_usage':
          // 按调用粒度累加估算成本（元）；单价在设置页配置
          setCost(c => c + (e.data?.costIn ?? 0) + (e.data?.costOut ?? 0))
          break
        case 'chat_done':
          streamingRef.current = false
          setRunning(false)
          setMessages(m => {
            const settled = settleRunningCards(m)
            if (e.data?.content == null) return settled
            const last = settled[settled.length - 1]
            const done: Msg = { role: 'assistant', content: e.data.content }
            return last && last.role === 'assistant' ? [...settled.slice(0, -1), done] : [...settled, done]
          })
          if (e.data?.totalTokens) {
            setUsage({
              promptTokens: e.data.promptTokens ?? 0,
              completionTokens: e.data.completionTokens ?? 0,
              totalTokens: e.data.totalTokens,
            })
          }
          break
        case 'chat_error':
          streamingRef.current = false
          setRunning(false)
          setMessages(m => [...settleRunningCards(m), { role: 'error', content: e.data?.message ?? '未知错误' }])
          break
        case 'model_fallback':
          // 头部模型名跟随实际在用的模型，避免切换后仍显示主模型
          if (e.data?.to) setModelName(e.data.to)
          setMessages(m => [...m, {
            role: 'system',
            content: `⚠ 主模型不可用，已切换到「${e.data?.to}」（${e.data?.reason}）`,
          }])
          break
      }
    })
    return () => { off() }
  }, [activeId])

  const scrollToBottom = (behavior: ScrollBehavior = 'auto') => {
    const el = scrollRef.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior })
    atBottomRef.current = true
    setAtBottom(true)
  }

  const onMessagesScroll = () => {
    const el = scrollRef.current
    if (!el) return
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 48
    atBottomRef.current = nearBottom
    setAtBottom(nearBottom)
  }

  // 用户上翻阅读时不抢滚动位置；只有停在底部才跟随新消息
  useEffect(() => {
    if (atBottomRef.current) scrollToBottom()
  }, [messages, pendingApprovals])

  const send = () => {
    const text = input.trim()
    if (!text || running || !activeId) return
    setInput('')
    setUsage(null)
    if (messages.length === 0) setThreadTitle(text.slice(0, 30))
    atBottomRef.current = true
    setAtBottom(true)
    setMessages(m => [...m, { role: 'user', content: text }])
    setRunning(true)
    ChatService.SendMessage(activeId, text).catch(err => {
      setRunning(false)
      // 发送失败把草稿还回输入框（若用户已重新输入则不覆盖），方便直接重试
      setInput(prev => prev || text)
      setMessages(m => [...m, { role: 'error', content: String(err) }])
    })
  }

  const switchModel = async (id: string) => {
    const prev = activeModelId
    try {
      setActiveModelId(id)
      // 只切换激活项，避免「读整份配置 → 覆盖保存」丢失其他并发修改
      await ConfigService.SetActiveModel(id)
      const m = models.find((x: any) => x.id === id)
      setModelName(m?.name ?? '')
    } catch {
      setActiveModelId(prev) // 失败回滚，避免 UI 与实际配置不一致
    }
  }

  const cancel = () => {
    if (activeId) ChatService.Cancel(activeId)
  }

  const isEmpty = messages.length === 0

  return (
    <div className="thread">
      <div className="thread-header">
        <span className="thread-header-title">{activeTitle || threadTitle}</span>
        {agentId && <Badge variant="outline" className="font-mono text-[10px]">Agent {agentId.slice(0, 8)}</Badge>}
        {modelName && <span className="thread-header-model" title="当前主模型">{modelName}</span>}
        <span className="spacer" />
        <span className="usage-inline">
          {usage ? `Tokens ${usage.totalTokens}（入 ${usage.promptTokens} / 出 ${usage.completionTokens}）` : ''}
          {cost > 0 ? `${usage ? ' · ' : ''}成本 ¥${cost.toFixed(4)}` : ''}
        </span>
      </div>

      <div className="chat-scroll">
        <div className="chat-messages" ref={scrollRef} onScroll={onMessagesScroll}>
        {isEmpty && (
          <div className="chat-empty">
            <div className="chat-empty-icon"><Bot className="size-8" /></div>
            <h2>ai-dog-agent</h2>
            <p>多 Agent 编码开发助手 · 主 Agent 已就绪</p>
            {wsPath
              ? <p>工作区已就绪。试试：<span className="text-primary">「理解一下这个项目」</span> — 主 Agent 会分派 explorer 子 Agent 分析结构、依赖、入口与符号。</p>
              : <p>在左侧「项目」中选择目录后，即可进行项目理解与代码探索。</p>}
          </div>
        )}
        {messages.map((m, i) => {
          if (m.role === 'assistant') {
            return (
              <div key={i} className={`msg assistant${streamingRef.current && i === messages.length - 1 ? ' streaming' : ''}`}>
                <div className="avatar">AI</div>
                <div className="body">
                  <MarkdownBody content={m.content} />
                </div>
              </div>
            )
          }
          if (m.role === 'tool') {
            const open = openSteps[i] ?? false
            const running = m.running === true
            const StepIcon = toolIcons[m.tool ?? ''] ?? Wrench
            const firstLine = (m.content || '').split('\n')[0]
            const preview = running
              ? (firstLine ? `参数: ${firstLine}` : '执行中…')
              : (firstLine || '(无输出)')
            const toggle = () => setOpenSteps(s => ({ ...s, [i]: !open }))
            return (
              <div
                key={i}
                className={`step-card${open ? ' open' : ''}${running ? ' running' : ''}`}
                role="button"
                tabIndex={0}
                aria-expanded={open}
                aria-label={`工具调用 ${m.tool || '未知工具'}${running ? '（执行中）' : ''}`}
                onClick={toggle}
                onKeyDown={e => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    toggle()
                  }
                }}
              >
                <div className="step-head">
                  <span className="step-icon"><StepIcon className="size-3.5" /></span>
                  <span className="step-name">{m.tool}</span>
                  {running && <span className="step-state">执行中</span>}
                  <span className="step-preview">{preview.length > 60 ? preview.slice(0, 60) + '…' : preview}</span>
                  <span className="step-caret">{open ? '▾' : '▸'}</span>
                </div>
                {open && <pre className="step-body">{(m.content || '').length > 2000 ? m.content.slice(0, 2000) + '\n… (已截断)' : m.content}</pre>}
              </div>
            )
          }
          return <div key={i} className={`msg ${m.role}`}>{m.content}</div>
        })}
        {pendingApprovals.map(a => (
          <ApprovalCard
            key={a.id}
            approval={a}
            onDone={id => onApprovalDone(id)}
          />
        ))}
        </div>
        {!atBottom && (
          <Button
            variant="secondary"
            size="icon-sm"
            className="scroll-bottom"
            title="回到底部"
            aria-label="回到底部"
            onClick={() => scrollToBottom('smooth')}
          >
            <ArrowDown />
          </Button>
        )}
      </div>

      <div className="composer">
        <div className="composer-box">
          <Textarea
            className="chat-input"
            rows={2}
            value={input}
            placeholder={running ? 'Agent 执行中…' : '输入需求，Enter 发送，Shift+Enter 换行'}
            onChange={e => setInput(e.target.value)}
            onCompositionStart={() => { composingRef.current = true }}
            onCompositionEnd={() => { composingRef.current = false }}
            onKeyDown={e => {
              if (e.key !== 'Enter' || e.shiftKey) return
              // 输入法合成中的 Enter 用于确认候选词，不能当发送
              if (composingRef.current || e.nativeEvent.isComposing) return
              e.preventDefault()
              send()
            }}
          />
          <div className="composer-toolbar">
            {models.length > 0 && (
              <Select value={activeModelId} onValueChange={switchModel}>
                <SelectTrigger size="sm" className="max-w-48 min-w-0" title="切换模型（下一轮生效）">
                  <SelectValue placeholder="选择模型" />
                </SelectTrigger>
                <SelectContent>
                  {models.map((m: any) => (
                    <SelectItem key={m.id} value={m.id}>
                      {m.name === m.model ? m.name : `${m.name} · ${m.model}`}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            <span className="spacer" />
            {running
              ? <Button variant="destructive" size="sm" onClick={cancel}><Square className="size-3" /> 中断</Button>
              : <Button size="sm" onClick={send} disabled={!input.trim()}>发送 <SendHorizontal className="size-3.5" /></Button>}
          </div>
        </div>
      </div>
    </div>
  )
}
