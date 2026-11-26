# 多 Agent 运行时（阶段 A）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按设计文档阶段 A 落地 work agent 运行时与 Agent Runtime：身份分离、模型路由、配置分层、六项执行上限、独立工具/MCP/Skill/上下文、主 Agent 动态分派。阶段 B–E 留待后续批次。

**Architecture:** 在现有 Eino ADK `ChatModelAgent` 外增加 `Execution` 计数器与超时上下文（`internal/agent/runtime.go`），通过全局 callbacks 采集步骤/工具/Token/成本；`agentspec.Spec` 由 `BuildWorkAgent` 消费为独立 Agent；项目级配置（`.aidog`）在内存中与全局配置合并；主 Agent 依据项目 Agent 列表生成 `dispatch_<sanitized>` 工具。

**Tech Stack:** Go 1.26 + Eino ADK + Wails3 + React/TS。

**Spec:** `docs/superpowers/specs/2026-11-26-multi-agent-architecture-design.md` §3.1–3.6、§6 阶段 A。

**Status:** 阶段 A 已完成（2026-11-26）；阶段 B–E 未开始。

## Global Constraints

- 提示词稳定前缀不可插入动态内容：`Spec.SystemPrompt` / 主 Agent 静态前缀在前，Skills 与项目上下文追加在后。
- 所有文件/命令操作仍以 workspace 为边界；work agent 的写/执行审批归属调用方 session。
- work agent 的 `Spec.Tools` 为空时默认只读；未声明的 MCP 不暴露。
- `Spec.Model` 找不到时回退主 Agent 激活模型并 `slog.Warn`，不直接不可用。
- 用量与成本以 API `usage` 为准；实际模型单价折算，缺失 usage 时不静默当 0。
- 不改变现有主 Agent 事件契约与消息持久化格式；超限按现有 `failed` 状态落库（B 阶段再迁移状态机）。
- 验证命令：`go test ./internal/...`、`go vet ./...`、`npm --prefix frontend run build`。

## 已完成任务

### Task A1: 身份拆分与模型路由

**Files:** `internal/llm/factory.go`、`fallback.go`、`route.go`、`internal/agent/chat.go`、`callback.go`、`internal/eventbus/eventbus.go`、`internal/store/{store,duckdb}.go`

- [x] `BuildModel` / `BuildModelChain` 增加 `userID`，`extra_fields` 拆分 `agent_id` / `user_id`；`DisableExtraFields` 整体关闭。
- [x] `ResolveModel` 按 ID → Name → Model 唯一命中；`BuildWorkModelChain` 以 `Spec.Model` 为主、其余全局模型兜底，未命中回退主激活模型。
- [x] 用量按实际模型归因：`obsInfo` 携带 AgentID/ModelID/单价，`llm_usage` 事件与 `token_usage` 表增加 `agentId`/`model_id`（DuckDB 旧库迁移）。
- [x] `model_fallback` 事件增加 `agentId`。
- [x] 测试：httptest 断言身份字段、`ResolveModel` 表驱动、work 链顺序/回退、实际模型成本。
- [x] 提交：`7985557 feat: 拆分 agent_id/user_id 并支持 work agent 模型路由`

### Task A2: 配置分层与运行时配置模型

**Files:** `internal/config/config.go`、`project.go`、`internal/agentspec/agentspec.go`、`internal/service/config_service.go`

- [x] `ModelConfig` 增加 `ContextWindow`（0 → 32000）与 `IncludeUsage`（显式注入 `stream_options.include_usage`）。
- [x] `agentspec.Spec` 增加 `Budget`（steps/tokens/duration/tool_calls/cost/retries）与 `Context`（compress_threshold/keep_recent），含非负与阈值校验。
- [x] `LoadProject` / `SaveProject` 读写 `.aidog/main.json`、`mcp_servers.json`、`skills.json`；`.aidog` 存在时记录信任告警。
- [x] `MergeProject` 项目级同 ID/Name 覆盖全局、其余追加；悬空引用返回 warning。
- [x] `config.Manager.Effective(projectRoot)` 合并视图，`Config.Project` 带 `json:"-"`，项目配置不写回全局。
- [x] `ConfigService` 增加项目配置与项目 MCP/Skill 读写 RPC。
- [x] 测试：覆盖优先级、悬空引用、原子读写、ContextWindow 序列化、Spec 校验。
- [x] 提交：`8ba3388 feat: 项目级配置分层与 Agent 预算/压缩配置`

### Task A3: Agent Runtime 执行引擎

**Files:** `internal/agent/runtime.go`、`chat.go`、`callback.go`、`assembly.go`

- [x] `Limits` / `WorkLimits` / `MainLimits`：Budget 覆盖 + work/主 Agent 默认值。
- [x] `Execution`：步骤、工具次数、Token、成本原子计数；超限 `trip` 并 cancel。
- [x] `StartExecution` 用 `context.WithTimeout` 承担最大执行时间。
- [x] 全局 callbacks 在 ChatModel（流式/非流式）累加使用量，在 Tool OnStart 累加工具次数。
- [x] `LimitError` 携带 Kind/Limit/Used 并可 `errors.Is` 判定 `ErrMaxSteps` / `ErrMaxTokens` / `ErrBudgetExceeded` / `ErrMaxToolCalls` / `ErrTimeout`。
- [x] `runTurn` 统一走 `exec.Run`；超限保留已产出助手文本、emit `chat_error`、按现有 `failed` 落库；用户中断优先判为 `interrupted`。
- [x] 主 Agent `MaxIterations = limits.MaxSteps`，`retryConfig(limits.MaxRetries)`；`HardIterationLimit` 支持 `Spec.MaxTurns` 优先。
- [x] 测试：六项上限、默认值、HardIterationLimit、超时与用户取消区分。
- [x] 提交：`8d84603 feat: Agent Runtime 执行上限与结构化终止`

