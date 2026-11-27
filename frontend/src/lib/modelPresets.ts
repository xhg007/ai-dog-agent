// 常用模型供应商预设。
//
// 数据核对日期：2026-11-27。Base URL 与模型 ID 来自各家官方文档：
//   - DeepSeek: https://api-docs.deepseek.com/zh-cn/quick_start/pricing
//   - 阿里云百炼: https://help.aliyun.com/zh/model-studio/getting-started/models
//   - 智谱 GLM: https://docs.bigmodel.cn/cn/guide/start/model-overview
//   - Kimi: https://platform.kimi.com/docs/models
//   - 火山方舟: https://ark.volcengine.com/docs/model-list
//   - 硅基流动: https://docs.siliconflow.cn/cn/userguide/capabilities/text-generation
//   - OpenAI: 官方 models 页面被网络策略拦截，候选来自公开聚合目录，建议用「检测模型」核对。
//
// 模型迭代很快：预设只是起点，添加后仍可通过「检测模型」拉取账号当前可用模型。

export interface PresetModel {
  id: string
  name: string
  note?: string
  recommended?: boolean
  contextWindow?: number
}

export interface ProviderPreset {
  id: string
  name: string
  baseURL: string
  docsURL?: string
  note?: string
  models: PresetModel[]
}

export const PROVIDER_PRESETS: ProviderPreset[] = [
  {
    id: 'deepseek',
    name: 'DeepSeek',
    baseURL: 'https://api.deepseek.com',
    docsURL: 'https://api-docs.deepseek.com/zh-cn/quick_start/pricing',
    note: '旧模型 deepseek-chat / deepseek-reasoner 已停用；当前为 V4.1 Flash 与 V4 Pro。',
    models: [
      {
        id: 'deepseek-flash',
        name: 'DeepSeek V4.1 Flash',
        note: '快速、支持视觉，推荐',
        recommended: true,
        contextWindow: 1000000,
      },
      {
        id: 'deepseek-v4-pro',
        name: 'DeepSeek V4 Pro',
        note: '复杂推理',
        contextWindow: 1000000,
      },
    ],
  },
  {
    id: 'dashscope',
    name: '阿里云百炼（通义千问）',
    baseURL: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    docsURL: 'https://help.aliyun.com/zh/model-studio/getting-started/models',
    note: '官方推荐业务空间专属域名 https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1；通用域名仍可用，API Key 需与地域匹配。',
    models: [
      { id: 'qwen4-max', name: 'Qwen4 Max', note: '复杂推理与编程，推荐', recommended: true },
      { id: 'qwen3.7-plus', name: 'Qwen3.7 Plus', note: '性能、速度与成本均衡' },
      { id: 'qwen3.8-flash', name: 'Qwen3.8 Flash', note: '快速且低成本' },
    ],
  },
  {
    id: 'glm',
    name: '智谱 GLM',
    baseURL: 'https://open.bigmodel.cn/api/paas/v4',
    docsURL: 'https://docs.bigmodel.cn/cn/guide/models/overview',
    note: 'GLM-5.4 尚未开放 API；预设使用已开放的 GLM-5.3 系列。',
    models: [
      {
        id: 'glm-5.3',
        name: 'GLM-5.3',
        note: '旗舰，编程与 Agent，推荐',
        recommended: true,
        contextWindow: 1000000,
      },
      {
        id: 'glm-5.3-flash',
        name: 'GLM-5.3 Flash',
        note: '原生多模态，快速',
        contextWindow: 1000000,
      },
      { id: 'glm-5.2', name: 'GLM-5.2', note: '长程任务', contextWindow: 1000000 },
      { id: 'glm-4.7', name: 'GLM-4.7', note: '通用对话与推理', contextWindow: 200000 },
    ],
  },
  {
    id: 'kimi',
    name: 'Kimi（月之暗面）',
    baseURL: 'https://api.moonshot.cn/v1',
    docsURL: 'https://platform.kimi.com/docs/models',
    note: 'K2.7 及更早模型已下线或即将下线；预设当前 K3.1 / K3。',
    models: [
      {
        id: 'kimi-k3.1-pro',
        name: 'Kimi K3.1 Pro',
        note: '旗舰，1M 上下文，推荐',
        recommended: true,
        contextWindow: 1000000,
      },
      { id: 'kimi-k3.1-pro-highspeed', name: 'Kimi K3.1 Pro 高速版', note: '约 2-3 倍速度' },
      { id: 'kimi-k3', name: 'Kimi K3', note: '上一代旗舰', contextWindow: 1000000 },
    ],
  },
  {
    id: 'ark',
    name: '火山方舟（豆包）',
    baseURL: 'https://ark.cn-beijing.volces.com/api/v3',
    docsURL: 'https://ark.volcengine.com/docs/model-list',
    note: '模型 ID 含版本后缀；需先在方舟控制台开通对应模型。',
    models: [
      {
        id: 'doubao-seed-2-1-pro-260915',
        name: 'Doubao Seed 2.1 Pro',
        note: '旗舰，1M 上下文，推荐',
        recommended: true,
        contextWindow: 1024000,
      },
      {
        id: 'doubao-seed-2-1-lite-260915',
        name: 'Doubao Seed 2.1 Lite',
        note: '高并发，快速',
        contextWindow: 1024000,
      },
      {
        id: 'doubao-seed-2-1-turbo-260628',
        name: 'Doubao Seed 2.1 Turbo',
        note: '低延迟',
        contextWindow: 256000,
      },
      {
        id: 'doubao-seed-evolving',
        name: 'Doubao Seed Evolving',
        note: '快速迭代模型',
        contextWindow: 1024000,
      },
    ],
  },
  {
    id: 'siliconflow',
    name: '硅基流动 SiliconFlow',
    baseURL: 'https://api.siliconflow.cn/v1',
    docsURL: 'https://cloud.siliconflow.cn/models',
    note: '聚合多家中立模型，模型 ID 以模型广场为准；建议添加后用「检测模型」刷新。',
    models: [
      { id: 'deepseek-ai/DeepSeek-V3.2', name: 'DeepSeek V3.2', note: '推荐', recommended: true },
      { id: 'Pro/zai-org/GLM-5.1', name: 'GLM-5.1（Pro）', note: '编程与 Agent' },
      { id: 'Qwen/Qwen3.6-27B', name: 'Qwen3.6 27B', note: '通用' },
    ],
  },
  {
    id: 'openai',
    name: 'OpenAI',
    baseURL: 'https://api.openai.com/v1',
    docsURL: 'https://platform.openai.com/docs/models',
    note: '官方模型页在当前网络不可达；候选来自公开目录，请以「检测模型」结果为准。',
    models: [
      { id: 'gpt-6.2-astra-pro', name: 'GPT-6.2 Astra Pro', note: '旗舰，推荐', recommended: true },
      { id: 'gpt-6.2-astra', name: 'GPT-6.2 Astra', note: '均衡' },
      { id: 'gpt-6.2-sol-pro', name: 'GPT-6.2 Sol Pro', note: '推理' },
    ],
  },
]
