# 后端加固与全 API 适配 · 设计说明

日期：2026-11-25
状态：已实现（未提交）

## 意图与原因

`ai-dog-agent` 的后端（Wails3 Services + Eino 编排 + DuckDB 持久层）功能完整，但存在几类会在真实使用中暴露的问题：同一会话并发发送可双执行、用户中断后审批/子进程不释放、写新文件可经符号链接目录逃逸 workspace、备份/回滚路径未规范化、配置可写入不可用状态、`TaskService` 返回 `any` 导致前端类型靠手写。

本次改动目标：**先让后端行为可预期，再让前后端 API 面完整对齐**，不引入新依赖、不改产品形态。

## 目标 / 非目标

- 目标：消除并发与取消语义缺陷；收紧密闭路径安全；RPC 返回强类型；前端消费全部已暴露 RPC。
- 非目标：不改 Agent 编排策略、不接 MCP 客户端、不重做会话存储格式、不改 UI 视觉。

## 关键决策

| 决策 | 取舍 |
|---|---|
| `SendMessage` 用 `claimSession` 在同一临界区完成「检查 + 占用」 | 拒绝「先解锁再构建 Agent」的写法；构建失败必须 `releaseSession` 复位 |
| 审批新增 `RequestContext(ctx)`，保留 `Request` 作为 `context.Background()` 包装 | 旧测试与调用方零改动；工具链路统一透传 ctx |
| `CancelSession(sessionID)` 以「未批准」决策唤醒等待方，由等待方清理 pending | 不直接删 pending，保证 `approval_decided` 事件一定发出、前端卡片移除 |
| `Resolve` 对不存在目标解析「最近已存在祖先」 | 保留「目标不存在也能解析」的能力，同时堵住 symlink 目录 + 新文件名逃逸 |
| 备份/回滚统一使用 workspace 相对路径 + `withinDir` 校验 | 绝对路径入参不再产生畸形备份路径；回滚只读 `backupDir` 内文件 |
| `Task.FinishedAt` 改为 `*time.Time` | 运行中 JSON 省略该字段，不再输出 `0001-01-01T00:00:00Z` |
| `TaskService` 三个方法返回具体类型 | bindings 生成 `store.Task[]` 等，前端删除手写 `TaskInfo` 结构 |
| 新增 `ConfigService.SetActiveModel` | 前端切模型不再「读整份配置 → 覆盖保存」，避免丢失并发修改 |
| 删除 `EnsureSession`、`RestoreSessions` 转私有 | 二者前端从未调用；减少无效 RPC 面 |

## 实现摘要

- `internal/agent/chat.go`：`claimSession` / `releaseSession`；`DeleteSession` 撤销会话挂起审批。
- `internal/approval/approval.go`：`RequestContext`、`CancelSession`。
- `internal/tools/{fs_write,shell,gotool}.go`、`internal/agent/assembly.go`：审批与子进程接收 ctx；父 ctx 取消时返回「命令已中断」并 kill 子进程。
- `internal/workspace/workspace.go`：`resolveExistingAncestor` + `within(root)` 校验。
- `internal/config/config.go`：`validateConfig`（模型字段/ID 唯一/ActiveModelID 有效/白名单无空项）。
- `internal/store/{store,duckdb}.go`、`internal/service/task_service.go`、`internal/service/config_service.go`：强类型与 `SetActiveModel`。
- 前端：`TaskPanel`（强类型、新任务即时出现、事件流展开）、`ThreadPanel`/`SettingsPanel`（`SetActiveModel`）、`styles.css`（`.task-events`）。

## 验证

- `go vet ./internal/...`、`go build ./internal/... .`：通过。
- `go test ./internal/... -count=1`：通过。
- 关键包 `-race`：agent / tools / approval / workspace / config / task / store 全部通过。
- `wails3 generate bindings -ts`：5 Services / 26 Methods；`npx tsc --noEmit` 无错误。
- 新增回归测试：并发占用唯一性、审批 ctx 取消与按会话撤销、symlink 目录逃逸、备份/回滚路径、配置校验、`finishedAt` JSON 语义。

## 未做与建议（按优先级）

