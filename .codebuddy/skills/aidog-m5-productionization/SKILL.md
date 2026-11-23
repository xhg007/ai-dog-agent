---
name: aidog-m5-productionization
description: ai-dog-agent M5「生产化」：DuckDB 持久层（tasks/task_events/token_usage/checkpoints）、任务生命周期与事件流扩展、eino callbacks 观测（LLM 用量/工具检查点）、断点级中断恢复（RecoverTask）、模型故障切换、任务面板与用量报表前端。在修改任务/统计/恢复/fallback、DuckDB 表结构、callbacks 织入或任务面板 UI 时使用。触发词：task_state、llm_usage、model_fallback、checkpoints、RecoverTask、stats.duckdb、TaskPanel、UsageTab、fallbackChatModel。
---

# M5：生产化（持久化 + 任务面板 + 断点恢复 + 用量统计 + 故障切换）

## 核心文件

- `internal/store/store.go` — `Store` 接口 + `Task/TaskEvent/TokenUsage/Checkpoint/DailyUsageRow` 结构（JSON tags 供前端）
- `internal/store/duckdb.go` — DuckDB 实现：`Open(path)` 建表（tasks/task_events/token_usage/checkpoints + 序列 + 索引）；写操作经 `chanLimiter` 互斥；`DailyUsage` 用 `strftime` + `INTERVAL (n) DAY` 聚合
- `internal/store/writer.go` — `AsyncWriter`：单 goroutine 异步写、队列满丢弃告警、`Close` **幂等**（closeOnce）且排空
- `internal/store/emutlfix/fix.go` — **Windows 关键补丁**：补齐 libduckdb 预构建库缺失的 emutls 控制变量（详见「已知坑」）
- `internal/task/manager.go` — `Manager`：`Start/Finish`（同步写 + EvTaskState + 事件落表）、`Emit`（落库+推送）、`AppendTokenUsage/AppendCheckpoint`（异步）、`NextCheckpointSeq`、读透传（GetTask/CheckpointBySeq/ListTasks/TaskEvents/DailyUsage）
- `internal/agent/callback.go` — **全局 eino callbacks.Handler**：ChatModel OnEnd/流式结束采集 TokenUsage → token_usage 落库 + `llm_usage` 事件；Tool OnStart → before checkpoint（含消息快照）+ `tool_call` 事件；Tool OnEnd → after checkpoint
- `internal/agent/recovery.go` — `RecoverTask`：最后 after 检查点 + before 快照重建历史，工具结果以 **user 续跑指令**注入（不伪造 tool role），恢复轮次走新任务生命周期
- `internal/llm/fallback.go` — `FallbackModel` 装饰器 + `BuildModelChain`：激活模型优先、配置顺序备用、瞬态错误切换
- `internal/service/task_service.go` — `TaskService`：ListTasks/GetTaskEvents/GetDailyUsage
- 前端：`TaskPanel.tsx`（任务列表/task_state 实时合并/恢复按钮/跳线程）、`Sidebar.tsx`（线程/任务 Tab）、`SettingsPanel.tsx`（用量 Tab + UsageTab 子组件）、`ThreadPanel.tsx`（model_fallback → system 消息）
- 落盘：`%AppData%/ai-dog-agent/stats.duckdb`（会话仍 JSON 在 sessions/）

## 任务状态机

`running` → `completed`（正常结束）/ `failed`（Agent 错误）/ `interrupted`（用户中断，含「ctx 已取消但 iter 正常 EOF」分支）。中断/失败后可由 `ChatService.RecoverTask(taskID)` 从最后 after 检查点重建历史续跑（新任务记录，原任务保持终态）。

## 关键设计

