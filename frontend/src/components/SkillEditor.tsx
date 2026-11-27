import { useState } from 'react'
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
  // source 为目录扫描来源（user-dir / project-dir）：只读，不写回 YAML。
  source?: string
}

// 新建 Skill 的默认值：页面标题栏的「添加 Skill」按钮也复用它。
export function newSkill(): SkillItem {
  return { id: crypto.randomUUID(), name: '新 Skill', description: '', prompt: '', enabled: true }
}

export function isDirectorySkill(s: SkillItem): boolean {
  return s.source === 'user-dir' || s.source === 'project-dir'
}

// skillKey 区分 YAML 与目录来源中可能重复的 id。
const skillKey = (s: SkillItem) => `${s.source ?? 'yaml'}:${s.id}`

// promptSummary 折叠时展示首行与字符数，避免大段指令占满设置页。
function promptSummary(p: string): string {
  const text = (p ?? '').trim()
  if (!text) return '未填写'
  const first = text.split('\n').find(line => line.trim())?.trim() ?? ''
  const head = first.slice(0, 80)
  return `${head}${text.length > 80 ? '…' : ''} · ${text.length} 字`
}

// SkillEditor 全局 Skill 与项目专属 Skill 共用。
// 目录来源（~/.agents/skills 或 .aidog/skills）只读展示，避免设置页保存时回写 YAML。
export default function SkillEditor({ skills, onChange, showAddButton = true }: {
  skills: SkillItem[]
  onChange: (next: SkillItem[]) => void
  showAddButton?: boolean
}) {
  // 指令内容默认折叠；新添加的 Skill 自动展开，方便立即填写。
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())

  const togglePrompt = (key: string) => {
    setExpanded(prev => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const update = (i: number, p: Partial<SkillItem>) =>
    onChange(skills.map((s, j) => j === i ? { ...s, ...p } : s))
  const remove = (i: number) => {
    if (isDirectorySkill(skills[i])) return
    onChange(skills.filter((_, j) => j !== i))
  }
  const add = () => {
    const s = newSkill()
    onChange([...skills, s])
    setExpanded(prev => new Set(prev).add(skillKey(s)))
  }

  return (
    <>
      {skills.length === 0 && <p className="hint">暂无 Skill。</p>}
      {skills.map((s, i) => {
        const readOnly = isDirectorySkill(s)
        const key = skillKey(s)
        const isExpanded = expanded.has(key)
        return (
          <div key={key} className="model-row">
            <div style={{ flex: 1 }}>
              <div className="form-grid">
                <Label>名称</Label>
                <Input value={s.name} disabled={readOnly}
                  onChange={e => update(i, { name: e.target.value })} />
                <Label>说明</Label>
                <Input value={s.description} disabled={readOnly}
                  placeholder="例如：Go 代码审查清单"
                  onChange={e => update(i, { description: e.target.value })} />
                <Label>指令内容</Label>
                <div className="skill-prompt">
                  <div className="skill-prompt-head">
                    <span className="hint">{promptSummary(s.prompt)}</span>
                    <Button
                      type="button"
                      variant="secondary"
                      size="xs"
                      onClick={() => togglePrompt(key)}
                    >
                      {isExpanded ? '收起' : '展开'}
                    </Button>
                  </div>
                  {isExpanded && (
                    <Textarea
                      rows={6}
                      value={s.prompt}
                      disabled={readOnly}
                      placeholder="追加给 Agent 的具体指令"
                      onChange={e => update(i, { prompt: e.target.value })}
                    />
                  )}
                </div>
                <Label>启用</Label>
                <Switch checked={s.enabled} disabled={readOnly}
                  onCheckedChange={v => update(i, { enabled: v })} />
              </div>
              {!readOnly && (
                <div className="row-actions">
                  <Button
                    variant="destructive"
                    size="icon-sm"
                    title="删除 Skill"
                    aria-label={`删除 Skill ${s.name || s.id}`}
                    onClick={() => remove(i)}
                  ><Trash2 /></Button>
                </div>
              )}
            </div>
            <div className="meta">
              {readOnly ? '目录来源 · 只读' : s.enabled ? '已启用' : '未启用'}
            </div>
          </div>
        )
      })}
      {showAddButton && (
        <div className="row-actions">
          <Button variant="secondary" size="sm" onClick={add}><Plus className="size-3.5" /> 添加 Skill</Button>
        </div>
      )}
    </>
  )
}
