# ai-dog-agent 需求文档

> 版本：v0.1（2026-11-20）
> 状态：待评审
> 产品定位：**桌面端多 Agent 编码开发助手**（Windows 优先），以 Wails3 + React 为 GUI，以 Eino 为 Agent 内核。

---

## 1. 产品概述

### 1.1 一句话描述

ai-dog-agent 是一个运行在用户桌面上的 AI 编码 Agent 工具：用户在 GUI 中选定一个本地项目（workspace），Agent 自主完成「理解项目 → 操作代码 → 执行测试 → 自动修复」的闭环，全程事件流可视化、高风险操作人工审批、失败可回滚。

### 1.2 核心用户价值

| 价值 | 说明 |
|---|---|
| 项目理解 | 自动分析目录结构、Go Module、入口、符号引用关系，快速建立代码上下文 |
| 代码操作 | 通过受控工具读写/编辑/检索代码，所有改动记录 Git Diff |
| 执行与修复 | 跑 `go test` / `go vet`，解析编译错误，限轮数自动修复 |
| 安全可控 | 文件操作限制在 workspace、Shell 白名单、高风险审批、可回滚 |
| 生产化 | 事件流、任务持久化、中断恢复、Token 成本统计、Tracing、模型故障切换 |

### 1.3 用户界面形态

单窗口桌面应用（Wails3），主要界面：

1. **工作区面板**：选择/切换 workspace（本地项目目录），展示项目结构树。
2. **会话面板**：与 Agent 对话；展示 Agent 执行事件流（工具调用、审批请求、测试输出）。
3. **审批弹层**：高风险操作（写文件、Shell 命令）的 diff 预览 + 批准/拒绝。
4. **任务面板**：任务列表、状态（运行/中断/完成/失败）、历史任务恢复。
5. **设置页**：模型配置（Base URL / API Key / Model，多供应商多 Key）、Shell 白名单、审批策略、Token 配额。

---

## 2. 技术选型（确认）

| 层次 | 选型 | 说明 |
|---|---|---|
| Agent 框架 | `github.com/cloudwego/eino` | Agent 编排内核 |
| 多 Agent | Eino ADK（`adk` 包，ChatModelAgent / SubAgent / Runner） | 主 Agent 分派子 Agent |
| 工作流 | Eino Graph / Workflow | 多步骤确定性流程（如「理解项目」管线） |
| 模型适配 | `eino-ext/components/model/openai`（OpenAI 兼容协议，**自填 Base URL/Key/Model**） | 一个配置覆盖 DeepSeek / Qwen / GLM / OpenAI，多模型故障切换 |
| 工具协议 | `github.com/modelcontextprotocol/go-sdk` | 内置工具可直接注册；预留 MCP Client 接入外部工具 |
| 代码索引 | **M2 已落地**：Go 项目用标准库 go/parser + go/ast 提取符号表（精确、无 CGO 依赖）；引用分析用正则 + 词边界；其他语言暂用文件/文本工具（read/list/search）理解，tree-sitter 多语言索引延后到 M6；向量检索后置 | 产品语言无关；Go 为首个一等支持，stdlib AST 即可实现用户决策的「AST 符号 + 正则引用」 |
| 任务执行隔离 | 独立 workspace + 独立 `context.Context` + 并发上限 | 每个 Task 一个独立执行域 |
| 可观测性 | Eino Callback + OpenTelemetry（OTLP 导出，可选开关） | Trace/Token 统计 |
| GUI | Wails3 + React + TypeScript | 事件用 Wails Event 双向推送 |
| 持久化 | SQLite（`modernc.org/sqlite`，纯 Go 无 CGO） | 任务/会话/事件/Token 用量 |

**已确认的环境事实**：Windows（PowerShell）、Go 1.26、当前为 Wails3 空脚手架，eino/eino-ext/MCP SDK 已在 go.mod。

---

## 3. 系统架构

### 3.0 多 Agent 架构（本项目与主流单 Agent 工具的核心差异）

