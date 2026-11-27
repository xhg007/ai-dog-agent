import { useEffect, useMemo, useState } from 'react'
import { Plus, Save, ScanSearch, Sparkles, Trash2, Undo2, Wand2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { ConfigService, TaskService } from '../../bindings/ai-dog-agent/internal/service/index.js'
import { ChatService } from '../../bindings/ai-dog-agent/internal/agent/index.js'
import SkillEditor, { newSkill } from './SkillEditor'
import MCPEditor, { newMCPServer } from './MCPEditor'
import MultiSelect, { MultiSelectOption } from './MultiSelect'
import { PROVIDER_PRESETS, ProviderPreset } from '../lib/modelPresets'
import {
  applyGlass, loadGlass, saveGlass,
  DEFAULT_GLASS_ALPHA, DEFAULT_GLASS_BLUR,
  MIN_GLASS_ALPHA, MAX_GLASS_ALPHA, MIN_GLASS_BLUR, MAX_GLASS_BLUR,
} from '../lib/glass'

interface ProviderConfig {
  id: string
  name: string
  baseURL: string
  apiKey: string
}

interface ModelConfig {
  id: string
  providerId?: string
  name: string
  baseURL?: string   // 运行时由所属供应商填充（不在前端编辑）
  apiKey?: string    // 同上
  model: string
  priceIn?: number
  priceOut?: number
  timeoutSeconds?: number
  disableExtraFields?: boolean
  contextWindow?: number
}

interface SkillConfig {
  id: string
  name: string
  description: string
  prompt: string
  enabled: boolean
  // source 为目录扫描来源（user-dir / project-dir）：只读展示，保存时过滤。
  source?: string
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
  providers: ProviderConfig[]
  models: ModelConfig[]
  activeModelId: string
  skills: SkillConfig[]
  mcpServers: MCPServerConfig[]
  shellWhitelist: string[]
  agentSmithAuto?: boolean
  // 工具目录：由后端 GetConfig 填充，供 Agent 配置页多选（不落盘）。
  toolCatalog?: ToolOption[]
}

interface ToolOption {
  name: string
  description?: string
  group?: string
}

type Tab = 'models' | 'skills' | 'mcp' | 'project' | 'appearance' | 'security' | 'usage'

interface DailyUsageRow { date: string; calls: number; inputTokens: number; outputTokens: number; costTotal: number }

const emptyAppConfig: AppConfig = {
  providers: [], models: [], activeModelId: '', skills: [], mcpServers: [], shellWhitelist: [],
  toolCatalog: [],
}

// splitList 把 Agent 配置里的逗号分隔字符串拆成数组（兼容空串与多余空格）。
const splitList = (v: string) => (v ?? '').split(',').map(s => s.trim()).filter(Boolean)

// modelLabel 模型下拉的展示名：name 与 model 相同时只显示一次。
const modelLabel = (m: ModelConfig) =>
  !m.name || m.name === m.model ? m.model : `${m.name} · ${m.model}`

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
  // 供应商模型检测对话框：检测结果 + 勾选项
  const [detect, setDetect] = useState<{
    providerId: string
    providerName: string
    loading: boolean
    error: string
    models: string[]
    selected: Set<string>
  } | null>(null)

  // 预设供应商对话框：选择供应商与候选模型，一键创建 Provider + Model。
  const [presetOpen, setPresetOpen] = useState(false)
  const [presetId, setPresetId] = useState(PROVIDER_PRESETS[0].id)
  const [presetModels, setPresetModels] = useState<Set<string>>(() => new Set())

  // Agent 配置页的可选项：工具目录来自 GetConfig，Skill/MCP 合并全局 + 项目。
  const toolOptions = useMemo<MultiSelectOption[]>(() => {
    const groups: Record<string, string> = {
      readonly: '只读', write: '写入（需审批）', exec: '执行', devtools: 'devtools',
    }
    return (cfg.toolCatalog ?? []).map(t => ({
      value: t.name,
      label: t.name,
      hint: t.description,
      group: groups[t.group ?? ''] ?? t.group ?? '其他',
    }))
  }, [cfg.toolCatalog])

  const skillOptions = useMemo<MultiSelectOption[]>(() => {
    const map = new Map<string, MultiSelectOption>()
    for (const s of [...(cfg.skills ?? []), ...(projectCfg.skills ?? [])]) {
      const v = s.name || s.id
      if (!v) continue
      const hint = [s.description, s.source ? '目录技能（只读）' : ''].filter(Boolean).join(' · ')
      map.set(v, { value: v, label: s.name || s.id, hint })
    }
    return [...map.values()]
  }, [cfg.skills, projectCfg.skills])

  const mcpOptions = useMemo<MultiSelectOption[]>(() => {
    const map = new Map<string, MultiSelectOption>()
    for (const m of [...(cfg.mcpServers ?? []), ...(projectCfg.mcpServers ?? [])]) {
      const v = m.name || m.id
      if (!v) continue
      const kind = m.transport || (m.command ? 'stdio' : 'http')
      map.set(v, {
        value: v,
        label: m.name || m.id,
        hint: `${kind}${m.enabled === false ? ' · 未启用' : ''}`,
      })
    }
    return [...map.values()]
  }, [cfg.mcpServers, projectCfg.mcpServers])

  // modelRef 把旧配置里的 Name/Model 规范化为模型 ID；无法匹配时保留原值。
  const modelRef = (ref: string) => {
    if (!ref) return '__default__'
    const hit = (cfg.models ?? []).find(m => m.id === ref || m.name === ref || m.model === ref)
    return hit ? hit.id : ref
  }

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
      providers: c.providers ?? [],
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
      toolCatalog: c.toolCatalog ?? [],
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

  const updateModel = (id: string, p: Partial<ModelConfig>) =>
    patch({ models: cfg.models.map(m => m.id === id ? { ...m, ...p } : m) })

  const addProvider = () => patch({
    providers: [...cfg.providers, { id: crypto.randomUUID(), name: '新供应商', baseURL: '', apiKey: '' }],
  })

  // ---------- 预设供应商 ----------
  const preset: ProviderPreset = PROVIDER_PRESETS.find(p => p.id === presetId) ?? PROVIDER_PRESETS[0]

  // pickPresetModels 默认勾选推荐模型；没有推荐项时全选。
  const pickPresetModels = (p: ProviderPreset) => {
    const recommended = p.models.filter(m => m.recommended)
    return new Set((recommended.length > 0 ? recommended : p.models).map(m => m.id))
  }

  const openPreset = () => {
    const p = PROVIDER_PRESETS[0]
    setPresetId(p.id)
    setPresetModels(pickPresetModels(p))
    setPresetOpen(true)
  }

  const changePreset = (id: string) => {
    const p = PROVIDER_PRESETS.find(x => x.id === id) ?? PROVIDER_PRESETS[0]
    setPresetId(p.id)
    setPresetModels(pickPresetModels(p))
  }

  const togglePresetModel = (id: string) => {
    setPresetModels(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const addPreset = () => {
    const p = PROVIDER_PRESETS.find(x => x.id === presetId)
    if (!p) return
    const providerId = crypto.randomUUID()
    const provider: ProviderConfig = {
      id: providerId, name: p.name, baseURL: p.baseURL, apiKey: '',
    }
    const models: ModelConfig[] = p.models
      .filter(m => presetModels.has(m.id))
      .map(m => ({
        id: crypto.randomUUID(),
        providerId,
        name: m.name,
        model: m.id,
        priceIn: 0,
        priceOut: 0,
        contextWindow: m.contextWindow ?? 0,
      }))
    patch({
      providers: [...cfg.providers, provider],
      models: [...cfg.models, ...models],
    })
    setPresetOpen(false)
  }

  const updateProvider = (id: string, p: Partial<ProviderConfig>) =>
    patch({ providers: cfg.providers.map(x => x.id === id ? { ...x, ...p } : x) })

  const removeProvider = (id: string) => {
    const models = cfg.models.filter(m => m.providerId !== id)
    patch({
      providers: cfg.providers.filter(p => p.id !== id),
      models,
      activeModelId: models.some(m => m.id === cfg.activeModelId) ? cfg.activeModelId : '',
    })
  }

  const addModelToProvider = (providerId: string) => patch({
    models: [...cfg.models, {
      id: crypto.randomUUID(), providerId, name: '新模型', model: '', priceIn: 0, priceOut: 0,
    }],
  })

  const removeModel = (id: string) => patch({
    models: cfg.models.filter(m => m.id !== id),
    activeModelId: cfg.activeModelId === id ? '' : cfg.activeModelId,
  })

  const updateAgent = (i: number, p: Partial<AgentSpec>) => {
    setAgents(a => a.map((x, j) => j === i ? { ...x, ...p } : x))
    setSaved(false)
    setDirty(true)
  }

  const addAgent = () => setAgents(a => [...a, {
    agent_id: '', name: '', description: '', system_prompt: '', model: '',
    tools: '', skills: '', mcp_servers: '', max_turns: 0,
    budget: {}, context: {},
  }])

  const addProjectSkill = () => setProjectCfg((c: any) => ({
    ...c, skills: [...(c.skills ?? []), newSkill()],
  }))

  const addProjectMCPServer = () => setProjectCfg((c: any) => ({
    ...c, mcpServers: [...(c.mcpServers ?? []), newMCPServer()],
  }))

  const addGlobalSkill = () => patch({ skills: [...cfg.skills, newSkill()] })

  const addGlobalMCPServer = () => patch({ mcpServers: [...cfg.mcpServers, newMCPServer()] })

  const [newWhitelistEntry, setNewWhitelistEntry] = useState('')

  const addWhitelistEntry = () => {
    const v = newWhitelistEntry.trim()
    if (!v || cfg.shellWhitelist.includes(v)) return
    patch({ shellWhitelist: [...cfg.shellWhitelist, v] })
    setNewWhitelistEntry('')
  }

  const isDirSkill = (s: SkillConfig) => s.source === 'user-dir' || s.source === 'project-dir'

  const serialize = (c: AppConfig) => ({
    ...c,
    // 目录来源 Skill 只读：不回写 config.yml（后端 Save 也会过滤，这里提前剔除）。
    skills: (c.skills ?? []).filter(s => !isDirSkill(s)),
    mcpServers: c.mcpServers.map(m => ({
      ...m,
      args: m.args.split(',').map(s => s.trim()).filter(Boolean),
    })),
  })

  const saveConfig = async (): Promise<boolean> => {
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
      return true
    } catch (e) {
      setError(String(e))
      return false
    }
  }

  // 检测供应商可用模型：检测接口从已保存配置读取 BaseURL/APIKey，
  // 因此先落盘当前编辑，再列出尚未添加的模型供勾选。
  const openDetect = async (p: ProviderConfig) => {
    setError('')
    if (!(await saveConfig())) return
    const name = p.name || p.baseURL
    setDetect({ providerId: p.id, providerName: name, loading: true, error: '', models: [], selected: new Set() })
    try {
      const list = (await ConfigService.DetectProviderModels(p.id)) as string[] | null
      const existing = new Set(cfg.models.filter(m => m.providerId === p.id).map(m => m.model))
      const fresh = (list ?? []).filter(id => !existing.has(id))
      setDetect({
        providerId: p.id,
        providerName: name,
        loading: false,
        error: fresh.length === 0 ? '该供应商的模型已全部添加。' : '',
        models: fresh,
        selected: new Set(fresh),
      })
    } catch (e: any) {
      setDetect(prev => prev ? {
        ...prev,
        loading: false,
        error: typeof e === 'string' ? e : e?.message ?? String(e),
      } : null)
    }
  }

  const toggleDetectModel = (id: string) => setDetect(prev => {
    if (!prev) return prev
    const selected = new Set(prev.selected)
    if (selected.has(id)) selected.delete(id)
    else selected.add(id)
    return { ...prev, selected }
  })

  const confirmDetect = () => {
    if (!detect || detect.selected.size === 0) {
      setDetect(null)
      return
    }
    const added: ModelConfig[] = [...detect.selected].map(id => ({
      id: crypto.randomUUID(), providerId: detect.providerId, name: id, model: id,
      priceIn: 0, priceOut: 0,
    }))
    patch({ models: [...cfg.models, ...added] })
    setDetect(null)
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
      // 目录来源 Skill 只读：不回写 skills.yml（后端 SaveProject 也会过滤）。
      skills: (projectCfg.skills ?? []).filter(
        (s: any) => s.source !== 'user-dir' && s.source !== 'project-dir'
      ),
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
    if (tab === 'project') {
      // 项目设置页同时承载项目级配置、work agent 定义与全局开关（agentSmithAuto），
      // 三者落在不同文件，保存时一起提交
      await saveProject()
      await saveAgents()
      await saveConfig()
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
    { id: 'project', label: `项目设置 (Agent ${agents.length})` },
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
            <div className="settings-head">
              <h3>模型配置（OpenAI 兼容）</h3>
              <div className="settings-section-actions">
                <Button variant="secondary" size="xs" onClick={openPreset}>
                  <Sparkles className="size-3.5" /> 添加预设
                </Button>
                <Button variant="secondary" size="xs" onClick={addProvider}>
                  <Plus className="size-3.5" /> 添加供应商
                </Button>
              </div>
            </div>
            <p className="hint">
              一个供应商只需填写一次 Base URL / API Key，其下可挂多个模型；
              「检测模型」会请求供应商的 /models 接口，勾选后批量添加。<br />
              「添加预设」已按官方文档预填常用供应商的 Base URL 与最新候选模型（2026-11-27 核对），添加后填写 API Key 即可。
            </p>
            {cfg.providers.length === 0 && (
              <p className="hint">尚未配置供应商，点击右上角「添加供应商」。</p>
            )}
            {cfg.providers.map(p => {
              const models = cfg.models.filter(m => m.providerId === p.id)
              return (
                <section key={p.id} className="settings-section">
                  <div className="settings-section-head">
                    <h4>{p.name || '未命名供应商'}</h4>
                    <div className="settings-section-actions">
                      <Button variant="secondary" size="xs" onClick={() => openDetect(p)}>
                        <ScanSearch className="size-3.5" /> 检测模型
                      </Button>
                      <Button variant="secondary" size="xs" onClick={() => addModelToProvider(p.id)}>
                        <Plus className="size-3.5" /> 添加模型
                      </Button>
                      <Button
                        variant="destructive"
                        size="icon-xs"
                        title="删除供应商（连同其下模型）"
                        aria-label={`删除供应商 ${p.name || p.id}`}
                        onClick={() => removeProvider(p.id)}
                      ><Trash2 /></Button>
                    </div>
                  </div>
                  <div className="form-grid">
                    <Label>名称</Label>
                    <Input value={p.name} placeholder="DeepSeek"
                      onChange={e => updateProvider(p.id, { name: e.target.value })} />
                    <Label>Base URL</Label>
                    <Input value={p.baseURL} placeholder="https://api.deepseek.com/v1"
                      onChange={e => updateProvider(p.id, { baseURL: e.target.value })} />
                    <Label>API Key</Label>
                    <Input type="password" value={p.apiKey} placeholder="sk-..."
                      onChange={e => updateProvider(p.id, { apiKey: e.target.value })} />
                  </div>
                  {models.length === 0 && (
                    <p className="hint">该供应商下暂无模型，点击右上角「添加模型」或「检测模型」。</p>
                  )}
                  {models.map(m => (
                    <div
                      key={m.id}
                      className={`model-row${cfg.activeModelId === m.id ? ' active' : ''}`}
                      onClick={() => activateModel(m.id)}
                    >
                      <div style={{ flex: 1 }}>
                        <div className="form-grid">
                          <Label>显示名</Label>
                          <Input value={m.name} onClick={e => e.stopPropagation()}
                            onChange={e => updateModel(m.id, { name: e.target.value })} />
                          <Label>Model ID</Label>
                          <Input value={m.model} placeholder="deepseek-chat"
                            onClick={e => e.stopPropagation()}
                            onChange={e => updateModel(m.id, { model: e.target.value })} />
                          <Label>输入单价/1M</Label>
                          <Input type="number" value={m.priceIn}
                            onClick={e => e.stopPropagation()}
                            onChange={e => updateModel(m.id, { priceIn: Number(e.target.value) })} />
                          <Label>输出单价/1M</Label>
                          <Input type="number" value={m.priceOut}
                            onClick={e => e.stopPropagation()}
                            onChange={e => updateModel(m.id, { priceOut: Number(e.target.value) })} />
                          <Label>超时（秒）</Label>
                          <Input type="number" value={m.timeoutSeconds ?? 0}
                            placeholder="0 = 默认 300"
                            onClick={e => e.stopPropagation()}
                            onChange={e => updateModel(m.id, { timeoutSeconds: Number(e.target.value) })} />
                          <Label>发送 agent_id</Label>
                          <Switch checked={!m.disableExtraFields}
                            onCheckedChange={v => updateModel(m.id, { disableExtraFields: !v })} />
                        </div>
                        <div className="row-actions">
                          <Button
                            variant="destructive"
                            size="icon-sm"
                            title="删除模型"
                            aria-label={`删除模型 ${m.name || m.model || m.id}`}
                            onClick={e => { e.stopPropagation(); removeModel(m.id) }}
                          ><Trash2 /></Button>
                        </div>
                      </div>
                      <div className="meta">{cfg.activeModelId === m.id ? '当前使用' : '点击设为当前'}</div>
                    </div>
                  ))}
                </section>
              )
            })}
            {cfg.models.some(m => !m.providerId) && (
              <section className="settings-section">
                <div className="settings-section-head">
                  <h4>未归属供应商的模型</h4>
                </div>
                <p className="hint">旧式独立模型（自带 Base URL / API Key），建议归入某个供应商。</p>
                {cfg.models.filter(m => !m.providerId).map(m => (
                  <div
                    key={m.id}
                    className={`model-row${cfg.activeModelId === m.id ? ' active' : ''}`}
                    onClick={() => activateModel(m.id)}
                  >
                    <div style={{ flex: 1 }}>
                      <div className="form-grid">
                        <Label>显示名</Label>
                        <Input value={m.name} onClick={e => e.stopPropagation()}
                          onChange={e => updateModel(m.id, { name: e.target.value })} />
                        <Label>Base URL</Label>
                        <Input value={m.baseURL ?? ''} placeholder="https://api.deepseek.com/v1"
                          onClick={e => e.stopPropagation()}
                          onChange={e => updateModel(m.id, { baseURL: e.target.value })} />
                        <Label>API Key</Label>
                        <Input type="password" value={m.apiKey ?? ''}
                          onClick={e => e.stopPropagation()}
                          onChange={e => updateModel(m.id, { apiKey: e.target.value })} />
                        <Label>Model ID</Label>
                        <Input value={m.model} placeholder="deepseek-chat"
                          onClick={e => e.stopPropagation()}
                          onChange={e => updateModel(m.id, { model: e.target.value })} />
                        <Label>输入单价/1M</Label>
                        <Input type="number" value={m.priceIn}
                          onClick={e => e.stopPropagation()}
                          onChange={e => updateModel(m.id, { priceIn: Number(e.target.value) })} />
                        <Label>输出单价/1M</Label>
                        <Input type="number" value={m.priceOut}
                          onClick={e => e.stopPropagation()}
                          onChange={e => updateModel(m.id, { priceOut: Number(e.target.value) })} />
                        <Label>超时（秒）</Label>
                        <Input type="number" value={m.timeoutSeconds ?? 0}
                          placeholder="0 = 默认 300"
                          onClick={e => e.stopPropagation()}
                          onChange={e => updateModel(m.id, { timeoutSeconds: Number(e.target.value) })} />
                        <Label>发送 agent_id</Label>
                        <Switch checked={!m.disableExtraFields}
                          onCheckedChange={v => updateModel(m.id, { disableExtraFields: !v })} />
                      </div>
                      <div className="row-actions">
                        <Button
                          variant="destructive"
                          size="icon-sm"
                          title="删除模型"
                          aria-label={`删除模型 ${m.name || m.model || m.id}`}
                          onClick={e => { e.stopPropagation(); removeModel(m.id) }}
                        ><Trash2 /></Button>
                      </div>
                    </div>
                    <div className="meta">{cfg.activeModelId === m.id ? '当前使用' : '点击设为当前'}</div>
                  </div>
                ))}
              </section>
            )}
            <p className="hint">
              配置文件：{dir || '…'}<br />
              点击模型行会立即设为当前模型；其它字段修改需点右下角「保存」。
              保存后对当前会话下一轮生效；超时 0 表示默认 300s。
            </p>
          </>
        )}

        {tab === 'skills' && (
          <>
            <div className="settings-head">
              <h3>Skills（可复用指令包）</h3>
              <Button variant="secondary" size="xs" onClick={addGlobalSkill}>
                <Plus className="size-3.5" /> 添加 Skill
              </Button>
            </div>
            <p className="hint">启用的 Skill 会追加到主 Agent 的动态上下文尾部（不破坏提示词缓存前缀）；保存后对当前会话下一轮生效。</p>
            <SkillEditor skills={cfg.skills} onChange={skills => patch({ skills })} showAddButton={false} />
          </>
        )}

        {tab === 'mcp' && (
          <>
            <div className="settings-head">
              <h3>MCP Server（外部工具）</h3>
              <Button variant="secondary" size="xs" onClick={addGlobalMCPServer}>
                <Plus className="size-3.5" /> 添加 MCP Server
              </Button>
            </div>
            <p className="hint">
              支持 stdio 与 HTTP/SSE 远程连接。已启用 Server 在新建会话或配置变更后连接，工具以 mcp_&lt;server&gt;_&lt;tool&gt; 暴露给主 Agent；
              默认每次调用需审批（可「批准并记住」）。「只读工具免审批」仅对服务器声明 readOnlyHint=true 的工具跳过审批，默认关闭——
              该注解来自服务器，不可完全信任。
            </p>
            <MCPEditor servers={cfg.mcpServers} onChange={mcpServers => patch({ mcpServers })} showAddButton={false} />
          </>
        )}

        {tab === 'project' && (
          <>
            <h3>项目设置</h3>
            <p className="hint">
              项目级配置来自 {agentDir || '.aidog'}；加载前需确认信任，高危配置会被自动拉黑。
              保存后下次构建 Agent 时生效。
            </p>
            <section className="settings-section">
              <div className="settings-section-head">
                <h4>项目配置</h4>
                <div className="settings-section-actions">
                  {projectStatus?.status === 'untrusted' && (
                    <Button variant="secondary" size="xs" disabled={projectBusy}
                      onClick={() => updateTrust('trust')}>信任项目配置</Button>
                  )}
                  {projectStatus?.status === 'trusted' && (
                    <Button variant="outline" size="xs" disabled={projectBusy}
                      onClick={() => updateTrust('untrust')}>取消信任</Button>
                  )}
                  {projectStatus?.status === 'blocked' && (
                    <Button variant="destructive" size="xs" disabled={projectBusy}
                      onClick={() => updateTrust('unblock')}>解除拉黑</Button>
                  )}
                </div>
              </div>
              {projectStatus ? (
                <div className="form-grid">
                  <Label>信任状态</Label>
                  <div className="form-value">
                    <Badge variant="outline">{projectStatus.status}</Badge>
                    {projectStatus.audit?.blocked && (
                      <span className="hint text-destructive">存在高危配置</span>
                    )}
                  </div>
                  <Label>审计统计</Label>
                  <div className="form-value hint">
                    高危 {projectStatus.audit?.highCount ?? 0} · 中危 {projectStatus.audit?.mediumCount ?? 0} · 低危 {projectStatus.audit?.lowCount ?? 0}
                  </div>
                  <Label>项目根目录</Label>
                  <div className="form-value hint">{projectStatus.root}</div>
                </div>
              ) : (
                <p className="hint">设置工作区后可查看项目配置状态。</p>
              )}
              {projectStatus?.audit?.findings?.length > 0 && (
                <>
                  <div className="settings-subhead">审计发现</div>
                  <ul className="hint">
                    {projectStatus.audit.findings.map((f: any, i: number) => (
                      <li key={i}>[{f.severity}] {f.rule} · {f.file} · {f.evidence}</li>
                    ))}
                  </ul>
                </>
              )}
            </section>

            <section className="settings-section">
              <div className="settings-section-head">
                <h4>项目 Agent（work agent）</h4>
                <div className="settings-section-actions">
                  <Button variant="secondary" size="xs" disabled={!agentDir} onClick={addAgent}>
                    <Plus className="size-3.5" /> 添加 Agent
                  </Button>
                  <Button
                    variant="secondary"
                    size="xs"
                    disabled={!agentDir || !activeSessionId || smithBusy}
                    title={activeSessionId ? '运行 agent-smith 优化项目 Agent' : '先在左侧选择一个线程'}
                    onClick={optimizeAgents}
                  ><Wand2 className="size-3.5" /> {smithBusy ? '优化中…' : '优化 Agent'}</Button>
                </div>
              </div>
              {smithMsg && <p className="hint">{smithMsg}</p>}
              <p className="hint">
                目录：{agentDir || '未设置工作区，请先在左侧「项目」选择目录'}<br />
                每个 Agent 对应 .aidog/agents 下的一个 .yml 文件：agent_id（自动生成固化，唯一不变）/ name / description / system_prompt / model / tools / skills / mcp_servers / max_turns / budget / context。<br />
                work agent 按此定义分派执行（独立上下文 / Skill / MCP 工具集）；保存后对当前会话下一轮生效。<br />
                工具 / Skill / MCP 可从已配置列表勾选，也可手动输入（回车添加）任意值。
              </p>
              {!agentDir && <p className="hint">设置工作区后即可添加项目级 Agent。</p>}
              <div className="form-grid" style={{ margin: '12px 0' }}>
                <Label>自动优化 Agent</Label>
                <div className="form-value">
                  <Switch
                    checked={!!cfg.agentSmithAuto}
                    onCheckedChange={v => patch({ agentSmithAuto: v })}
                  />
                  <span className="hint">任务失败/取消后自动运行 agent-smith（写配置仍需审批）</span>
                </div>
              </div>
              {agents.length === 0 && <p className="hint">暂无项目 Agent，点击右上角「添加 Agent」创建。</p>}
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
                    <Select
                      value={modelRef(a.model)}
                      onValueChange={v => updateAgent(i, { model: v === '__default__' ? '' : v })}
                    >
                      <SelectTrigger size="sm" className="w-full">
                        <SelectValue placeholder="跟随当前模型" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__default__">跟随当前模型</SelectItem>
                        {(cfg.models ?? []).map(m => (
                          <SelectItem key={m.id} value={m.id}>{modelLabel(m)}</SelectItem>
                        ))}
                        {a.model && modelRef(a.model) === a.model && (
                          <SelectItem value={a.model}>{a.model}（当前配置）</SelectItem>
                        )}
                      </SelectContent>
                    </Select>
                    <Label>工具集</Label>
                    <MultiSelect
                      value={splitList(a.tools)}
                      options={toolOptions}
                      onChange={v => updateAgent(i, { tools: v.join(', ') })}
                    />
                    <Label>Skills</Label>
                    <MultiSelect
                      value={splitList(a.skills)}
                      options={skillOptions}
                      onChange={v => updateAgent(i, { skills: v.join(', ') })}
                    />
                    <Label>MCP Servers</Label>
                    <MultiSelect
                      value={splitList(a.mcp_servers)}
                      options={mcpOptions}
                      onChange={v => updateAgent(i, { mcp_servers: v.join(', ') })}
                    />
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
                  {a.name || '未命名'}.yml
                  {a.agent_id && <div className="agent-id">{a.agent_id}</div>}
                </div>
              </div>
            ))}
            </section>

            <section className="settings-section">
              <div className="settings-section-head">
                <h4>项目专属 Skills</h4>
                <div className="settings-section-actions">
                  <Button variant="secondary" size="xs" onClick={addProjectSkill}>
                    <Plus className="size-3.5" /> 添加 Skill
                  </Button>
                </div>
              </div>
              <SkillEditor
                skills={projectCfg.skills ?? []}
                onChange={skills => setProjectCfg((c: any) => ({ ...c, skills }))}
                showAddButton={false}
              />
            </section>

            <section className="settings-section">
              <div className="settings-section-head">
                <h4>项目专属 MCP Server</h4>
                <div className="settings-section-actions">
                  <Button variant="secondary" size="xs" onClick={addProjectMCPServer}>
                    <Plus className="size-3.5" /> 添加 MCP Server
                  </Button>
                </div>
              </div>
              <MCPEditor
                servers={projectCfg.mcpServers ?? []}
                onChange={mcpServers => setProjectCfg((c: any) => ({ ...c, mcpServers }))}
                showAddButton={false}
              />
            </section>

            <section className="settings-section">
              <div className="settings-section-head">
                <h4>主 Agent 覆盖（.aidog/main.yml）</h4>
              </div>
            {(() => {
              const main = projectCfg.main ?? {}
              const patchMain = (p: any) => setProjectCfg((c: any) => ({
                ...c, main: { ...(c.main ?? {}), ...p },
              }))
              return (
                <>
                  <div className="form-grid">
                    <Label>模型覆盖</Label>
                    <Select
                      value={modelRef(main.model ?? '')}
                      onValueChange={v => patchMain({ model: v === '__default__' ? '' : v })}
                    >
                      <SelectTrigger size="sm" className="w-full">
                        <SelectValue placeholder="跟随全局激活模型" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__default__">跟随全局激活模型</SelectItem>
                        {(cfg.models ?? []).map(m => (
                          <SelectItem key={m.id} value={m.id}>{modelLabel(m)}</SelectItem>
                        ))}
                        {main.model && modelRef(main.model) === main.model && (
                          <SelectItem value={main.model}>{main.model}（当前配置）</SelectItem>
                        )}
                      </SelectContent>
                    </Select>
                    <Label>工具</Label>
                    <MultiSelect
                      value={main.tools ?? []}
                      options={toolOptions}
                      onChange={v => patchMain({ tools: v })}
                    />
                    <Label>Skills</Label>
                    <MultiSelect
                      value={main.skills ?? []}
                      options={skillOptions}
                      onChange={v => patchMain({ skills: v })}
                    />
                    <Label>MCP Servers</Label>
                    <MultiSelect
                      value={main.mcp_servers ?? []}
                      options={mcpOptions}
                      onChange={v => patchMain({ mcp_servers: v })}
                    />
                  </div>
                  <div className="settings-subhead">执行预算（0 = 默认值）</div>
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
                  <div className="settings-subhead">上下文压缩（阈值 0-1，0 = 默认 0.7）</div>
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
            </section>

            <div className="row-actions">
              <Button variant="secondary" size="sm" disabled={projectBusy} onClick={saveProject}>
                保存项目配置
              </Button>
              {projectMsg && <span className="hint">{projectMsg}</span>}
            </div>
            <p className="hint">
              保存会一并提交项目配置、work agent 定义与全局「自动优化 Agent」开关（分别写入 .aidog 与全局配置）。
            </p>
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

      {/* 供应商模型检测：勾选后批量添加 */}
      {/* 预设供应商：Base URL 与候选模型来自官方文档，添加后填写 API Key 即可 */}
      <Dialog open={presetOpen} onOpenChange={setPresetOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>添加预设供应商</DialogTitle>
            <DialogDescription>
              选择供应商与候选模型；添加后填写 API Key 并保存即可使用。
            </DialogDescription>
          </DialogHeader>
          <div className="form-grid">
            <Label>供应商</Label>
            <Select value={presetId} onValueChange={changePreset}>
              <SelectTrigger size="sm" className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                {PROVIDER_PRESETS.map(p => (
                  <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Label>Base URL</Label>
            <Input value={preset.baseURL} readOnly />
          </div>
          {preset.note && <p className="hint">{preset.note}</p>}
          <div className="multiselect-panel" style={{ marginTop: 8 }}>
            {preset.models.map(m => (
              <label key={m.id} className="multiselect-item">
                <input
                  type="checkbox"
                  checked={presetModels.has(m.id)}
                  onChange={() => togglePresetModel(m.id)}
                />
                <span className="multiselect-item-name">{m.name}</span>
                <span className="hint">{m.id}{m.note ? ` · ${m.note}` : ''}</span>
              </label>
            ))}
          </div>
          {preset.docsURL && <p className="hint">文档：{preset.docsURL}</p>}
          <DialogFooter>
            <Button variant="secondary" onClick={() => setPresetOpen(false)}>取消</Button>
            <Button onClick={addPreset} disabled={presetModels.size === 0}>
              添加 {presetModels.size} 个模型
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!detect} onOpenChange={v => { if (!v) setDetect(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>检测可用模型</DialogTitle>
            <DialogDescription>
              {detect ? `供应商：${detect.providerName}` : ''}
            </DialogDescription>
          </DialogHeader>
          {detect?.loading && <p className="detect-status">正在请求 /models…</p>}
          {detect?.error && <p className="detect-error">{detect.error}</p>}
          {detect && !detect.loading && detect.models.length > 0 && (
            <>
              <div className="detect-toolbar">
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => setDetect(prev => prev ? { ...prev, selected: new Set(prev.models) } : prev)}
                >全选</Button>
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => setDetect(prev => prev ? { ...prev, selected: new Set() } : prev)}
                >全不选</Button>
                <span className="detect-count">已选 {detect.selected.size} / {detect.models.length}</span>
              </div>
              <div className="detect-list">
                {detect.models.map(id => (
                  <label key={id} className="detect-item">
                    <input
                      type="checkbox"
                      checked={detect.selected.has(id)}
                      onChange={() => toggleDetectModel(id)}
                    />
                    <span>{id}</span>
                  </label>
                ))}
              </div>
            </>
          )}
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setDetect(null)}>取消</Button>
            <Button
              size="sm"
              disabled={!detect || detect.loading || detect.selected.size === 0}
              onClick={confirmDetect}
            >添加选中（{detect?.selected.size ?? 0}）</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
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
