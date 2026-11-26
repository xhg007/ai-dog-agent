import { useEffect, useState } from 'react'
import { Plus, Save, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ConfigService, MCPService, TaskService } from '../../bindings/ai-dog-agent/internal/service/index.js'
import {
  applyGlass, loadGlass, saveGlass,
  DEFAULT_GLASS_ALPHA, DEFAULT_GLASS_BLUR,
  MIN_GLASS_ALPHA, MAX_GLASS_ALPHA, MIN_GLASS_BLUR, MAX_GLASS_BLUR,
} from '../lib/glass'

interface ModelConfig {
  id: string
  name: string
  baseURL: string
  apiKey: string
  model: string
  priceIn?: number
  priceOut?: number
  timeoutSeconds?: number
  disableExtraFields?: boolean
}

interface SkillConfig {
  id: string
  name: string
  description: string
  prompt: string
  enabled: boolean
}

interface MCPServerConfig {
  id: string
  name: string
  transport: string   // auto | stdio | http | sse
  command: string
  args: string        // 逗号分隔，保存时转成数组
  env: string         // KEY=VALUE，每行一条
  url: string
  headers: string     // KEY=VALUE，每行一条
  enabled: boolean
  autoApproveReadOnly: boolean
}

// usesStdio 决定表单展示哪组连接字段：auto 时有 command 优先，其次看 url。
function usesStdio(m: MCPServerConfig): boolean {
  if (m.transport === 'stdio') return true
  if (m.transport === 'http' || m.transport === 'sse') return false
  if ((m.command ?? '').trim()) return true
  return !(m.url ?? '').trim()
}

interface AgentSpec {
  agent_id: string     // 唯一不变 ID（UUID 无连字符），保存时后端自动生成固化
  name: string
  description: string
  system_prompt: string
  model: string
  tools: string        // 逗号分隔
  skills: string
  mcp_servers: string
  max_turns: number
}

interface AppConfig {
  models: ModelConfig[]
  activeModelId: string
  skills: SkillConfig[]
  mcpServers: MCPServerConfig[]
  shellWhitelist: string[]
}

type Tab = 'models' | 'skills' | 'mcp' | 'agents' | 'appearance' | 'security' | 'usage'

interface DailyUsageRow { date: string; calls: number; inputTokens: number; outputTokens: number; costTotal: number }

const emptyAppConfig: AppConfig = { models: [], activeModelId: '', skills: [], mcpServers: [], shellWhitelist: [] }

function fromAgentSpec(s: any): AgentSpec {
  return {
    agent_id: s.agent_id ?? '',
    name: s.name ?? '',
    description: s.description ?? '',
    system_prompt: s.system_prompt ?? '',
    model: s.model ?? '',
    tools: (s.tools ?? []).join(', '),
    skills: (s.skills ?? []).join(', '),
    mcp_servers: (s.mcp_servers ?? []).join(', '),
    max_turns: s.max_turns ?? 0,
  }
}

function toAgentSpec(a: AgentSpec) {
  const list = (v: string) => v.split(',').map(s => s.trim()).filter(Boolean)
  return {
    agent_id: a.agent_id, // 回传后端沿用；后端对缺失/新配置自动生成固化
    name: a.name.trim(),
    description: a.description,
    system_prompt: a.system_prompt,
    model: a.model,
    tools: list(a.tools),
    skills: list(a.skills),
    mcp_servers: list(a.mcp_servers),
    max_turns: Number(a.max_turns) || 0,
  }
}