| 角色 | 职责 | 配置来源 | 上下文 |
|---|---|---|---|
| **主 Agent（Orchestrator）** | 软件启动时常驻；理解用户意图、制定计划、编排与分派子 Agent；汇总结果 | 应用级配置 + 稳定前缀系统提示词 | 独立上下文（任务级会话） |
| **子 Agent（项目级）** | 执行具体工作（探索/编码/修复/自定义）；各有独立的上下文、skill、MCP 连接、function call 工具集 | **随项目走**：workspace 内 `.aidog/agents/*.yml` | 完全独立（不共享主 Agent 历史） |
| **后台 Meta Agent（agent-smith）** | 默认后台运行；负责**优化与创造子 Agent**（分析任务表现，生成/修改 `.aidog/agents/*.yml`）；写操作走审批 | 应用级固定提示词 | 独立上下文 |

约束：

- **子 Agent 定义（Schema）**：`{ name, description, system_prompt, model(可选覆盖), tools[], skills[], mcp_servers[], max_turns }`。
- **上下文前缀稳定**：所有 Agent 的 system prompt 采用「稳定静态前缀 + 动态尾部」结构——角色定义、工具说明、输出规范等静态内容在前，项目信息、任务上下文等动态内容追加在后；保证供应商提示词缓存（KV cache）命中，降低成本与首 Token 延迟。
- **隔离**：子 Agent 的工具调用同样经过统一安全层（workspace 限制、白名单、审批），但其可用工具集由自身配置决定。

### 3.1 模块划分（Go 包结构规划）

```
ai-dog-agent/
├── main.go                    # Wails3 入口，注册 Services
├── internal/
│   ├── config/                # 应用配置（模型、审批策略、白名单），JSON 存于用户目录
│   ├── workspace/             # workspace 管理：注册、校验、路径安全（逃逸检查）
│   ├── agentspec/             # 子 Agent 配置读写：workspace/.aidog/agents/*.yml（Schema 校验）
│   ├── tools/                 # Agent 工具实现（eino tool.InvokableTool）
│   │   ├── fs.go              #   read_file / write_file / edit_file / list_files
│   │   ├── search.go          #   search_code（字面 + 正则）
│   │   ├── git.go             #   git_diff / git_status（只读）
│   │   ├── shell.go           #   run_shell（白名单校验 + 审批）
│   │   └── gotool.go          #   go_test / go_vet / go_build（阶段三）
│   ├── indexer/               # 项目索引：目录树、go.mod 解析、tree-sitter 符号表
│   ├── agent/                 # 编排层（Eino ADK）
│   │   ├── orchestrator.go    #   主 Agent：制定计划、分派子 Agent、汇总
│   │   ├── subagent.go        #   子 Agent 运行时：独立上下文 + 按配置装配工具/skill/MCP
│   │   ├── smith.go           #   后台 Meta Agent：优化/创造子 Agent 配置
│   │   ├── prefix.go          #   稳定前缀系统提示词构建（静态前缀 + 动态尾部）
│   │   └── registry.go        #   Agent 注册表：主/子/Meta Agent 的生命周期管理
│   ├── approval/              # 审批管理器：请求 → 前端弹层 → 等待决策（支持任务内记住）
│   ├── task/                  # 任务管理器：创建/持久化/中断/恢复
│   ├── eventbus/              # 事件总线：Agent 事件 → Wails Event 推送
│   ├── llm/                   # 模型工厂：OpenAI 兼容配置、故障切换、用量回调
│   └── store/                 # SQLite 持久层（任务、事件、消息、Token 用量）
├── frontend/                  # React + TS（wails3 生成的工程）
```

### 3.1.1 Agent 唯一标识与请求体

- **每个 Agent（含每个会话的主 Agent 实例、每个子 Agent）拥有全局唯一 ID（UUID）**。
- 构建 LLM 请求时，该 ID 注入 **请求体自定义字段 `agent_id`**（经 OpenAI 兼容协议扩展字段 `ExtraFields` 合并进 chat.completions 请求体），随每次模型调用上送。
- 用途：供应商侧限流归因、调用链追踪、按 Agent 维度的 Token/成本核算（与 FR-5.4 呼应）。
- 前端在会话头部展示 Agent ID（短格式）。

