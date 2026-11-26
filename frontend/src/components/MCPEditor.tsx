import { useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { MCPService } from '../../bindings/ai-dog-agent/internal/service/index.js'

export interface MCPItem {
  id: string
  name: string
  transport: string
  command: string
  args: string // 逗号分隔
  env: string
  url: string
  headers: string
  enabled: boolean
  autoApproveReadOnly: boolean
}

// usesStdio 决定表单展示哪组连接字段：auto 时有 command 优先，其次看 url。
function usesStdio(m: MCPItem): boolean {
  if (m.transport === 'stdio') return true
  if (m.transport === 'http' || m.transport === 'sse') return false
  if ((m.command ?? '').trim()) return true
  return !(m.url ?? '').trim()
}

// MCPEditor 全局 MCP 与项目专属 MCP 共用；测试连接独立于已建立的会话连接。
export default function MCPEditor({ servers, onChange }: {
  servers: MCPItem[]
  onChange: (next: MCPItem[]) => void
}) {
  const [test, setTest] = useState<Record<string, { loading?: boolean; tools?: string[]; error?: string }>>({})
  const update = (i: number, p: Partial<MCPItem>) =>
    onChange(servers.map((m, j) => j === i ? { ...m, ...p } : m))
  const remove = (i: number) => onChange(servers.filter((_, j) => j !== i))
  const add = () => onChange([...servers, {
    id: crypto.randomUUID(), name: '新 MCP Server', transport: 'auto',
    command: '', args: '', env: '', url: '', headers: '',
    enabled: true, autoApproveReadOnly: false,
  }])

  const testMCP = async (m: MCPItem) => {
    setTest(s => ({ ...s, [m.id]: { loading: true } }))
    try {
      const names = await MCPService.TestConnection({
        id: m.id,
        name: m.name,
        transport: m.transport || 'auto',
        command: m.command,
        args: m.args.split(',').map(s => s.trim()).filter(Boolean),
        env: m.env,
        url: m.url,
        headers: m.headers,
        enabled: m.enabled,
        autoApproveReadOnly: m.autoApproveReadOnly,
      } as any)
      setTest(s => ({ ...s, [m.id]: { tools: names ?? [] } }))
    } catch (e: any) {
      setTest(s => ({ ...s, [m.id]: { error: typeof e === 'string' ? e : e?.message ?? String(e) } }))
    }
  }

  return (
    <>
      {servers.length === 0 && <p className="hint">暂无 MCP Server，点击下方「添加 MCP Server」。</p>}
      {servers.map((m, i) => (
        <div key={m.id} className="model-row">
          <div style={{ flex: 1 }}>
            <div className="form-grid">
              <Label>名称</Label>
              <Input value={m.name} onChange={e => update(i, { name: e.target.value })} />
              <Label>连接方式</Label>
              <Select value={m.transport || 'auto'} onValueChange={v => update(i, { transport: v })}>
                <SelectTrigger size="sm" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">自动（有命令走 stdio，否则 HTTP）</SelectItem>
                  <SelectItem value="stdio">stdio（本地进程）</SelectItem>
                  <SelectItem value="http">HTTP（Streamable）</SelectItem>
                  <SelectItem value="sse">SSE（旧版）</SelectItem>
                </SelectContent>
              </Select>
              {usesStdio(m) ? (
                <>
                  <Label>启动命令</Label>
                  <Input value={m.command} placeholder="npx / uvx / 可执行文件路径"
                    onChange={e => update(i, { command: e.target.value })} />
                  <Label>参数</Label>
                  <Input value={m.args} placeholder="-y, @modelcontextprotocol/server-filesystem"
                    onChange={e => update(i, { args: e.target.value })} />
                  <Label>环境变量</Label>
                  <Textarea rows={2} value={m.env}
                    placeholder={'ROOT=C:\\project\nDEBUG=0'}
                    onChange={e => update(i, { env: e.target.value })} />
                </>
              ) : (
                <>
                  <Label>服务地址</Label>
                  <Input value={m.url} placeholder="https://example.com/mcp"
                    onChange={e => update(i, { url: e.target.value })} />
                  <Label>请求头</Label>
                  <Textarea rows={2} value={m.headers}
                    placeholder={'Authorization=Bearer ${MCP_TOKEN}'}
                    onChange={e => update(i, { headers: e.target.value })} />
                </>
              )}
              <Label>启用</Label>
              <Switch checked={m.enabled} onCheckedChange={v => update(i, { enabled: v })} />
              <Label>只读工具免审批</Label>
              <Switch checked={m.autoApproveReadOnly}
                onCheckedChange={v => update(i, { autoApproveReadOnly: v })} />
            </div>
            <div className="row-actions">
              <Button
                variant="outline"
                size="xs"
                disabled={test[m.id]?.loading}
                onClick={() => testMCP(m)}
                title="启动一次连接并列出工具（不影响已建立的会话连接）"
              >{test[m.id]?.loading ? '连接中…' : '测试连接'}</Button>
              <Button
                variant="destructive"
                size="icon-sm"
                title="删除 MCP Server"
                aria-label={`删除 MCP Server ${m.name || m.id}`}
                onClick={() => remove(i)}
              ><Trash2 /></Button>
            </div>
            {test[m.id] && (
              <p className={test[m.id].error ? 'hint text-destructive' : 'hint'}>
                {test[m.id].loading ? '连接中…'
                  : test[m.id].error ? `连接失败：${test[m.id].error}`
                    : `已连接，工具：${(test[m.id].tools ?? []).join(', ') || '（无）'}`}
              </p>
            )}
          </div>
          <div className="meta">{m.enabled ? '已启用' : '未启用'}</div>
        </div>
      ))}
      <div className="row-actions">
        <Button variant="secondary" size="sm" onClick={add}><Plus className="size-3.5" /> 添加 MCP Server</Button>
      </div>
    </>
  )
}
