import { useState } from 'react'
import { Check, Loader2, ShieldAlert, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ApprovalService } from '../../bindings/ai-dog-agent/internal/service/index.js'

export interface PendingApproval {
  id: string
  agentId?: string
  tool: string
  path: string
  preview: string
}

interface Props {
  approval: PendingApproval
  onDone: (id: string) => void
}

export default function ApprovalCard({ approval, onDone }: Props) {
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const decide = async (approved: boolean, remember: boolean) => {
    if (submitting) return
    setSubmitting(true)
    setError('')
    try {
      await ApprovalService.Decide(approval.id, approved, remember)
      // 只有后端确认受理后才移除卡片；失败时保留，允许重试
      onDone(approval.id)
    } catch (e: any) {
      setError(typeof e === 'string' ? e : e?.message ?? String(e))
      setSubmitting(false)
    }
  }

  return (
    <div className="approval-card">
      <div className="approval-head">
        <Badge variant="outline" className="approval-badge gap-1"><ShieldAlert className="size-3" /> 需要审批</Badge>
        <span className="approval-tool">{approval.tool}</span>
        {approval.agentId && (
          <span className="approval-path" title={approval.agentId}>Agent {approval.agentId.slice(0, 8)}</span>
        )}
        <span className="approval-path" title={approval.path}>{approval.path}</span>
      </div>
      <pre className="approval-diff">{approval.preview}</pre>
      <div className="approval-actions">
        <Button size="sm" disabled={submitting} onClick={() => decide(true, false)}>
          {submitting ? <Loader2 className="size-3.5 animate-spin" /> : <Check className="size-3.5" />} 批准
        </Button>
        <Button variant="secondary" size="sm" disabled={submitting} onClick={() => decide(true, true)}>
          <Check className="size-3.5" /> 批准并记住
        </Button>
        <Button variant="destructive" size="sm" disabled={submitting} onClick={() => decide(false, false)}>
          <X className="size-3.5" /> 拒绝
        </Button>
      </div>
      {error && <p className="approval-hint text-destructive">审批提交失败：{error}</p>}
      <p className="approval-hint">批准并记住：本次会话内对同一文件不再询问。拒绝后 Agent 将收到拒绝原因。</p>
    </div>
  )
}