### 3.1.2 限流与重试（参考 DeepSeek 限流文档 api-docs.deepseek.com/zh-cn/quick_start/rate_limit）

- DeepSeek 等供应商按账户并发/速率限流，超限返回 429。
- 所有模型调用启用 ADK `ModelRetryConfig`：最多重试 3 次，指数退避（ADK 默认 100ms 起步、上限 10s、带抖动）；用户主动取消（context 取消）不重试。
- 重试期间不中断事件流；最终失败向事件流推送明确错误信息。
- 后续里程碑（FR-5.6 故障切换）在此基础上叠加多模型切换。

### 3.1.3 前端 UI 参考

- 布局与配色借鉴主流 AI 编程工具（Cursor / GitHub Copilot Chat）：中性深灰画布（#191919 系）、细边框分区、蓝色强调、左栏导航 + 会话区。
- 用户消息为右侧深灰圆角气泡；助手消息全宽无底色、带角色头像；输入区为大圆角聚焦边框容器。
- 会话头部展示：当前模型名、Agent ID、新会话按钮。

### 3.1.4 开发调试 MCP（wails3）

- `wails3 dev` 保持最小参数形式（配合 `$env:WAILS_MCP = "1"`）。
- dev 模式自动生成前端绑定，并启动开发调试 MCP server（streamable HTTP，`http://127.0.0.1:9099/mcp`）；MCP 客户端需在 dev 运行后连接，必要时刷新。

### 3.2 关键数据流

```
用户输入 → TaskManager 创建任务 → Eino ADK Runner
   → Agent 决策调用工具 → ToolManager（安全检查：workspace 限制 / 白名单 / 审批）
       → 需审批：ApprovalManager 发事件给前端 → 用户批准/拒绝 → 继续/终止
       → 免审批：直接执行 → 结果回给 Agent
   → 每一步产生 Event（tool_call / tool_result / approval / llm_usage / state_change）
       → EventBus → Wails Event → React 渲染；同时写入 SQLite（可恢复）
   → 任务结束：生成 Git Diff 摘要，失败可一键回滚（git checkout / restore）
```

### 3.3 安全模型

- **路径安全**：所有文件工具入参经 `filepath.Clean` + 前缀校验，禁止绝对路径越出 workspace、禁止 `..` 逃逸、拒绝符号链接逃逸。
- **Shell 白名单**：默认仅允许 `go`（build/test/vet/fmt）、`git`（status/diff/log）、`dir/ls`；命令解析首 token + 子命令匹配白名单；白名单外一律触发审批。
- **审批分级**（可配置）：
  - `write_file`/`edit_file`：默认需审批（前端展示 unified diff）；
  - `read_file`/`list_files`/`search_code`/`git_diff`：免审批；
  - 白名单内 Shell：免审批；白名单外：必须审批；
  - 删除文件、`git push/commit/reset`：必须审批。
- **最大修复轮数**：fixer 子 Agent 循环上限（默认 3，可配置），超限终止并汇报。

---

## 4. 功能需求（按阶段细化）

### 阶段一：项目理解（FR-1）

| ID | 需求 | 验收标准 |
|---|---|---|
| FR-1.1 | 读取项目目录与文件结构 | ✅ `list_files`（递归/上限/大小）；忽略 `.git`、`node_modules`、`dist`、`vendor` 等 |
| FR-1.2 | 读取 README / AGENTS.md | ✅ `BuildProjectContext` 注入 README/AGENTS.md 摘要到 explorer 动态上下文尾部 |
| FR-1.3 | 识别 Go Module、依赖与入口 | ✅ `x/mod/modfile` 解析 go.mod（module/go/require/replace）；`go/ast` 扫描 `package main` + `func main` 定位入口 |
| FR-1.4 | 代码符号检索与引用关系 | ✅ go/ast 符号表（func/method/struct/interface/type，含行号）；`search_code` 关键字检索；`find_references` 正则 + 词边界 |
| FR-1.5 | 「理解项目」一键工作流 | ✅ 主 Agent 通过 `dispatch_explorer`（ADK AgentTool + EmitInternalEvents）分派 explorer 子 Agent（独立上下文、只读工具集 + `project_summary`）；子 Agent 事件流实时转发到前端 |

