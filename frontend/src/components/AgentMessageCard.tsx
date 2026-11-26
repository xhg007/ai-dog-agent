import { useState } from 'react'
import { ArrowRight, ChevronDown, MessageSquare } from 'lucide-react'
import { Badge } from '@/components/ui/badge'

// 后端 eventbus.AgentMessageData 的前端镜像。
export interface AgentMessageData {
  from: string
  fromName?: string
  to: string
  toName?: string
  content: string
  reply?: string
  depth?: number
  error?: string
}

// AgentMessageCard 展示一次 agent-to-agent 私聊：来源 → 目标、请求、回复与错误。
// 默认折叠；出错时默认展开，便于直接看到失败原因。
export default function AgentMessageCard({ msg }: { msg: AgentMessageData }) {
  const [open, setOpen] = useState(!!msg.error)
  const from = msg.fromName || msg.from || 'agent'
  const to = msg.toName || msg.to || 'agent'
  const depth = msg.depth ?? 0

  return (
    <div className={`agent-msg${msg.error ? ' error' : ''}${open ? ' open' : ''}`}>
      <button
        type="button"
        className="agent-msg-head"
        aria-expanded={open}
        aria-label={`Agent 私聊 ${from} 到 ${to}`}
        onClick={() => setOpen(v => !v)}
      >
        <span className="agent-msg-icon"><MessageSquare className="size-3.5" /></span>
        <span className="agent-msg-from">{from}</span>
        <ArrowRight className="size-3 opacity-60" />
        <span className="agent-msg-to">{to}</span>
        {depth > 1 && (
          <Badge variant="outline" className="agent-msg-depth">#{depth}</Badge>
        )}
        {msg.error && <span className="agent-msg-flag">失败</span>}
        <span className="spacer" />
        <ChevronDown className={`size-3.5 transition-transform${open ? ' rotate-180' : ''}`} />
      </button>
      {open && (
        <div className="agent-msg-body">
          <div className="agent-msg-label">请求</div>
          <pre className="agent-msg-text">{msg.content || '(空)'}</pre>
          {msg.reply && (
            <>
              <div className="agent-msg-label">回复</div>
              <pre className="agent-msg-text">{msg.reply}</pre>
            </>
          )}
          {msg.error && <div className="agent-msg-error">{msg.error}</div>}
        </div>
      )}
    </div>
  )
}