- **callbacks 注册**：`RegisterObsHandler(tasks, priceFn)` → `callbacks.AppendGlobalHandlers`，仅在 `NewChatService` 且 tasks != nil 时注册一次（测试避免重复注册）。价格经 `atomic.Value` 注入 `PriceFunc`（元/1M tokens，取自激活模型 PriceIn/PriceOut）。
- **用量按调用粒度**：runTurn 不再按轮聚合记账（曾短暂存在后移除，避免与 callback 重复计量）；`chat_done` 仍带 usage 供对话 UI。
- **checkpoint 快照**：`runTurn` 把「本轮初始消息历史 JSON」放入 ctx（`obsInfo`），Tool OnStart 写入 before.Messages；恢复时以 before.Messages 为历史基底。
- **恢复不伪造 tool role**：tools 消息需 ToolCallID 严格匹配，伪造易 400；改以 user 消息携带「工具 X（参数）的结果：…，请继续」。
- **故障切换**：`IsTransientError` 关键词匹配（429/5xx/timeout/dial/connection/EOF 等）；4xx 配置错误不切换；切换发 `model_fallback` 事件（前端渲染 system 提示）。`BindTools` 签名**无 ctx**（eino 接口）。

## 已知坑（重要）

- **DuckDB + MSYS2 GCC16（Windows）链接**：duckdb-go-bindings 预构建库构建于旧版 GCC，引用 `__emutls_v._ZSt11__once_call`/`_ZSt15__once_callable`（libstdc++ call_once 的 emutls 控制块），而 GCC16 的 libstdc++ 改用原生 TLS、libgcc 移除 emutls runtime → undefined reference。**解法：`internal/store/emutlfix` 用 cgo 定义两个 `__emutls_object{size=8,align=8,index=0,templ=NULL}` 控制变量**（`__asm__` 重命名符号），`__emutls_get_address` 由 duckdb 库自带。另：MSYS2 的 `ld.lld.exe` 是 COFF flavor，不认 Go 链接驱动的 `-Wl,-T`，不要走 lld 路线。
- **wails3 dev/build 必须 `CGO_ENABLED=1`**：`go.duckdb.org/libduckdb/windows-amd64/prebuilt.go` 是 `import "C"` 文件，CGO 关闭时报 "build constraints exclude all Go files"。后台启动 dev 时要显式 `$env:CGO_ENABLED="1"`。
- **`HandlerBuilder.OnEndFn` 后设覆盖先设**：ChatModel 与 Tool 的 OnEnd 必须合并到一个 fn（用 `info.Component` 分流），否则先前的静默失效。
- **`model.ConvCallbackOutput`/`tool.ConvCallbackInput` 只认指针或 string**：传值结构体返回 nil（症状：用量/参数为空但不报错）。真实组件实现传 `*model.CallbackOutput` / string。
- **流式 handler 可直接同步消费**：框架 `OnWithStreamHandle` 已为每个 handler `Copy` 独立副本（`cpy(len+1)`），无需自行 Copy/goroutine。
- **`AsyncWriter.Close` 必须幂等**：测试/装配双重 defer 会 close of closed channel panic。
- **`TaskService.ListTasks` 空结果序列化为 null**：前端需 `?? []` 兜底。
- `wails3 generate bindings` 期间 Vite 瞬时报 `/bindings/...` 加载失败；多次热重启后 Vite HMR 缓存可能错乱（页面 root 空 + "Failed to fetch dynamically imported module"）→ 重启 dev（并清理残留 node/vite 进程占用的 9245 端口）。
- deepseek-flash 的 DSML 文本化问题见 `aidog-m4-execution-fix`，与 M5 无关但端到端验证时会遇到（任务/用量记录不受影响）。

## 验证方式

- 单测：`go test ./internal/store/ ./internal/task/ ./internal/agent/ ./internal/llm/`（DuckDB 建表/CRUD/聚合、任务生命周期、handler 用量与检查点、恢复消息重建、fallback 分类与切换）
- 端到端（mcp-wails3-dev）：侧栏「任务」Tab 空态→发消息→任务行 `完成` 徽标实时出现；设置页「用量」Tab 显示日汇总 + 总计；`TaskService.GetDailyUsage` 返回真实 tokens。
