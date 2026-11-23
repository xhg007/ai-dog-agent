import { useEffect, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { ChatService } from '../../bindings/ai-dog-agent/internal/agent/index.js'
import { ConfigService } from '../../bindings/ai-dog-agent/internal/service/index.js'
import ApprovalCard, { PendingApproval } from './ApprovalCard'
import MarkdownBody from './MarkdownBody'

interface Msg {
  role: 'user' | 'assistant' | 'error' | 'tool' | 'system'
  content: string
  tool?: string
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

const toolIcons: Record<string, string> = {
  read_file: '📄', list_files: '📁', search_code: '🔍',
  git_diff: '🌿', git_status: '🌿', project_summary: '🧭',
  dispatch_explorer: '🧭',
  write_file: '✏️', edit_file: '✏️',
  run_shell: '⌨️', go_test: '🧪', go_vet: '🔬', go_build: '🔨',
}

export default function ThreadPanel({ activeId, activeTitle, wsPath, onThreadsChanged, pendingApprovals, onApprovalDone }: {
  activeId: string
  activeTitle: string
  wsPath: string
  onThreadsChanged: () => void
  pendingApprovals: PendingApproval[]
  onApprovalDone: (id: string) => void
}) {
  const [messages, setMessages] = useState<Msg[]>([])
  const [input, setInput] = useState('')
  const [running, setRunning] = useState(false)
  const [usage, setUsage] = useState<Usage | null>(null)
  const [, setModelName] = useState('')
  const [agentId, setAgentId] = useState('')
  const [threadTitle, setThreadTitle] = useState('新线程')
  const [openSteps, setOpenSteps] = useState<Record<number, boolean>>({})

  // 模型列表（Composer 工具条）
  const [models, setModels] = useState<any[]>([])
  const [activeModelId, setActiveModelId] = useState('')

  const scrollRef = useRef<HTMLDivElement>(null)
  const streamingRef = useRef(false)

  // 切换线程时清空并载入该会话的持久化消息历史（重启应用后可恢复）
  useEffect(() => {
    let cancelled = false
    setMessages([])
    setUsage(null)
    setAgentId('')
    setRunning(false)
    setOpenSteps({})
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
          if (e.data?.agentId) setAgentId(e.data.agentId)
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
        case 'tool_result':
          streamingRef.current = false
          setMessages(m => [...m, {
            role: 'tool',
            tool: e.data?.tool ?? '',
            content: e.data?.content ?? '',
          }])
          break
        case 'chat_done':
          streamingRef.current = false
          setRunning(false)
          if (e.data?.content != null) {
            setMessages(m => {
              const last = m[m.length - 1]
              const done: Msg = { role: 'assistant', content: e.data.content }
              return last && last.role === 'assistant' ? [...m.slice(0, -1), done] : [...m, done]
            })
          }
          if (e.data?.totalTokens) {
            setUsage({
              promptTokens: e.data.promptTokens ?? 0,
              completionTokens: e.data.completionTokens ?? 0,
              totalTokens: e.data.totalTokens,
            })
          }
          onThreadsChanged()
          break
        case 'chat_error':
          streamingRef.current = false
          setRunning(false)
          setMessages(m => [...m, { role: 'error', content: e.data?.message ?? '未知错误' }])
          onThreadsChanged()
          break
        case 'model_fallback':
          setMessages(m => [...m, {
            role: 'system',
            content: `⚠ 主模型不可用，已切换到「${e.data?.to}」（${e.data?.reason}）`,
          }])
          break
      }
    })
    return () => { off() }
  }, [activeId, onThreadsChanged])

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [messages, pendingApprovals])

  const send = () => {
    const text = input.trim()
    if (!text || running || !activeId) return
    setInput('')
    setUsage(null)
    if (messages.length === 0) setThreadTitle(text.slice(0, 30))
    setMessages(m => [...m, { role: 'user', content: text }])
    setRunning(true)
    ChatService.SendMessage(activeId, text).catch(err => {
      setRunning(false)
      setMessages(m => [...m, { role: 'error', content: String(err) }])
    })
  }

  const switchModel = async (id: string) => {
    try {
      setActiveModelId(id)
      const cfg: any = await ConfigService.GetConfig()
      cfg.activeModelId = id
      await ConfigService.SaveConfig(cfg)
      const m = (cfg.models ?? []).find((x: any) => x.id === id)
      setModelName(m?.name ?? '')
    } catch { /* 忽略 */ }
  }

  const cancel = () => {
    if (activeId) ChatService.Cancel(activeId)
  }

  const isEmpty = messages.length === 0

  return (
    <div className="thread">
      <div className="thread-header">
        <span className="thread-header-title">{activeTitle || threadTitle}</span>
        {agentId && <span className="chip">Agent {agentId.slice(0, 8)}</span>}
        <span className="spacer" />
        <span className="usage-inline">
          {usage ? `Tokens ${usage.totalTokens}（入 ${usage.promptTokens} / 出 ${usage.completionTokens}）` : ''}
        </span>
      </div>

      <div className="chat-messages" ref={scrollRef}>
        {isEmpty && (
          <div className="chat-empty">
            <h2>ai-dog-agent</h2>
            <p>多 Agent 编码开发助手 · 主 Agent 已就绪</p>
            {wsPath
              ? <p>工作区已就绪。试试：「理解一下这个项目」— 主 Agent 会分派 explorer 子 Agent 分析结构、依赖、入口与符号。</p>
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
            const icon = toolIcons[m.tool ?? ''] ?? '🛠'
            const preview = (m.content || '').split('\n')[0] || '(无输出)'
            return (
              <div key={i} className={`step-card${open ? ' open' : ''}`} onClick={() => setOpenSteps(s => ({ ...s, [i]: !open }))}>
                <div className="step-head">
                  <span className="step-icon">{icon}</span>
                  <span className="step-name">{m.tool}</span>
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

      <div className="composer">
        <div className="composer-box">
          <textarea
            className="chat-input"
            rows={2}
            value={input}
            placeholder={running ? 'Agent 执行中…' : '输入需求，Enter 发送，Shift+Enter 换行'}
            onChange={e => setInput(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                send()
              }
            }}
          />
          <div className="composer-toolbar">
            <select
              className="model-select"
              value={activeModelId}
              onChange={e => switchModel(e.target.value)}
              title="切换模型（新线程生效）"
            >
              {models.map((m: any) => (
                <option key={m.id} value={m.id}>{m.name} · {m.model}</option>
              ))}
            </select>
            <span className="composer-mode">Local</span>
            <span className="spacer" />
            {running
              ? <button className="btn danger" onClick={cancel}>■ 中断</button>
              : <button className="btn" onClick={send} disabled={!input.trim()}>发送 ↑</button>}
          </div>
        </div>
      </div>
    </div>
  )
}
