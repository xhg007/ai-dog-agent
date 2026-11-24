---
name: aidog-m2-read-tools
description: ai-dog-agent 只读工具与项目理解（M2）：workspace 路径安全隔离、只读工具集（read_file/list_files/search_code/git_diff/git_status/project_summary）、explorer 只读子 Agent 与 dispatch_explorer 分派、go-ast 项目索引（符号/引用/入口）、项目级子 Agent 配置（.aidog/agents/*.json）。在新增工具、修改工具参数、改动路径安全策略、扩展 explorer 或子 Agent 配置时使用。触发词：ReadOnlyToolset、dispatch_explorer、workspace.Resolve、InferTool、indexer、agentspec。
---

# 只读工具与项目理解（M2）

## Workspace 隔离（internal/workspace/workspace.go）

`Manager.SetRoot/Root/IsSet/Resolve`；`Resolve` 是所有文件工具的**唯一入口**：拒绝 `..` 逃逸、越界绝对路径、符号链接逃逸（错误：`ErrNoWorkspace`、`ErrOutsideWorkspace`）。新增文件类工具必须经 `Resolve` 取真实路径。

`WorkspaceService.SetWorkspace`（internal/service/workspace_service.go）设置根目录并持久化到 config。

## 只读工具集（internal/tools/）

- `registry.go`：`ReadOnlyToolset(ws)` 装配免审批工具；参数结构体（`readArgs/listArgs/searchArgs/NoArgs`）经 `utils.InferTool` 自动生成 JSON Schema。
- `fs.go`：`ReadFile` / `ListFiles` / `SearchCode`。
- `git.go`：`GitDiff` / `GitStatus`（调 git 命令，供 Agent 与审查面板共用）。

新增只读工具的步骤：定义参数结构体 → `utils.InferTool(name, desc, struct, impl)` → 加入 `ReadOnlyToolset` → 补测试。

## explorer 子 Agent（internal/agent/assembly.go）

`BuildExplorer(ctx, ws, cm)`：独立上下文的只读探索子 Agent，工具 = `ReadOnlyToolset` + `project_summary`（内部调 `BuildProjectContext` 生成 go.mod/入口/README/符号概览）。提示词稳定前缀 `ExplorerInstruction`（internal/agent/instruction.go）。

主 Agent（`BuildOrchestrator`）经 `dispatch_explorer` 工具（`adk.NewAgentTool` 包装）分派 explorer；子 Agent 内部工具事件经 `EmitInternalEvents: true` 转发到主事件流。`retryConfig`：3 次指数退避。

## go-ast 索引（internal/indexer/indexer.go）

标准库 go/parser（tree-sitter 延后 M6）：`ParseGoMod`→`ModuleInfo`、`FindEntryPoints`→`EntryPoint`、`ScanSymbols`→`Symbol`（func/method/struct/interface/type）、`FindReferences`→`Reference`（词边界正则）。`ignoreDirs` 排除 vendor 等。

## 项目级子 Agent 配置（internal/agentspec/agentspec.go）

`Spec`（agent_id/name/system_prompt/tools/skills/mcp_servers/max_turns）读写 `<workspace>/.aidog/agents/*.json`，`Save/List/Delete/Dir` + `validate`。RPC 在 `ConfigService.GetProjectAgents/SaveProjectAgent/DeleteProjectAgent`；启用的 spec 摘要经 `ChatService.buildDynamicExtras` 追加进主 Agent 提示词尾部。

**稳定 agent_id（M5 追加，agent_id.go）**：每个配置文件含唯一不变 `agent_id`（32 位无连字符 UUID）。`Save` 缺省时自动生成固化，**已有同名配置沿用其 ID**（前端编辑不回传 ID 也不换）；`List` 对手工配置缺失项自动迁移写回。项目主 Agent ID 固化在 `<workspace>/.aidog/agent_id`（`EnsureAgentID`/`EnsureIDInFile(path)` 通用读取或生成）。

## 修改时的注意点

- 工具返回内容直接进入对话上下文，注意截断（如 search_code 结果上限）。
- explorer 是纯只读的——任何写能力都应走 M3 的审批工具集，不要加进 `ReadOnlyToolset`。
- 子 Agent 配置随 workspace 走（不在全局 config），切换 workspace 后列表会变。
