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
        <span className="approval-badge">⚠ 需要审批</span>
        <span className="approval-tool">{approval.tool}</span>
        <span className="approval-path" title={approval.path}>{approval.path}</span>
      </div>
      <pre className="approval-diff">{approval.preview}</pre>
      <div className="approval-actions">
        <button className="btn" onClick={() => decide(true, false)}>✓ 批准</button>
        <button className="btn secondary" onClick={() => decide(true, true)}>✓ 批准并记住</button>
        <button className="btn danger" onClick={() => decide(false, false)}>✕ 拒绝</button>
      </div>
      <p className="approval-hint">批准并记住：本次会话内对同一文件不再询问。拒绝后 Agent 将收到拒绝原因。</p>
    </div>
  )
}
