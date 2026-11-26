import { useEffect, useState } from 'react'
import { Plus, Save, Trash2, Undo2, Wand2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ConfigService, TaskService } from '../../bindings/ai-dog-agent/internal/service/index.js'
import { ChatService } from '../../bindings/ai-dog-agent/internal/agent/index.js'
import SkillEditor from './SkillEditor'
import MCPEditor from './MCPEditor'
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
  budget: {
    max_steps?: number
    max_tokens?: number
    max_duration_seconds?: number
    max_tool_calls?: number
    max_cost?: number
    max_retries?: number
  }
  context: { compress_threshold?: number; keep_recent?: number }
}

interface AppConfig {
  models: ModelConfig[]
  activeModelId: string
  skills: SkillConfig[]
  mcpServers: MCPServerConfig[]
  shellWhitelist: string[]
  agentSmithAuto?: boolean
}

type Tab = 'models' | 'skills' | 'mcp' | 'agents' | 'project' | 'appearance' | 'security' | 'usage'

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
    budget: s.budget ?? {},
    context: s.context ?? {},
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
    budget: a.budget ?? {},
    context: a.context ?? {},
  }
}

function SettingsPanel({ activeSessionId, onDirtyChange }: {
  activeSessionId?: string
  onDirtyChange?: (dirty: boolean) => void
}) {
  const [cfg, setCfg] = useState<AppConfig>(emptyAppConfig)
  const [agents, setAgents] = useState<AgentSpec[]>([])
  const [dir, setDir] = useState('')
  const [agentDir, setAgentDir] = useState('')
  const [tab, setTab] = useState<Tab>('models')
  const [saved, setSaved] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState('')
  const [smithBusy, setSmithBusy] = useState(false)
  const [smithMsg, setSmithMsg] = useState('')
  const [projectCfg, setProjectCfg] = useState<any>({ main: {}, mcpServers: [], skills: [] })
  const [projectStatus, setProjectStatus] = useState<any>(null)
  const [projectBusy, setProjectBusy] = useState(false)
  const [projectMsg, setProjectMsg] = useState('')
  const [glassAlpha, setGlassAlpha] = useState(() => loadGlass().alpha)
  const [glassBlur, setGlassBlur] = useState(() => loadGlass().blur)

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

  // 项目设置：配置快照 + 信任/审计状态
  const loadProject = () => {
    ConfigService.GetProjectConfig()
      .then((p: any) => setProjectCfg({
        main: p?.main ?? {},
        skills: p?.skills ?? [],
        mcpServers: (p?.mcpServers ?? []).map((m: any) => ({
          ...m,
          transport: m.transport ?? 'auto',
          command: m.command ?? '',
          url: m.url ?? '',
          headers: m.headers ?? '',
          args: (m.args ?? []).join(', '),
          env: m.env ?? '',
          autoApproveReadOnly: m.autoApproveReadOnly ?? false,
        })),
      }))
      .catch(() => setProjectCfg({ main: {}, mcpServers: [], skills: [] }))
    ConfigService.GetProjectStatus()
      .then((s: any) => setProjectStatus(s ?? null))
      .catch(() => setProjectStatus(null))
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
      agentSmithAuto: c.agentSmithAuto ?? false,
    }))
    ConfigService.GetConfigDir().then(setDir)
    ConfigService.GetProjectAgentDir().then(setAgentDir)
    loadAgents()
    loadProject()
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

  // 项目设置：保存项目级配置（主 Agent 覆盖 / 项目 MCP/Skill）
  const saveProject = async () => {
    setProjectBusy(true)
    setProjectMsg('')
    const cleanList = (v: any) => Array.isArray(v)
      ? v.map((s: string) => s.trim()).filter(Boolean)
      : []
    const main = projectCfg.main ?? {}
    const payload = {
      ...projectCfg,
      skills: projectCfg.skills ?? [],
      mcpServers: (projectCfg.mcpServers ?? []).map((m: any) => ({
        ...m,
        args: (m.args ?? '').split(',').map((s: string) => s.trim()).filter(Boolean),
      })),
      main: {
        ...main,
        tools: cleanList(main.tools),
        skills: cleanList(main.skills),
        mcp_servers: cleanList(main.mcp_servers),
      },
    }
    try {
      await ConfigService.SaveProjectConfig(payload as any)
      loadProject()
      setProjectMsg('项目配置已保存；下次构建 Agent 时生效。')
    } catch (e: any) {
      setProjectMsg(typeof e === 'string' ? e : e?.message ?? String(e))
    } finally {
      setProjectBusy(false)
    }
  }

  // 项目信任操作：trust / untrust / unblock
  const updateTrust = async (action: 'trust' | 'untrust' | 'unblock') => {
    setProjectBusy(true)
    setProjectMsg('')
    try {
      if (action === 'trust') await ConfigService.TrustProject()
      else if (action === 'untrust') await ConfigService.UntrustProject()
      else await ConfigService.UnblockProject()
      loadProject()
      setProjectMsg(action === 'trust' ? '已信任项目配置'
        : action === 'untrust' ? '已取消信任'
        : '已解除拉黑，请再次确认信任')
    } catch (e: any) {
      setProjectMsg(typeof e === 'string' ? e : e?.message ?? String(e))
    } finally {
      setProjectBusy(false)
    }
  }

  const saveCurrent = async () => {
    if (tab === 'agents') {
      // Agent 配置与全局开关（agentSmithAuto）在不同文件，保存时一起提交
      await saveAgents()
      await saveConfig()
      return
    }
    if (tab === 'project') {
      await saveProject()
      return
    }
    await saveConfig()
  }

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

  // 手动触发 agent-smith：异步运行，结果通过 agent_smith 事件显示在对话事件流
  const optimizeAgents = async () => {
    if (!activeSessionId) return
    setSmithBusy(true)
    setSmithMsg('')
    try {
      await ChatService.OptimizeAgents(activeSessionId)
      setSmithMsg('agent-smith 已启动；结果会显示在当前对话的事件流中。')
    } catch (e: any) {
      setSmithMsg(typeof e === 'string' ? e : e?.message ?? String(e))
    } finally {
      setSmithBusy(false)
    }
  }

  // 撤销某个 Agent 最近一次 smith 优化（使用 .aidog/agents/.backup 最新备份）
  const restoreAgent = async (name: string) => {
    if (!name) return
    setError('')
    setSmithMsg('')
    try {
      const backups = await ConfigService.ListAgentBackups(name)
      if (!backups || backups.length === 0) {
        setSmithMsg(`「${name}」没有可撤销的备份`)
        return
      }
      await ConfigService.RestoreProjectAgent(name, backups[0].path)
      loadAgents()
      setSmithMsg(`已撤销「${name}」上次优化`)
    } catch (e: any) {
      setError(typeof e === 'string' ? e : e?.message ?? String(e))
    }
  }

  const tabs: { id: Tab; label: string }[] = [
    { id: 'models', label: '模型' },
    { id: 'skills', label: `Skills (${cfg.skills.length})` },
    { id: 'mcp', label: `MCP (${cfg.mcpServers.length})` },
    { id: 'agents', label: `项目 Agent (${agents.length})` },
    { id: 'project', label: '项目设置' },
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
            <SkillEditor skills={cfg.skills} onChange={skills => patch({ skills })} />
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
            <MCPEditor servers={cfg.mcpServers} onChange={mcpServers => patch({ mcpServers })} />
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
            <div className="form-grid" style={{ marginBottom: 12 }}>
              <Label>自动优化 Agent</Label>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Switch
                  checked={!!cfg.agentSmithAuto}
                  onCheckedChange={v => patch({ agentSmithAuto: v })}
                />
                <span className="hint">任务失败/取消后自动运行 agent-smith（写配置仍需审批）</span>
              </div>
            </div>
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
                    <Label>预算：最大步骤</Label>
                    <Input type="number" value={a.budget?.max_steps ?? 0}
                      onChange={e => updateAgent(i, { budget: { ...(a.budget ?? {}), max_steps: Number(e.target.value) } })} />
                    <Label>最大 Token</Label>
                    <Input type="number" value={a.budget?.max_tokens ?? 0}
                      onChange={e => updateAgent(i, { budget: { ...(a.budget ?? {}), max_tokens: Number(e.target.value) } })} />
                    <Label>最大时长(秒)</Label>
                    <Input type="number" value={a.budget?.max_duration_seconds ?? 0}
                      onChange={e => updateAgent(i, { budget: { ...(a.budget ?? {}), max_duration_seconds: Number(e.target.value) } })} />
                    <Label>最大工具调用</Label>
                    <Input type="number" value={a.budget?.max_tool_calls ?? 0}
                      onChange={e => updateAgent(i, { budget: { ...(a.budget ?? {}), max_tool_calls: Number(e.target.value) } })} />
                    <Label>最大成本(元)</Label>
                    <Input type="number" step="0.1" value={a.budget?.max_cost ?? 0}
                      onChange={e => updateAgent(i, { budget: { ...(a.budget ?? {}), max_cost: Number(e.target.value) } })} />
                    <Label>最大重试</Label>
                    <Input type="number" value={a.budget?.max_retries ?? 0}
                      onChange={e => updateAgent(i, { budget: { ...(a.budget ?? {}), max_retries: Number(e.target.value) } })} />
                    <Label>压缩阈值（0-1）</Label>
                    <Input type="number" step="0.05" min="0" max="1"
                      value={a.context?.compress_threshold ?? 0}
                      onChange={e => updateAgent(i, { context: { ...(a.context ?? {}), compress_threshold: Number(e.target.value) } })} />
                    <Label>保留最近轮数</Label>
                    <Input type="number" value={a.context?.keep_recent ?? 0}
                      onChange={e => updateAgent(i, { context: { ...(a.context ?? {}), keep_recent: Number(e.target.value) } })} />
                  </div>
                  <div className="row-actions">
                    <Button
                      variant="outline"
                      size="icon-sm"
                      title="撤销上次优化"
                      aria-label={`撤销 ${a.name || '未命名'} 上次优化`}
                      disabled={!a.name}
                      onClick={() => restoreAgent(a.name)}
                    ><Undo2 /></Button>
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
                budget: {}, context: {},
              }])}><Plus className="size-3.5" /> 添加 Agent</Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={!agentDir || !activeSessionId || smithBusy}
                title={activeSessionId ? '运行 agent-smith 优化项目 Agent' : '先在左侧选择一个线程'}
                onClick={optimizeAgents}
              ><Wand2 className="size-3.5" /> {smithBusy ? '优化中…' : '优化 Agent'}</Button>
              {smithMsg && <span className="hint">{smithMsg}</span>}
            </div>
          </>
        )}

        {tab === 'project' && (
          <>
            <h3>项目设置</h3>
            <p className="hint">
              项目级配置来自 {agentDir || '.aidog'}；加载前需确认信任，高危配置会被自动拉黑。
              保存后下次构建 Agent 时生效。
            </p>
            {projectStatus ? (
              <div className="model-row">
                <div style={{ flex: 1 }}>
                  <div className="form-grid">
                    <Label>信任状态</Label>
                    <div>
                      <Badge variant="outline">{projectStatus.status}</Badge>
                      {projectStatus.audit?.blocked && (
                        <span className="hint text-destructive"> 存在高危配置</span>
                      )}
                    </div>
                    <Label>审计统计</Label>
                    <div className="hint">
                      高危 {projectStatus.audit?.highCount ?? 0} · 中危 {projectStatus.audit?.mediumCount ?? 0} · 低危 {projectStatus.audit?.lowCount ?? 0}
                    </div>
                  </div>
                  <div className="row-actions">
                    {projectStatus.status === 'untrusted' && (
                      <Button variant="secondary" size="sm" disabled={projectBusy}
                        onClick={() => updateTrust('trust')}>信任项目配置</Button>
                    )}
                    {projectStatus.status === 'trusted' && (
                      <Button variant="outline" size="sm" disabled={projectBusy}
                        onClick={() => updateTrust('untrust')}>取消信任</Button>
                    )}
                    {projectStatus.status === 'blocked' && (
                      <Button variant="destructive" size="sm" disabled={projectBusy}
                        onClick={() => updateTrust('unblock')}>解除拉黑（需再次确认信任）</Button>
                    )}
                  </div>
                </div>
                <div className="meta">{projectStatus.root}</div>
              </div>
            ) : (
              <p className="hint">设置工作区后可查看项目配置状态。</p>
            )}
            {projectStatus?.audit?.findings?.length > 0 && (
              <div className="model-row">
                <div style={{ flex: 1 }}>
                  <h4>审计发现</h4>
                  <ul className="hint">
                    {projectStatus.audit.findings.map((f: any, i: number) => (
                      <li key={i}>[{f.severity}] {f.rule} · {f.file} · {f.evidence}</li>
                    ))}
                  </ul>
                </div>
              </div>
            )}
            <h4>项目 Agent（work agent）</h4>
            {agents.length === 0 ? (
              <p className="hint">暂无项目 Agent，可在「项目 Agent」tab 添加。</p>
            ) : (
              <div className="model-row">
                <div style={{ flex: 1 }}>
                  <ul className="hint">
                    {agents.map(a => (
                      <li key={a.agent_id || a.name}>
                        {a.name || '未命名'} · 模型 {a.model || '跟随主模型'} · 工具 {a.tools || '只读'} · 预算 {a.budget?.max_steps || '默认'} 步
                      </li>
                    ))}
                  </ul>
                  <Button variant="outline" size="sm" onClick={() => setTab('agents')}>
                    编辑完整字段
                  </Button>
                </div>
              </div>
            )}
            <h4>项目专属 Skills</h4>
            <SkillEditor
              skills={projectCfg.skills ?? []}
              onChange={skills => setProjectCfg((c: any) => ({ ...c, skills }))}
            />
            <h4>项目专属 MCP Server</h4>
            <MCPEditor
              servers={projectCfg.mcpServers ?? []}
              onChange={mcpServers => setProjectCfg((c: any) => ({ ...c, mcpServers }))}
            />
            <h4>主 Agent 覆盖（.aidog/main.json）</h4>
            {(() => {
              const main = projectCfg.main ?? {}
              const listText = (v: any) => Array.isArray(v) ? v.join(', ') : ''
              const parseList = (v: string) => v.split(',').map(s => s.trim())
              const patchMain = (p: any) => setProjectCfg((c: any) => ({
                ...c, main: { ...(c.main ?? {}), ...p },
              }))
              return (
                <>
                  <div className="form-grid">
                    <Label>模型覆盖</Label>
                    <Input value={main.model ?? ''} placeholder="留空跟随全局激活模型"
                      onChange={e => patchMain({ model: e.target.value })} />
                    <Label>工具</Label>
                    <Input value={listText(main.tools)} placeholder="read_file, dispatch_coder"
                      onChange={e => patchMain({ tools: parseList(e.target.value) })} />
                    <Label>Skills</Label>
                    <Input value={listText(main.skills)} placeholder="全局或项目 Skill 名称"
                      onChange={e => patchMain({ skills: parseList(e.target.value) })} />
                    <Label>MCP Servers</Label>
                    <Input value={listText(main.mcp_servers)} placeholder="全局或项目 MCP 名称"
                      onChange={e => patchMain({ mcp_servers: parseList(e.target.value) })} />
                  </div>
                  <h4>执行预算（0 = 默认值）</h4>
                  <div className="form-grid">
                    <Label>最大步骤</Label>
                    <Input type="number" value={main.budget?.max_steps ?? 0}
                      onChange={e => patchMain({ budget: { ...(main.budget ?? {}), max_steps: Number(e.target.value) } })} />
                    <Label>最大 Token</Label>
                    <Input type="number" value={main.budget?.max_tokens ?? 0}
                      onChange={e => patchMain({ budget: { ...(main.budget ?? {}), max_tokens: Number(e.target.value) } })} />
                    <Label>最大时长(秒)</Label>
                    <Input type="number" value={main.budget?.max_duration_seconds ?? 0}
                      onChange={e => patchMain({ budget: { ...(main.budget ?? {}), max_duration_seconds: Number(e.target.value) } })} />
                    <Label>最大工具调用</Label>
                    <Input type="number" value={main.budget?.max_tool_calls ?? 0}
                      onChange={e => patchMain({ budget: { ...(main.budget ?? {}), max_tool_calls: Number(e.target.value) } })} />
                    <Label>最大成本(元)</Label>
                    <Input type="number" step="0.1" value={main.budget?.max_cost ?? 0}
                      onChange={e => patchMain({ budget: { ...(main.budget ?? {}), max_cost: Number(e.target.value) } })} />
                    <Label>最大重试</Label>
                    <Input type="number" value={main.budget?.max_retries ?? 0}
                      onChange={e => patchMain({ budget: { ...(main.budget ?? {}), max_retries: Number(e.target.value) } })} />
                  </div>
                  <h4>上下文压缩（阈值 0-1，0 = 默认 0.7）</h4>
                  <div className="form-grid">
                    <Label>触发阈值</Label>
                    <Input type="number" step="0.05" min="0" max="1"
                      value={main.context?.compress_threshold ?? 0}
                      onChange={e => patchMain({ context: { ...(main.context ?? {}), compress_threshold: Number(e.target.value) } })} />
                    <Label>保留最近轮数</Label>
                    <Input type="number" value={main.context?.keep_recent ?? 0}
                      onChange={e => patchMain({ context: { ...(main.context ?? {}), keep_recent: Number(e.target.value) } })} />
                  </div>
                </>
              )
            })()}
            <div className="row-actions">
              <Button variant="secondary" size="sm" disabled={projectBusy} onClick={saveProject}>
                保存项目配置
              </Button>
              {projectMsg && <span className="hint">{projectMsg}</span>}
            </div>
            <p className="hint">主 Agent 覆盖与项目专属 MCP/Skill 编辑在本页后续接入。</p>
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
              其他语言（如 node / python / cargo / mvn）把对应构建、测试命令加入白名单即可免审批，未加入则逐次审批。
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
