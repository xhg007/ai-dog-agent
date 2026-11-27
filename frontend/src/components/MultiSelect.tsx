import { useMemo, useState } from 'react'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export interface MultiSelectOption {
  value: string
  label?: string
  hint?: string
  group?: string
}

// MultiSelect 用于 Agent 配置页的 tools / skills / mcp_servers：
// 已选项以 tag 展示，可展开列表勾选，也可手动输入任意值。
export default function MultiSelect({
  value,
  options,
  onChange,
  placeholder = '手动输入后回车添加',
}: {
  value: string[]
  options: MultiSelectOption[]
  onChange: (next: string[]) => void
  placeholder?: string
}) {
  const [query, setQuery] = useState('')
  const [manual, setManual] = useState('')
  const [open, setOpen] = useState(false)

  const selected = useMemo(() => new Set(value), [value])
  const optionByValue = useMemo(() => {
    const m = new Map<string, MultiSelectOption>()
    for (const o of options) m.set(o.value, o)
    return m
  }, [options])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return options
    return options.filter(o =>
      o.value.toLowerCase().includes(q) ||
      (o.label ?? '').toLowerCase().includes(q) ||
      (o.hint ?? '').toLowerCase().includes(q)
    )
  }, [options, query])

  const grouped = useMemo(() => {
    const groups: { name: string; items: MultiSelectOption[] }[] = []
    for (const o of filtered) {
      const name = o.group || '其他'
      let g = groups.find(x => x.name === name)
      if (!g) {
        g = { name, items: [] }
        groups.push(g)
      }
      g.items.push(o)
    }
    return groups
  }, [filtered])

  const toggle = (v: string) => {
    onChange(selected.has(v) ? value.filter(x => x !== v) : [...value, v])
  }
  const addManual = () => {
    const v = manual.trim()
    if (!v) return
    if (!selected.has(v)) onChange([...value, v])
    setManual('')
  }

  return (
    <div className="multiselect">
      <div className="multiselect-tags">
        {value.length === 0 && <span className="hint">未选择；可展开列表勾选，或手动输入。</span>}
        {value.map(v => (
          <span key={v} className="multiselect-tag" title={optionByValue.get(v)?.hint}>
            {optionByValue.get(v)?.label ?? v}
            <button type="button" aria-label={`移除 ${v}`} onClick={() => toggle(v)}>
              <X className="size-3" />
            </button>
          </span>
        ))}
      </div>
      <div className="row-actions">
        <Input
          className="multiselect-input"
          value={manual}
          placeholder={placeholder}
          onChange={e => setManual(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter') {
              e.preventDefault()
              addManual()
            }
          }}
        />
        <Button type="button" size="xs" variant="secondary" onClick={addManual}>添加</Button>
        <Button type="button" size="xs" variant="secondary" onClick={() => setOpen(o => !o)}>
          {open ? '收起列表' : `从列表选择（${options.length}）`}
        </Button>
      </div>
      {open && (
        <div className="multiselect-panel">
          <Input
            value={query}
            placeholder="搜索名称或说明…"
            onChange={e => setQuery(e.target.value)}
          />
          {grouped.length === 0 && <p className="hint">没有匹配项，可直接手动输入。</p>}
          {grouped.map(g => (
            <div key={g.name} className="multiselect-group">
              <div className="multiselect-group-title">{g.name}</div>
              {g.items.map(o => (
                <label key={o.value} className="multiselect-item">
                  <input
                    type="checkbox"
                    checked={selected.has(o.value)}
                    onChange={() => toggle(o.value)}
                  />
                  <span className="multiselect-item-name">{o.label ?? o.value}</span>
                  {o.label && o.label !== o.value && <span className="hint">{o.value}</span>}
                  {o.hint && <span className="hint multiselect-item-hint">{o.hint}</span>}
                </label>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