### Task A4: work agent 运行时

**Files:** `internal/tools/catalog.go`、`internal/agent/workagent.go`、`assembly.go`

- [x] `tools.Catalog`：按名选择工具；空列表默认只读；写工具需审批；未知工具名报错；`run_shell` 构造抽到 tools 包。
- [x] `BuildWorkAgent`：独立模型链（`Spec.AgentID` + 主 ID）、`Spec.SystemPrompt → Skills → 项目上下文`、按 `Spec.Tools`/`Spec.MCPServers` 过滤、`MaxIterations`、WorkLimits。
- [x] `DispatchToolName` 合法化（`[a-z0-9-]`，中文回退 agent_id 前缀，≤64 字符）。
- [x] 测试：指令顺序、MCP/Skill 过滤、工具名、模型回退、缺 agent_id/workspace 报错、Catalog 行为。
- [x] 提交：`8a79e5f feat: work agent 独立运行时与工具目录`

### Task A5: 主 Agent 动态分派

**Files:** `internal/agent/chat.go`、`assembly.go`、`prompts.go`、`internal/prompts/orchestrator.tmpl`

- [x] `ChatService.buildDispatchTools`：每个 Spec 构建 work agent 并包装为 `adk.NewAgentTool`，工具名即 `dispatch_<name>`，重名跳过。
- [x] `OrchestratorTools` 检测 `dispatch_explorer`：项目配置存在时覆盖内置 explorer。
- [x] `BuildExplorer` 名称统一为 `dispatch_explorer`（与模板一致）。
- [x] `PromptData.Dispatches` 与模板更新：instruction 列出实际可用工具；无工具时明确提示。
- [x] 测试：命名/去重、instruction 工具名、explorer 覆盖/默认、会话装配一致。
- [x] 提交：`e4a07e3 feat: 主 Agent 动态分派项目 Agent`

### Task A6: 回归与收口

- [x] `go test ./internal/...` 全绿。
- [x] `go vet ./...` 无输出。
- [x] `npm --prefix frontend run build` 成功。
- [x] 计划文档（本文件）。
- [ ] 设计文档状态与 README 更新（Task A6 收口提交）。

## 验收结果

| 验收项 | 结果 |
|---|---|
| 主/work agent 请求体 `agent_id`/`user_id` 正确 | ✅ httptest |
| `Spec.Model` 路由、回退与备用链 | ✅ route_test |
| 项目级配置合并且不污染全局 | ✅ project_test |
| work agent 工具/MCP/Skill/上下文隔离 | ✅ workagent_test |
| 六项执行上限终止与结构化原因 | ✅ runtime_test |
| 主 Agent 分派工具与提示词一致 | ✅ dispatch_test |
| 全量 Go 测试 / vet / 前端构建 | ✅ |

## 阶段 B–E 待办

### 阶段 B：任务状态机

- 扩展 `store` 状态：PENDING/READY/ASSIGNED/RUNNING/BLOCKED/REVIEW/REJECTED/COMPLETED/CANCELLED/FAILED。
- `Transition` 合法迁移表与旧数据映射（running→RUNNING、interrupted→CANCELLED、failed→FAILED/REJECTED）。
- 审批请求 → BLOCKED，审批结果 → RUNNING/REJECTED；TaskPanel 新状态展示。

### 阶段 C：agent-to-agent 私聊

- `AgentRegistry` 按 `(session, agent)` 保持历史/inbox。
- `send_message` 同步 + 超时/取消 + 深度限制；事件流展示来源/目标 Agent。

### 阶段 D：agent-smith

- 应用级后台触发（任务结束/手动），读取任务与配置生成/修改 Spec，写操作走审批。

### 阶段 E：上下文压缩 + 配置页

- 按 `ModelConfig.ContextWindow × Spec.Context.CompressThreshold` 触发压缩。
- 摘要失败降级规则截断；`context_compressed` 事件与持久化。
- SettingsPanel 按全局/项目分组，项目专属 MCP/Skill、预算/压缩阈值编辑与 `.aidog` 信任确认。

## 已知限制与风险

- **ErrMaxRetries**：已定义但 ADK 内部重试用尽后以普通错误上报，暂未形成独立触发点。
- **超限边界**：ADK `MaxIterations` 与 Runtime 外层计数可能相差一次调用；两者同时存在，超限原因以 Runtime 计数为准。
- **项目配置信任**：阶段 A 仅记录 slog 告警，UI 确认留阶段 E。
- **MCP 连接数**：每个 work agent 独立 MCP 集合可能启动多个子进程；`mcp.Manager` 按 server+workdir 复用连接，后续仍需上限策略。
- **usage 缺失**：端点不返回 usage 时预算无法判定，仅记录日志，不静默通过。
- **配置页**：项目配置 RPC 已就绪，但前端仍展示全局配置，阶段 E 统一改造。