function SettingsPanel({ onDirtyChange }: { onDirtyChange?: (dirty: boolean) => void }) {
  const [cfg, setCfg] = useState<AppConfig>(emptyAppConfig)
  const [agents, setAgents] = useState<AgentSpec[]>([])
  const [dir, setDir] = useState('')
  const [agentDir, setAgentDir] = useState('')
  const [tab, setTab] = useState<Tab>('models')
  const [saved, setSaved] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState('')
  const [glassAlpha, setGlassAlpha] = useState(() => loadGlass().alpha)
  const [glassBlur, setGlassBlur] = useState(() => loadGlass().blur)
  // MCP 连接诊断结果（按 server ID）
  const [mcpTest, setMcpTest] = useState<Record<string, { loading?: boolean; tools?: string[]; error?: string }>>({})

  // 外观设置即时生效并持久化（不参与配置文件的保存流程）
  useEffect(() => {
    applyGlass(glassAlpha, glassBlur)
    saveGlass(glassAlpha, glassBlur)
  }, [glassAlpha, glassBlur])

  // 把脏状态上报给 App，离开设置页时统一拦截
  useEffect(() => { onDirtyChange?.(dirty) }, [dirty, onDirtyChange])

  const loadAgents = () => {
    ConfigService.GetProjectAgents()
      .then((list: any[] | null) => setAgents((list ?? []).map(fromAgentSpec)))
      .catch(() => setAgents([]))
  }

  useEffect(() => {
    ConfigService.GetConfig().then((c: any) => setCfg({
      models: c.models ?? [],
      activeModelId: c.activeModelId ?? '',
      skills: c.skills ?? [],
      shellWhitelist: c.shellWhitelist ?? [],
      mcpServers: (c.mcpServers ?? []).map((m: any) => ({
        ...m,
        transport: m.transport ?? 'auto',
        command: m.command ?? '',
        url: m.url ?? '',
        headers: m.headers ?? '',
        autoApproveReadOnly: m.autoApproveReadOnly ?? false,
        args: (m.args ?? []).join(', '),
        env: m.env ?? '',
      })),
    }))
    ConfigService.GetConfigDir().then(setDir)
    ConfigService.GetProjectAgentDir().then(setAgentDir)
    loadAgents()
  }, [])

  const patch = (p: Partial<AppConfig>) => {
    setCfg(c => ({ ...c, ...p }))
    setSaved(false)
    setDirty(true)
  }

  const updateModel = (i: number, p: Partial<ModelConfig>) =>
    patch({ models: cfg.models.map((m, j) => j === i ? { ...m, ...p } : m) })

  const addModel = () => patch({
    models: [...cfg.models, {
      id: crypto.randomUUID(), name: '新模型', baseURL: '', apiKey: '', model: '',
      priceIn: 0, priceOut: 0,
    }],
  })

  const updateSkill = (i: number, p: Partial<SkillConfig>) =>
    patch({ skills: cfg.skills.map((s, j) => j === i ? { ...s, ...p } : s) })

  const updateMCP = (i: number, p: Partial<MCPServerConfig>) =>
    patch({ mcpServers: cfg.mcpServers.map((m, j) => j === i ? { ...m, ...p } : m) })

  const updateAgent = (i: number, p: Partial<AgentSpec>) => {
    setAgents(a => a.map((x, j) => j === i ? { ...x, ...p } : x))
    setSaved(false)
    setDirty(true)
  }

  const [newWhitelistEntry, setNewWhitelistEntry] = useState('')

  const addWhitelistEntry = () => {
    const v = newWhitelistEntry.trim()
    if (!v || cfg.shellWhitelist.includes(v)) return
    patch({ shellWhitelist: [...cfg.shellWhitelist, v] })
    setNewWhitelistEntry('')
  }

  const serialize = (c: AppConfig) => ({
    ...c,
    mcpServers: c.mcpServers.map(m => ({
      ...m,
      args: m.args.split(',').map(s => s.trim()).filter(Boolean),
    })),
  })

  const saveConfig = async () => {
    setError('')
    try {
      // activeModelId 由 SetActiveModel 单独维护：保存前重新读一次当前值，
      // 避免用打开设置页时的旧快照覆盖 Composer 里刚做的模型切换。
      const latest = await ConfigService.GetConfig()
      await ConfigService.SaveConfig(serialize({
        ...cfg,
        activeModelId: latest?.activeModelId ?? cfg.activeModelId,
      }) as any)
      setSaved(true)
      setDirty(false)
    } catch (e) {
      setError(String(e))
    }
  }

  // 点击模型行立即设为当前（与 Composer 的切换语义一致）；字段编辑仍需保存
  const activateModel = async (id: string) => {
    const prev = cfg.activeModelId
    setCfg(c => ({ ...c, activeModelId: id }))
    setError('')
    try {
      // 只切换激活项，避免整份配置读改写
      await ConfigService.SetActiveModel(id)
    } catch (e) {
      setCfg(c => ({ ...c, activeModelId: prev }))
      setError(String(e))
    }
  }

  const saveCurrent = () => (tab === 'agents' ? saveAgents() : saveConfig())

  const saveAgents = async () => {
    setError('')
    try {
      for (const a of agents) {
        await ConfigService.SaveProjectAgent(toAgentSpec(a) as any)
      }
      loadAgents()
      setSaved(true)
      setDirty(false)
    } catch (e) {
      setError(String(e))
    }
  }

  const removeAgent = async (i: number) => {
    const name = agents[i].name
    setError('')
    try {
      await ConfigService.DeleteProjectAgent(name)
      loadAgents()
    } catch (e) {
      setError(String(e))
    }
  }

  // MCP 连接诊断：一次性连接并列出工具名（无需先保存配置）
  const testMCP = async (m: MCPServerConfig) => {
    setMcpTest(s => ({ ...s, [m.id]: { loading: true } }))
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
      setMcpTest(s => ({ ...s, [m.id]: { tools: names ?? [] } }))
    } catch (e: any) {
      setMcpTest(s => ({ ...s, [m.id]: { error: typeof e === 'string' ? e : e?.message ?? String(e) } }))
    }
  }

  const tabs: { id: Tab; label: string }[] = [
    { id: 'models', label: '模型' },
    { id: 'skills', label: `Skills (${cfg.skills.length})` },
    { id: 'mcp', label: `MCP (${cfg.mcpServers.length})` },
    { id: 'agents', label: `项目 Agent (${agents.length})` },
    { id: 'appearance', label: '外观' },
    { id: 'security', label: '安全' },
    { id: 'usage', label: '用量' },
  ]

  return (
    <div className="settings">
      <Tabs
        orientation="vertical"
        value={tab}
        onValueChange={v => { setTab(v as Tab); setError('') }}
        className="settings-layout"
      >
        <TabsList variant="line" className="settings-nav">
          {tabs.map(t => (
            <TabsTrigger key={t.id} value={t.id} className="justify-start text-xs">{t.label}</TabsTrigger>
          ))}
        </TabsList>

        <div className="settings-main">
          <div className="settings-scroll">
            <div className="card">
        {tab === 'models' && (
          <>
            <h3>模型配置（OpenAI 兼容）</h3>
            {cfg.models.length === 0 && <p className="hint">尚未配置模型，请点击下方「添加模型」。</p>}
            {cfg.models.map((m, i) => (
              <div
                key={m.id}
                className={`model-row${cfg.activeModelId === m.id ? ' active' : ''}`}
                onClick={() => activateModel(m.id)}
              >
                <div style={{ flex: 1 }}>
                  <div className="form-grid">
                    <Label>显示名</Label>
                    <Input value={m.name} onClick={e => e.stopPropagation()}
                      onChange={e => updateModel(i, { name: e.target.value })} />
                    <Label>Base URL</Label>
                    <Input value={m.baseURL} placeholder="https://api.deepseek.com/v1"
                      onClick={e => e.stopPropagation()}
                      onChange={e => updateModel(i, { baseURL: e.target.value })} />
                    <Label>Model ID</Label>
                    <Input value={m.model} placeholder="deepseek-chat"
                      onClick={e => e.stopPropagation()}
                      onChange={e => updateModel(i, { model: e.target.value })} />
                    <Label>API Key</Label>
                    <Input type="password" value={m.apiKey}
                      onClick={e => e.stopPropagation()}
                      onChange={e => updateModel(i, { apiKey: e.target.value })} />
                    <Label>输入单价/1M</Label>
                    <Input type="number" value={m.priceIn}
                      onClick={e => e.stopPropagation()}
                      onChange={e => updateModel(i, { priceIn: Number(e.target.value) })} />
                    <Label>输出单价/1M</Label>
                    <Input type="number" value={m.priceOut}
                      onClick={e => e.stopPropagation()}
                      onChange={e => updateModel(i, { priceOut: Number(e.target.value) })} />
                    <Label>超时（秒）</Label>
                    <Input type="number" value={m.timeoutSeconds ?? 0}
                      placeholder="0 = 默认 300"
                      onClick={e => e.stopPropagation()}
                      onChange={e => updateModel(i, { timeoutSeconds: Number(e.target.value) })} />
                    <Label>发送 agent_id</Label>
                    <Switch checked={!m.disableExtraFields}
                      onCheckedChange={v => updateModel(i, { disableExtraFields: !v })} />
                  </div>
                  <div className="row-actions">
                    <Button
                      variant="destructive"
                      size="icon-sm"
                      title="删除模型"
                      aria-label={`删除模型 ${m.name || m.model || m.id}`}
                      onClick={e => {
                        e.stopPropagation()
                        patch({ models: cfg.models.filter(x => x.id !== m.id) })
                      }}
                    ><Trash2 /></Button>
                  </div>
                </div>
                <div className="meta">{cfg.activeModelId === m.id ? '当前使用' : '点击立即设为当前'}</div>
              </div>
            ))}
            <div className="row-actions">
              <Button variant="secondary" size="sm" onClick={addModel}><Plus className="size-3.5" /> 添加模型</Button>
            </div>
            <p className="hint">
              配置文件：{dir || '…'}<br />
              点击模型行会立即设为当前模型；其它字段修改需点右下角「保存」。
              一个 OpenAI 兼容配置即可覆盖 DeepSeek / Qwen / GLM / OpenAI 等；保存后对当前会话下一轮生效。
              超时 0 表示默认 300s；「发送 agent_id」关闭后请求体不再带 agent_id/user_id（严格端点兼容）。
            </p>
          </>
        )}

        {tab === 'skills' && (
          <>
            <h3>Skills（可复用指令包）</h3>
            <p className="hint">启用的 Skill 会追加到主 Agent 的动态上下文尾部（不破坏提示词缓存前缀）；保存后对当前会话下一轮生效。</p>
            {cfg.skills.length === 0 && <p className="hint">暂无 Skill，点击下方「添加 Skill」。</p>}
            {cfg.skills.map((s, i) => (
              <div key={s.id} className="model-row">
                <div style={{ flex: 1 }}>
                  <div className="form-grid">
                    <Label>名称</Label>
                    <Input value={s.name} onChange={e => updateSkill(i, { name: e.target.value })} />
                    <Label>说明</Label>
                    <Input value={s.description} placeholder="例如：Go 代码审查清单"
                      onChange={e => updateSkill(i, { description: e.target.value })} />
                    <Label>指令内容</Label>
                    <Textarea rows={3} value={s.prompt}
                      placeholder="追加给 Agent 的具体指令"
                      onChange={e => updateSkill(i, { prompt: e.target.value })} />
                    <Label>启用</Label>
                    <Switch checked={s.enabled} onCheckedChange={v => updateSkill(i, { enabled: v })} />
                  </div>
                  <div className="row-actions">
                    <Button
                      variant="destructive"
                      size="icon-sm"
                      title="删除 Skill"
                      aria-label={`删除 Skill ${s.name || s.id}`}
                      onClick={() => patch({ skills: cfg.skills.filter(x => x.id !== s.id) })}
                    ><Trash2 /></Button>
                  </div>
                </div>
                <div className="meta">{s.enabled ? '已启用' : '未启用'}</div>
              </div>
            ))}
            <div className="row-actions">
              <Button variant="secondary" size="sm" onClick={() => patch({
                skills: [...cfg.skills, {
                  id: crypto.randomUUID(), name: '新 Skill', description: '', prompt: '', enabled: true,
                }],
              })}><Plus className="size-3.5" /> 添加 Skill</Button>
            </div>
          </>
        )}

        {tab === 'mcp' && (
          <>
            <h3>MCP Server（外部工具）</h3>
            <p className="hint">
              支持 stdio 与 HTTP/SSE 远程连接。已启用 Server 在新建会话或配置变更后连接，工具以 mcp_&lt;server&gt;_&lt;tool&gt; 暴露给主 Agent；
              默认每次调用需审批（可「批准并记住」）。「只读工具免审批」仅对服务器声明 readOnlyHint=true 的工具跳过审批，默认关闭——
              该注解来自服务器，不可完全信任。
            </p>
            {cfg.mcpServers.length === 0 && <p className="hint">暂无 MCP Server，点击下方「添加 MCP Server」。</p>}
            {cfg.mcpServers.map((m, i) => (
              <div key={m.id} className="model-row">
                <div style={{ flex: 1 }}>
                  <div className="form-grid">
                    <Label>名称</Label>
                    <Input value={m.name} onChange={e => updateMCP(i, { name: e.target.value })} />
                    <Label>连接方式</Label>
                    <Select value={m.transport || 'auto'} onValueChange={v => updateMCP(i, { transport: v })}>
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
                          onChange={e => updateMCP(i, { command: e.target.value })} />
                        <Label>参数</Label>
                        <Input value={m.args} placeholder="-y, @modelcontextprotocol/server-filesystem"
                          onChange={e => updateMCP(i, { args: e.target.value })} />
                        <Label>环境变量</Label>
                        <Textarea rows={2} value={m.env}
                          placeholder={'ROOT=C:\\project\nDEBUG=0'}
                          onChange={e => updateMCP(i, { env: e.target.value })} />
                      </>
                    ) : (
                      <>
                        <Label>服务地址</Label>
                        <Input value={m.url} placeholder="https://example.com/mcp"
                          onChange={e => updateMCP(i, { url: e.target.value })} />
                        <Label>请求头</Label>
                        <Textarea rows={2} value={m.headers}
                          placeholder={'Authorization=Bearer ${MCP_TOKEN}'}
                          onChange={e => updateMCP(i, { headers: e.target.value })} />
                      </>
                    )}
                    <Label>启用</Label>
                    <Switch checked={m.enabled} onCheckedChange={v => updateMCP(i, { enabled: v })} />
                    <Label>只读工具免审批</Label>
                    <Switch checked={m.autoApproveReadOnly}
                      onCheckedChange={v => updateMCP(i, { autoApproveReadOnly: v })} />
                  </div>
                  <div className="row-actions">
                    <Button
                      variant="outline"
                      size="xs"
                      disabled={mcpTest[m.id]?.loading}
                      onClick={() => testMCP(m)}
                      title="启动一次连接并列出工具（不影响已建立的会话连接）"
                    >{mcpTest[m.id]?.loading ? '连接中…' : '测试连接'}</Button>
                    <Button
                      variant="destructive"
                      size="icon-sm"
                      title="删除 MCP Server"
                      aria-label={`删除 MCP Server ${m.name || m.id}`}
                      onClick={() => patch({ mcpServers: cfg.mcpServers.filter(x => x.id !== m.id) })}
                    ><Trash2 /></Button>
                  </div>
                  {mcpTest[m.id] && (
                    <p className={mcpTest[m.id].error ? 'hint text-destructive' : 'hint'}>
                      {mcpTest[m.id].loading ? '连接中…'
                        : mcpTest[m.id].error ? `连接失败：${mcpTest[m.id].error}`
                          : `已连接，工具：${(mcpTest[m.id].tools ?? []).join(', ') || '（无）'}`}
                    </p>
                  )}
                </div>
                <div className="meta">{m.enabled ? '已启用' : '未启用'}</div>
              </div>
            ))}
            <div className="row-actions">
              <Button variant="secondary" size="sm" onClick={() => patch({
                mcpServers: [...cfg.mcpServers, {
                  id: crypto.randomUUID(), name: '新 MCP Server', transport: 'auto',
                  command: '', args: '', env: '', url: '', headers: '',
                  enabled: true, autoApproveReadOnly: false,
                }],
              })}><Plus className="size-3.5" /> 添加 MCP Server</Button>
            </div>
          </>
        )}

        {tab === 'agents' && (
          <>
            <h3>项目自定义 Agent（配置随项目走）</h3>
            <p className="hint">
              目录：{agentDir || '未设置工作区，请先在左侧「项目」选择目录'}<br />
              每个 Agent 对应一个 .json 文件，Schema：agent_id（自动生成固化，唯一不变）/ name / description / system_prompt / model / tools / skills / mcp_servers / max_turns。<br />
              当前版本把定义注入主 Agent 上下文；按配置分派执行（独立上下文 / skill / MCP 工具集）在后续里程碑接入。保存后对当前会话下一轮生效。
            </p>
            {!agentDir && <p className="hint">设置工作区后即可添加项目级 Agent。</p>}
            {agents.map((a, i) => (
              <div key={i} className="model-row">
                <div style={{ flex: 1 }}>
                  <div className="form-grid">
                    <Label>名称</Label>
                    <Input value={a.name} placeholder="reviewer"
                      onChange={e => updateAgent(i, { name: e.target.value })} />
                    <Label>说明</Label>
                    <Input value={a.description} placeholder="代码审查子 Agent"
                      onChange={e => updateAgent(i, { description: e.target.value })} />
                    <Label>系统提示词</Label>
                    <Textarea rows={4} value={a.system_prompt}
                      placeholder="你是代码审查专家，只读不改。"
                      onChange={e => updateAgent(i, { system_prompt: e.target.value })} />
                    <Label>模型覆盖</Label>
                    <Input value={a.model} placeholder="留空则跟随当前模型"
                      onChange={e => updateAgent(i, { model: e.target.value })} />
                    <Label>工具集</Label>
                    <Input value={a.tools} placeholder="read_file, search_code"
                      onChange={e => updateAgent(i, { tools: e.target.value })} />
                    <Label>Skills</Label>
                    <Input value={a.skills} placeholder="go-review"
                      onChange={e => updateAgent(i, { skills: e.target.value })} />
                    <Label>MCP Servers</Label>
                    <Input value={a.mcp_servers} placeholder="local-fs"
                      onChange={e => updateAgent(i, { mcp_servers: e.target.value })} />
                    <Label>最大轮数</Label>
                    <Input type="number" value={a.max_turns}
                      onChange={e => updateAgent(i, { max_turns: Number(e.target.value) })} />
                  </div>
                  <div className="row-actions">
                    <Button
                      variant="destructive"
                      size="icon-sm"
                      title="删除 Agent"
                      aria-label={`删除 Agent ${a.name || '未命名'}`}
                      onClick={() => removeAgent(i)}
                    ><Trash2 /></Button>
                  </div>
                </div>
                <div className="meta">
                  {a.name || '未命名'}.json
                  {a.agent_id && <div className="agent-id">{a.agent_id}</div>}
                </div>
              </div>
            ))}
            <div className="row-actions">
              <Button variant="secondary" size="sm" disabled={!agentDir} onClick={() => setAgents(a => [...a, {
                agent_id: '', name: '', description: '', system_prompt: '', model: '',
                tools: '', skills: '', mcp_servers: '', max_turns: 0,
              }])}><Plus className="size-3.5" /> 添加 Agent</Button>
            </div>
          </>
        )}

        {tab === 'appearance' && (
          <>
            <h3>外观</h3>
            <p className="hint">
              毛玻璃会立即应用到标题栏、侧栏、对话区、审查栏、弹窗和输入区。
              不透明度下限 65%，用于保证正文与次级文字的可读对比度。
            </p>
            <div className="form-grid">
              <Label>面板不透明度</Label>
              <div className="range-row">
                <input
                  type="range"
                  min={Math.round(MIN_GLASS_ALPHA * 100)}
                  max={Math.round(MAX_GLASS_ALPHA * 100)}
                  step={1}
                  value={Math.round(glassAlpha * 100)}
                  onChange={e => setGlassAlpha(Number(e.target.value) / 100)}
                />
                <span className="range-val">{Math.round(glassAlpha * 100)}%</span>
              </div>
              <Label>背景模糊</Label>
              <div className="range-row">
                <input
                  type="range"
                  min={MIN_GLASS_BLUR}
                  max={MAX_GLASS_BLUR}
                  step={1}
                  value={glassBlur}
                  onChange={e => setGlassBlur(Number(e.target.value))}
                />
                <span className="range-val">{glassBlur}px</span>
              </div>
            </div>
            <div className="row-actions">
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  setGlassAlpha(DEFAULT_GLASS_ALPHA)
                  setGlassBlur(DEFAULT_GLASS_BLUR)
                }}
              >恢复默认</Button>
            </div>
            <div className="glass-preview">
              <strong>毛玻璃预览</strong>
              <span>正文和次级说明文字在毛玻璃背景上的实际效果，拖动上方滑块可实时调整。</span>
            </div>
          </>
        )}

        {tab === 'security' && (
          <>
            <h3>Shell 白名单</h3>
            <p className="hint">
              run_shell 白名单内的命令免审批直接执行；白名单外命令需用户批准。
              go 仅放行 build/test/vet/fmt/version；git 仅放行 status/diff/log/show 与 branch 的列分支形式（如 branch -a），
              branch -D/-m、push/commit/reset 等写操作一律需审批（不受白名单影响）。
              保存后对当前会话下一轮生效。
            </p>
            {cfg.shellWhitelist.length === 0 && <p className="hint">白名单为空：所有 run_shell 命令都需审批。</p>}
            <div className="whitelist-list">
              {cfg.shellWhitelist.map((w, i) => (
                <div key={w} className="whitelist-row">
                  <code className="whitelist-name">{w}</code>
                  <Button
                    variant="destructive"
                    size="icon-sm"
                    title="删除白名单项"
                    aria-label={`删除白名单 ${w}`}
                    onClick={() => patch({ shellWhitelist: cfg.shellWhitelist.filter((_, j) => j !== i) })}
                  ><Trash2 /></Button>
                </div>
              ))}
            </div>
            <div className="row-actions">
              <Input
                className="w-56"
                placeholder="程序名，如 node"
                value={newWhitelistEntry}
                onChange={e => setNewWhitelistEntry(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter') addWhitelistEntry() }}
              />
              <Button variant="secondary" size="sm" onClick={addWhitelistEntry}>添加</Button>
            </div>
          </>
        )}

        {tab === 'usage' && (
          <UsageTab />
        )}

        {error && <p className="hint text-destructive">保存失败：{error}</p>}
            </div>
          </div>
          {tab !== 'usage' && tab !== 'appearance' && (
            <div className="settings-savebar">
              <span className={`settings-status${dirty ? ' dirty' : ''}`}>
                {dirty ? '有未保存修改' : saved ? '已保存' : ''}
              </span>
              <span className="spacer" />
              <Button size="sm" disabled={!dirty} onClick={saveCurrent}>
                <Save className="size-3.5" /> 保存
              </Button>
            </div>
          )}
        </div>
      </Tabs>
    </div>
  )
}

