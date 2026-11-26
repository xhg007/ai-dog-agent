import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'

export interface SkillItem {
  id: string
  name: string
  description: string
  prompt: string
  enabled: boolean
}

// SkillEditor 全局 Skill 与项目专属 Skill 共用。
export default function SkillEditor({ skills, onChange }: {
  skills: SkillItem[]
  onChange: (next: SkillItem[]) => void
}) {
  const update = (i: number, p: Partial<SkillItem>) =>
    onChange(skills.map((s, j) => j === i ? { ...s, ...p } : s))
  const remove = (i: number) => onChange(skills.filter((_, j) => j !== i))
  const add = () => onChange([...skills, {
    id: crypto.randomUUID(), name: '新 Skill', description: '', prompt: '', enabled: true,
  }])

  return (
    <>
      {skills.length === 0 && <p className="hint">暂无 Skill，点击下方「添加 Skill」。</p>}
      {skills.map((s, i) => (
        <div key={s.id} className="model-row">
          <div style={{ flex: 1 }}>
            <div className="form-grid">
              <Label>名称</Label>
              <Input value={s.name} onChange={e => update(i, { name: e.target.value })} />
              <Label>说明</Label>
              <Input value={s.description} placeholder="例如：Go 代码审查清单"
                onChange={e => update(i, { description: e.target.value })} />
              <Label>指令内容</Label>
              <Textarea rows={3} value={s.prompt}
                placeholder="追加给 Agent 的具体指令"
                onChange={e => update(i, { prompt: e.target.value })} />
              <Label>启用</Label>
              <Switch checked={s.enabled} onCheckedChange={v => update(i, { enabled: v })} />
            </div>
            <div className="row-actions">
              <Button
                variant="destructive"
                size="icon-sm"
                title="删除 Skill"
                aria-label={`删除 Skill ${s.name || s.id}`}
                onClick={() => remove(i)}
              ><Trash2 /></Button>
            </div>
          </div>
          <div className="meta">{s.enabled ? '已启用' : '未启用'}</div>
        </div>
      ))}
      <div className="row-actions">
        <Button variant="secondary" size="sm" onClick={add}><Plus className="size-3.5" /> 添加 Skill</Button>
      </div>
    </>
  )
}
