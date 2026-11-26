import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

export interface ProjectTrustStatus {
  root: string
  status: 'trusted' | 'untrusted' | 'blocked' | string
  audit?: {
    findings?: { severity: string; rule: string; file: string; evidence: string }[]
    highCount?: number
    mediumCount?: number
    lowCount?: number
    blocked?: boolean
  }
}

// ProjectTrustDialog 首次加载/切换项目时确认 .aidog 信任边界。
// blocked：审计高危自动拉黑，解除并信任需要二次确认。
export default function ProjectTrustDialog({ open, status, busy, onTrust, onUnblockTrust, onDismiss }: {
  open: boolean
  status: ProjectTrustStatus | null
  busy: boolean
  onTrust: () => void
  onUnblockTrust: () => void
  onDismiss: () => void
}) {
  const [confirmUnblock, setConfirmUnblock] = useState(false)
  useEffect(() => {
    if (!open) setConfirmUnblock(false)
  }, [open])
  if (!status) return null

  const blocked = status.status === 'blocked'
  const findings = status.audit?.findings ?? []
  return (
    <Dialog open={open} onOpenChange={v => { if (!v) onDismiss() }}>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{blocked ? '项目配置已被审计拉黑' : '是否信任此项目配置？'}</DialogTitle>
          <DialogDescription>
            项目根：{status.root}<br />
            `.aidog` 中的 Agent / MCP / Skill 等同可执行配置，加载前需要确认来源可信。
            高危配置会被自动拉黑并禁止加载。
          </DialogDescription>
        </DialogHeader>
        <div className="hint">
          高危 {status.audit?.highCount ?? 0} · 中危 {status.audit?.mediumCount ?? 0} · 低危 {status.audit?.lowCount ?? 0}
        </div>
        {findings.length > 0 && (
          <ul className="hint" style={{ maxHeight: 180, overflow: 'auto', margin: 0, paddingLeft: 18 }}>
            {findings.map((f, i) => (
              <li key={i}>[{f.severity}] {f.rule} · {f.file} · {f.evidence}</li>
            ))}
          </ul>
        )}
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onDismiss} disabled={busy}>暂不信任</Button>
          {blocked ? (
            confirmUnblock ? (
              <Button variant="destructive" size="sm" onClick={onUnblockTrust} disabled={busy}>
                确认解除拉黑并信任
              </Button>
            ) : (
              <Button variant="destructive" size="sm" onClick={() => setConfirmUnblock(true)} disabled={busy}>
                解除拉黑并信任…
              </Button>
            )
          ) : (
            <Button size="sm" onClick={onTrust} disabled={busy}>信任并加载</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
