import { Check, ShieldAlert, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ApprovalService } from '../../bindings/ai-dog-agent/internal/service/index.js'

export interface PendingApproval {
  id: string
  tool: string
  path: string
  preview: string
}

interface Props {
  approval: PendingApproval
  onDone: (id: string) => void
}

export default function ApprovalCard({ approval, onDone }: Props) {
  const decide = async (approved: boolean, remember: boolean) => {
    try {
      await ApprovalService.Decide(approval.id, approved, remember)
    } finally {
      onDone(approval.id)
    }
  }

  return (
    <div className="approval-card">
      <div className="approval-head">
        <Badge variant="outline" className="approval-badge gap-1"><ShieldAlert className="size-3" /> 需要审批</Badge>
        <span className="approval-tool">{approval.tool}</span>
        <span className="approval-path" title={approval.path}>{approval.path}</span>
      </div>
      <pre className="approval-diff">{approval.preview}</pre>
      <div className="approval-actions">
        <Button size="sm" onClick={() => decide(true, false)}><Check className="size-3.5" /> 批准</Button>
        <Button variant="secondary" size="sm" onClick={() => decide(true, true)}><Check className="size-3.5" /> 批准并记住</Button>
        <Button variant="destructive" size="sm" onClick={() => decide(false, false)}><X className="size-3.5" /> 拒绝</Button>
      </div>
      <p className="approval-hint">批准并记住：本次会话内对同一文件不再询问。拒绝后 Agent 将收到拒绝原因。</p>
    </div>
  )
}