1. **模型请求无超时**：`llm.BuildModel` 的 `Timeout: 0`，网络挂起时只能手动中断。建议给 ChatModel 加可配置超时（需兼顾长流式输出）。
2. **DuckDB 读不加锁**：写入经 `duckdbMu` 串行，查询直连连接池；单文件并发读写偶发冲突时仅日志告警。建议读也走同一限流器或改用只读连接。
3. **会话 JSON 全量重写**：每轮 `persistLocked` 重写整个会话文件，长会话 O(n²)。建议改追加式存储或按轮分片。
4. **MCP 只有配置没有客户端**：动态上下文里已声明「M6 提供」，当前 MCP 配置不会真正生效。
5. **`ExtraFields` 注入 `user_id`**：严格 OpenAI 端点可能以「未知参数」返回 400，建议按供应商开关。
6. **模型切换对已建 Agent 不生效**：仅新会话生效，前端可加提示或提供「重建会话」入口。
7. **`run_shell` 用 `strings.Fields` 分词**：带空格路径无法执行，建议文档明确或支持引号解析。

---

# 第二轮（同日）：MCP 接入与运行时改进

## 为什么

上一轮列出的 7 条建议全部落地。核心变化是 MCP 从「只写进上下文」变成真正的客户端：
用官方 `github.com/modelcontextprotocol/go-sdk` v1.8.0 启动 stdio 子进程、发现工具并交给主 Agent。

## 关键决策

| 决策 | 取舍 |
|---|---|
| 官方 MCP Go SDK（v1.8.0） | 协议权威、v1 稳定；代价是 vendor 体积增加 |
| 工具名 `mcp_<server>_<tool>` | 避免与内置工具冲突；服务名/工具名做字符清洗 |
| MCP 工具每次调用都走审批门 | 能力未知，默认不自动执行；按 `mcp:<serverID>` + 工具名可「批准并记住」 |
| MCP 连接懒建立、按配置 ID 复用 | 会话构建时才启动子进程；命令/参数/环境变化自动重连；失败只跳过该 server |
| `MCPService.TestConnection(config)` 接收配置而非已保存 ID | 设置页可在保存前试连 |
| 模型超时默认 300s、`TimeoutSeconds` 可覆盖 | 解决网络挂起无限等待；长流式输出可通过配置放大 |
| `DisableExtraFields` 开关 | 严格 OpenAI 端点不再被 `agent_id/user_id` 顶回 400 |
| `config.Manager.Version()` + 会话版本比对 | 配置保存后**当前会话下一轮**重建 Agent，模型/MCP/Skills/白名单立即生效 |
| DuckDB `SetMaxOpenConns(1)` | 单文件串行化，消除并发读写事务冲突；桌面单用户可接受 |
| 会话存储 `meta.json` + `messages.jsonl` | 正常轮次 O(新增条数) 追加；旧 `<id>.json` 自动迁移（先写成功再删） |
| `splitCommand` 引号分词 | 支持带空格路径；反斜杠按字面保留（Windows 友好） |

## 实现位置

- `internal/mcp/manager.go`：连接、ListTools、Eino `InvokableTool` 包装、审批、结果文本化、`TestConnection`、`Close`。
- `internal/service/mcp_service.go`：`TestConnection` RPC（30s 超时）。
- `internal/agent/{assembly,chat}.go`：MCP 工具注入 orchestrator；配置版本变化重建 Agent。
- `internal/config/config.go`：`TimeoutSeconds`、`DisableExtraFields`、`Version()`。
- `internal/llm/factory.go`：`modelTimeout` / `extraFields`。
- `internal/store/duckdb.go`：`SetMaxOpenConns(1)`。
- `internal/tools/shell.go`：`splitCommand`。
- `internal/agent/store.go`：目录式会话存储与旧格式迁移。
- 前端：`SettingsPanel`（MCP 测试连接、模型超时/agent_id 开关）、`ThreadPanel` 文案。

## 验证

- `go vet ./internal/... . && go build ./internal/... . && go test ./internal/... -count=1`：通过。
- 关键包 `-race`（agent / mcp / tools / approval / workspace / config / task / store）：通过。
- MCP 测试包含 in-memory 端到端与真实 stdio 子进程端到端（`TestMain` 兼作 helper）。
- `wails3 generate bindings -ts`：6 Services / 27 Methods；`npx tsc --noEmit` 无错误。
- 新增测试：MCP 审批/拒绝/无通道/非法参数/降级、超时与扩展字段、DuckDB 并发读写、引号分词、配置版本、会话增量追加/替换/迁移。

## 剩余建议