### 阶段二：代码操作（FR-2）

| ID | 需求 | 验收标准 |
|---|---|---|
| FR-2.1 | `read_file(path, offset?, limit?)` | 返回带行号内容；大文件强制分页；二进制拒绝 |
| FR-2.2 | `write_file(path, content)` | ✅ 全量写入；自动建父目录；覆盖前备份原文件到应用数据目录 backups/；经审批门（前端卡片批准/拒绝/记住）；拒绝返回 ErrDenied 给模型 |
| FR-2.3 | `edit_file(path, old_str, new_str, replace_all?)` | ✅ 精确串替换；old_str 必须恰好匹配一次（0 次或多于 1 次均报错）；替换前备份；经审批门 |
| FR-2.4 | `search_code(pattern, glob?, is_regex?, max_results?)` | 字面/正则检索，返回文件+行号+内容；结果上限默认 100 |
| FR-2.5 | `list_files(pattern?, recursive?)` | 通配符文件搜索 |
| FR-2.6 | `git_diff()` / `git_status()` | 只读；返回当前 workspace 相对 HEAD 的 diff/stat |
| FR-2.7 | 所有写操作记录 | 每次写/edit 前后内容存入 SQLite（task_events 表），任务结束生成累计 Git Diff |

### 阶段三：执行与修复（FR-3）

| ID | 需求 | 验收标准 |
|---|---|---|
| FR-3.1 | `go_test(dir?, run?, verbose?)` | 执行 `go test ./...`，截断超长输出，结构化提取失败用例与错误行 |
| FR-3.2 | `go_vet` / `go_build` | 同上模式；返回非零退出码 + stderr 摘要 |
| FR-3.3 | 编译错误分析 | fixer 子 Agent 解析错误（文件/行号/信息），调用 read/edit 修复 |
| FR-3.4 | 自动修复循环 | 「测试失败 → 分析 → 修复 → 重测」循环；`max_fix_rounds` 默认 3，超限终止并输出未解决清单 |
| FR-3.5 | 输出保护 | 命令超时（默认 120s）、输出截断（默认 64KB）、并发限制（同时 1 个 Shell 任务） |

### 阶段四：安全与审批（FR-4）

| ID | 需求 | 验收标准 |
|---|---|---|
| FR-4.1 | workspace 隔离 | 文件/Shell 工具强制 cwd=workspace；路径越界返回安全错误（不泄露绝对路径外信息） |
| FR-4.2 | Shell 白名单 | 默认白名单见 3.3；设置页可增删；白名单校验在执行前完成 |
| FR-4.3 | 高风险人工审批 | ✅ `approval.Manager`：工具调用阻塞等待决策；事件 `approval_request`（含工具/路径/行级 diff 预览）→ 前端对话流内审批卡片（批准 / 批准并记住 / 拒绝）；「记住」按会话+工具+路径生效（白名单外 Shell 将不适用，M4）；支持 `ListPending` 对账恢复（页面重载后卡片不丢） |
| FR-4.4 | Git Diff 记录 | 任务完成时自动生成并展示本次任务累计 diff；可复制/导出 |
| FR-4.5 | 失败回滚 | 优先 `git`（restore/checkout）；workspace 非 git 仓库时用写前备份逐文件恢复；回滚本身需审批 |

### 阶段五：生产化（FR-5）