// UsageTab 日维度 Token 用量与成本报表（M5 FR-5.4）。
function UsageTab() {
  const [rows, setRows] = useState<DailyUsageRow[]>([])
  const [err, setErr] = useState('')

  useEffect(() => {
    TaskService.GetDailyUsage(30)
      .then((r: any) => setRows((r as DailyUsageRow[]) ?? []))
      .catch((e: any) => setErr(typeof e === 'string' ? e : e?.message ?? String(e)))
  }, [])

  const totalCalls = rows.reduce((s, r) => s + (r.calls ?? 0), 0)
  const totalIn = rows.reduce((s, r) => s + (r.inputTokens ?? 0), 0)
  const totalOut = rows.reduce((s, r) => s + (r.outputTokens ?? 0), 0)
  const totalCost = rows.reduce((s, r) => s + (r.costTotal ?? 0), 0)

  return (
    <>
      <h3>Token 用量（近 30 天）</h3>
      <p className="hint">
        每次 LLM 调用的输入/输出 tokens 与成本（元/1M tokens 单价折算）按日汇总。
        成本随模型设置中的单价实时折算，历史记录不改写。
      </p>
      {err && <p className="hint text-destructive">{err}</p>}
      {rows.length === 0 ? (
        <p className="hint">暂无用量记录</p>
      ) : (
        <table className="usage-table">
          <thead>
            <tr>
              <th>日期</th><th>调用</th><th>输入 tok</th><th>输出 tok</th><th>成本（元）</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(r => (
              <tr key={r.date}>
                <td>{r.date}</td>
                <td>{r.calls}</td>
                <td>{r.inputTokens.toLocaleString()}</td>
                <td>{r.outputTokens.toLocaleString()}</td>
                <td className="num">{r.costTotal.toFixed(2)}</td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr>
              <td>总计</td>
              <td>{totalCalls}</td>
              <td>{totalIn.toLocaleString()}</td>
              <td>{totalOut.toLocaleString()}</td>
              <td className="num">{totalCost.toFixed(2)}</td>
            </tr>
          </tfoot>
        </table>
      )}
    </>
  )
}

export default SettingsPanel
