# ai-dog-agent

桌面端多 Agent 编码开发助手（Windows 优先）。

- **GUI**：Wails3 + React + TypeScript
- **Agent 内核**：Eino（ADK 多 Agent 编排 / Graph 工作流）
- **模型**：OpenAI 兼容协议自填（DeepSeek / Qwen / GLM / OpenAI 通用）
- **工具协议**：MCP Go SDK（stdio / HTTP / SSE）
- **语言无关**：任何语言/构建系统的项目都可使用；Go 为首个一等支持（go_test/go_vet/go_build 与 Go 符号索引）。非 Go 项目的构建/测试命令需加入 Shell 白名单（或逐次审批）
- **需求文档**：见 [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md)

## 架构要点

- **主 Agent（Orchestrator）**：常驻编排，制定计划、分派子 Agent；按项目 Agent 列表动态生成 `dispatch_<name>` 工具。
- **子 Agent**：配置随项目走（workspace 内 `.aidog/agents/*.yml`），独立上下文 / skill / MCP / function call / 模型；空工具默认只读。
- **执行上限**：步骤、Token、时间、工具次数、预算、重试六项上限，由 Agent Runtime 统一计数并以结构化原因终止。
- **项目级配置**：`.aidog/main.yml`（主 Agent 覆盖与项目 `agent_id`）、`mcp_servers.yml`、`skills.yml` 与全局配置在运行时合并，不写回全局。
- **后台 Meta Agent**：专职优化与创造子 Agent 配置（阶段 D，未实现）。
- **上下文前缀稳定**：系统提示词静态前缀在前、动态内容追加在后，保证提示词缓存命中。
- **会话持久化**：会话元数据与消息历史按会话落盘 `%AppData%/ai-dog-agent/sessions/*.json`，关闭软件后线程列表与对话记录可恢复，并在后续轮次回放历史。
- **配置**：设置页提供模型（OpenAI 兼容）、Skills、MCP Server 的可视化编辑；「项目设置」tab 统一编辑项目级子 Agent（`.aidog/agents/*.yml`，含预算与压缩）、主 Agent 覆盖、项目专属 MCP/Skill 与 `.aidog` 信任确认；启用项追加到主 Agent 动态上下文。
- **代码检索**：tree-sitter 多语言符号索引（Go / TypeScript / TSX / JavaScript / Python / Rust / Java / C / C++ / C# / Kotlin / Swift / Ruby / PHP / Bash）；`search_code` 命中附带所属符号，新增 `search_symbols` 工具；按文件 mtime 增量刷新。
- **Markdown**：助手回复支持 GFM 表格、代码块、列表（remark-gfm）。

## 开发

DuckDB 使用**动态库模式**（`-tags=duckdb_use_lib`），以便加载 vss 等扩展；
静态预构建库下 `INSTALL vss` 会原生崩溃。

`libs/` 目录（不入仓，可从发布包或 `go-duckdb-demo/libs` 复制）：

- `libs/duckdb-shared-libs-windows-amd64/`：`duckdb.dll` / `duckdb.lib` / `duckdb.h`
- `libs/duckdb-shared-libs-linux-amd64/`：`libduckdb.so` + 头文件
- `libs/duckdb-shared-libs-osx-amd64/`：`libduckdb.dylib` + 头文件

开发与构建：

```powershell
.\run-dev.ps1        # Windows 开发
.\build-windows.ps1  # Windows 构建
```

```bash
./run-dev.sh         # Linux/macOS 开发
./build.sh           # Linux/macOS 构建
```

`wails3 build` 的 Taskfile 已按平台设置 `CGO_CFLAGS` / `CGO_LDFLAGS` / `EXTRA_TAGS`，
并把 DuckDB 共享库复制到 `bin/`（macOS 同时复制进 `.app/Contents/MacOS`）。
运行可执行文件时共享库必须在同目录（或启动前已在 PATH / `LD_LIBRARY_PATH` /
`DYLD_LIBRARY_PATH` 中）。

DuckDB 使用动态库模式（`-tags=duckdb_use_lib`）。已配置 `GOFLAGS` 时可直接运行 Go 命令；
否则显式加标签，并设置与构建相同的 CGO 环境：

```bash
go test -tags duckdb_use_lib ./internal/...
```

新增依赖后可直接运行 `go mod tidy`。

## 里程碑

| 里程碑 | 状态 |
|---|---|
| M0 骨架（module/包结构/config/日志/前端布局） | 完成 |
| M1 对话内核（llm 工厂 + Eino ADK Runner + 事件流） | 完成 |
| M2 只读工具 + 项目理解（workspace 隔离 / explorer 子 Agent / go-ast 索引） | 完成 |
| M3 写操作 + 审批（write/edit + 审批卡片 + 备份回滚 API） | 完成 |
| M4 执行与修复（run_shell + go_test/vet/build + 白名单 + 回滚 UI） | 完成 |
| M5 生产化（DuckDB 持久化 + 任务面板 + 断点恢复 + Token 统计 + 模型故障切换） | 完成（OTLP tracing 延后，统计走 DuckDB） |
| M6 多 Agent 运行时（阶段 A–F：身份/模型路由/配置分层/Runtime 上限/work agent/动态分派/任务状态机/agent 私聊/agent-smith/上下文压缩/项目配置信任/可靠性收口） | 完成 |