| ID | 需求 | 验收标准 |
|---|---|---|
| FR-5.1 | Agent 事件流 | 事件类型：`task_state`、`agent_message`、`tool_call`、`tool_result`、`approval_request`、`approval_result`、`llm_usage`、`error`；前端实时渲染 |
| FR-5.2 | 任务持久化 | ✅ 先行落地轻量版：会话元数据 + 消息历史（含工具步骤）按会话落盘为 `%AppData%/ai-dog-agent/sessions/*.json`，重启后线程列表与对话历史完整恢复；SQLite `tasks/task_events/token_usage` 表仍属 M5 |
| FR-5.3 | 中断与恢复 | ✅ 部分：用户可中断运行中任务（取消 context）；消息历史持久化并在后续轮次回放给模型（无状态客户端管理历史）；断点级 checkpoint 恢复仍属 M5 |
| FR-5.4 | Token/成本统计 | Eino Callback 采集每次调用 input/output tokens，按模型单价配置折算成本；设置页可编辑单价；任务/日维度报表 |
| FR-5.5 | 日志与 Tracing | slog 结构化日志（文件 + 控制台）；OTel OTLP 导出可开关；Eino Callback 织入 LLM Span / Tool Span |
| FR-5.6 | 模型故障切换 | 按模型列表顺序重试（网络错误/429/5xx 可切换），切换事件上报事件流 |

---

## 5. 非功能需求

- **性能**：索引 10 万行级 Go 项目 < 10s（增量索引）；事件流推送延迟 < 100ms。
- **可靠性**：Agent 崩溃不影响已落盘数据；SQLite WAL 模式。
- **安全**：API Key 本地加密存储（DPAPI 或 AES + 机器码派生）；日志脱敏（不落 API Key、不落用户代码全文）。
- **可配置**：`~/.ai-dog-agent/config.json`，设置页修改即时生效。
- **平台**：Windows 优先（本机开发环境）；代码保持跨平台兼容（路径用 `filepath`）。

---

## 6. 里程碑划分（开发顺序）

| 里程碑 | 内容 | 交付物 |
|---|---|---|
| **M0 骨架** | module 改名、包结构、config、slog、前端布局骨架 | 应用可启动，设置页可存取模型配置 |
| **M1 对话内核** | llm 工厂 + Eino ADK Runner + 事件流推前端 | 能与模型对话，事件流渲染 |
| **M2 只读工具 + 项目理解** | read/list/search/git_diff + 索引器 + explorer Agent + 理解项目工作流 | FR-1 全部 |
| **M3 写操作 + 审批** | write/edit + ApprovalManager + 前端审批弹层 + diff 预览 | ✅ 完成（FR-2.2/2.3、FR-4.1、FR-4.3） |
| **M4 执行与修复** | shell/go_test/go_vet + 白名单 + fixer 循环 + 回滚 | ✅ 完成（FR-3.1/3.2/3.5、FR-4.2、FR-4.5 回滚 UI；FR-3.3/3.4 以提示词驱动修复循环实现，程序化 fixer 子 Agent 延后；FR-4.4 复用 ReviewPanel 实时 diff） |
| **M5 生产化** | DuckDB 持久化、任务管理、完整断点恢复、Token 统计、故障切换 | ✅ 完成（FR-5.1~5.4/5.6；存储引擎按用户要求改用 DuckDB；OTel OTLP 以 DuckDB 统计替代延后，FR-5.5 结构化日志已有） |
| **M6 增强（后置）** | tree-sitter 向量检索、MCP Client 接入外部工具、多 workspace 并行 | — |

---

## 7. 开放问题（需评审确认）

1. **模型供应商默认配置**：默认接哪个（DeepSeek / Qwen / GLM / OpenAI 兼容自填）？建议首版只做「OpenAI 兼容协议自填」，一个配置覆盖所有供应商。
2. **符号索引深度**：FR-1.4 的引用分析，MVP 用「gopls 优先 + 正则回退」还是纯 tree-sitter + 关键字？建议 MVP 用正则回退，M6 引入 gopls。
3. **多 Agent 粒度**：首版 3 个子 Agent（explorer/coder/fixer）是否足够？
4. **审批「会话记住」**：是否需要（同一命令模式本任务内不再询问）？建议需要，提升体验。
5. **数据库**：确认 SQLite（modernc 纯 Go）可接受。