1. **MCP 远程 transport**：当前仅 stdio；HTTP/SSE 可用 go-sdk 的 `StreamableClientTransport` 补齐。
2. **MCP 工具输出截断**：目前全量回传模型，大输出可能撑爆上下文，建议按字节截断并提示。
3. **MCP 连接健康检查**：子进程崩溃后无自动重连；可在调用失败时失效缓存并重试一次。
4. **MCP 审批粒度**：可按 tool annotations（readOnlyHint）对只读工具免审批，减少打扰。
5. **迁移不可逆**：旧会话迁移后删除原 JSON，无回滚；如需保守可改为保留 `.json.bak`。
6. **模型超时与长流式**：300s 默认对超长输出可能偏短，建议在设置里按模型调大。
7. **DuckDB 串行化**：长查询会阻塞写；数据量变大后可拆分只读连接或迁移到嵌入式分析库之外。
---

# 第三轮（同日）：MCP 远程连接与安全策略

## 为什么

MCP 客户端此前只支持 stdio，且所有工具一律审批、结果不截断、连接失效后不恢复。
本轮补齐远程连接（Streamable HTTP / 旧版 SSE）、只读工具免审批开关、结果截断、失败重连、
工具表刷新，以及旧会话迁移的可回滚备份。

## 关键决策

| 决策 | 取舍 |
|---|---|
| `MCPServerConfig` 增加 `transport`/`url`/`headers`/`autoApproveReadOnly` | 旧配置缺省 `transport=auto`，按 command 是否为空推断，零迁移成本 |
| 远程 transport 用 SDK 的 `StreamableClientTransport` / `SSEClientTransport` | 不引入新依赖；Headers 经自定义 `http.RoundTripper` 注入，支持 `${ENV}` 展开 |
| Headers 错误只报行号 | 避免把可能含密钥的值写进错误/日志 |
| 只读免审批按 server 开关，默认关 | `readOnlyHint` 来自服务器、不可完全信任；只对声明只读的工具跳过审批 |
| 结果按 64KB 截断（UTF-8 安全） | 与 `run_shell` 的输出上限一致，避免大结果撑爆上下文 |
| 调用失败重连并重试一次 | 子进程退出/远程断开后可自愈；重试仍失败返回原错误 |
| 每次 `Tools()` 重新 `ListTools` | 连接可能长期存活，服务器可增删工具；失败沿用旧表 |
| 旧会话迁移保留 `<id>.json.bak` | 迁移不可逆但可人工回滚；删除会话时一并清理 |

## 实现位置

- `internal/config/config.go`：MCP 字段、`validateConfig`（stdio/http/sse 必填项、headers 格式）。
- `internal/mcp/manager.go`：`transportFor`、`dial`（三种 transport）、`headersClient`/`parseHeaders`、
  `refreshTools`、`reconnect`、`truncateResult`、只读审批门、`sameServer` 覆盖远程参数。
- `internal/agent/store.go`：迁移成功后 `backupLegacy` 重命名为 `.bak`，`delete` 一并清理。
- `frontend/src/components/SettingsPanel.tsx`：transport 下拉、URL/Headers、只读免审批开关、测试连接透传。

## 验证

- `go vet ./internal/... . && go build ./internal/... . && go test ./internal/... -count=1`：通过。
- 关键包 `-race`（agent / mcp / tools / approval / workspace / config / task / store）：通过。
- MCP 测试新增：transport 推断、headers 解析、截断边界（UTF-8）、工具表刷新、
  关闭 session 后自动重连重试、只读免审批四种组合。
- 会话存储新增：迁移保留 `.bak`、删除会话清理 `.bak`。
- `wails3 generate bindings -ts`（6 Services / 27 Methods）+ `npx tsc --noEmit`：通过。

## 未验证与剩余建议

1. **远程 transport 未接真实远程 MCP Server 联调**：仅验证了 transport 选择、headers 构造与 stdio 端到端；
   HTTP/SSE 的握手、鉴权与断线行为需要在真实服务上跑一次。
2. **HTTP OAuth**：SDK 支持 OAuth，但当前配置只提供静态 Headers；需要时再加。
3. **只读注解的信任边界**：`autoApproveReadOnly` 默认关，开启后仍建议配合可信 Server 使用。
4. **重连并发**：多个工具同时失败会各自触发 reconnect；可加 singleflight 合并。
5. **`.bak` 无清理策略**：长期使用会累积旧备份，可加保留期限或数量上限。
6. **MCP 工具 schema 缓存**：每次装配重新拉取并解析，工具很多时可考虑缓存 schema。