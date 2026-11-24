---
name: aidog-project-overview
description: ai-dog-agent 项目总览与架构地图（M0 骨架：服务装配、配置、日志、前端三栏布局、Wails 绑定生成、数据落盘位置）。在新会话冷启动了解本项目、需要定位文件/包、修改整体架构、新增功能、或需要知道配置/会话/日志/备份的落盘位置时使用本 skill。触发词：ai-dog-agent、项目结构、架构、装配、config、bindings、里程碑。
---

# ai-dog-agent 项目总览（M0 骨架）

桌面端多 Agent 编码助手（Windows 优先）：**Go 后端（Eino ADK 多 Agent 编排）+ Wails3 + React/TS 前端**。

## 里程碑状态（改功能前先看）

| 里程碑 | 内容 | 状态 |
|---|---|---|
| M0 | 骨架（装配/config/日志/前端布局） | ✅ 完成（本 skill） |
| M1 | 对话内核（llm 工厂 + ADK Runner + 事件流） | ✅ 完成 → `aidog-m1-chat-core` |
| M2 | 只读工具 + 项目理解（workspace/explorer/go-ast） | ✅ 完成 → `aidog-m2-read-tools` |
| M3 | 写操作 + 审批（write/edit + 备份回滚） | ✅ 完成 → `aidog-m3-write-approval` |
| M4 | 执行与修复（run_shell + go 工具 + 白名单） | ✅ 完成 → `aidog-m4-execution-fix` |
| M5 | 生产化（DuckDB 持久化/任务面板/断点恢复/Token 统计/故障切换） | ✅ 完成 → `aidog-m5-productionization` |

**项目惯例：每完成一个功能，必须在 `.codebuddy/skills/` 下新增对应的 `aidog-<功能名>/SKILL.md` 描述 skill，保持新会话可冷启动。**

## 包结构地图

```
main.go                  启动装配 + Wails Service 注册
internal/config/         配置管理（config.json 读写）
internal/logging/        slog 落盘日志
internal/workspace/      工作区根目录 + 路径安全解析
internal/eventbus/       进程内事件总线 → Wails 事件推送
internal/llm/            OpenAI 兼容模型工厂
internal/agent/          ChatService（对话核心）+ Agent 装配 + 会话持久化
internal/tools/          工具集（只读 fs/git、写操作 fs_write、shell 执行、go 构建工具）
internal/approval/       写操作审批门
internal/indexer/        go-ast 项目索引（符号/引用/入口）
internal/agentspec/      项目级子 Agent 配置（.aidog/agents/*.json）
internal/service/        Wails RPC Service 层（config/workspace/approval）
frontend/src/            React 三栏布局（Sidebar/ThreadPanel/ReviewPanel/SettingsPanel/ApprovalCard）
frontend/bindings/       自动生成的 TS 绑定（勿手改）
```

## 启动装配（main.go）

按顺序：`config.NewManager` → `logging.Setup(<configDir>/logs)` → `workspace.New()` → `eventbus.New()` + `bus.SetApp(app)` → `approval.New(bus)` → `agent.NewChatService(...)` → `application.NewService(...)` 注册 4 个 Service → `application.RegisterEvent[eventbus.Event](eventbus.EventName)`（事件强类型绑定的关键一行）。`backupRoot` 与 session 目录由 main 组装后注入。

## 配置（internal/config/config.go）

`Config`：`Models []ModelConfig`（BaseURL/APIKey/Model/PriceIn/PriceOut）、`ActiveModelID`、`WorkspacePath`、`Skills`、`MCPServers`、`ShellWhitelist`（空时回退默认 go/git/dir/ls）。JSON 落盘 `%AppData%/ai-dog-agent/config.json`（tmp+rename 原子写）。`Manager.Get/Save/ActiveModel/ShellWhitelist`；`ActiveModel()` 是构建模型的前置校验。

## 前端布局（frontend/src/）

`App.tsx` 组装三栏：`Sidebar`（项目+线程列表+⚙ 设置入口 `side-footer`）+ `ThreadPanel`（对话流+Composer 输入框）+ `ReviewPanel`（Diff/文件审查）；设置弹层 `overlay` 内嵌 `SettingsPanel`。事件经 `Events.On("agent:event")` 消费。样式在 `src/styles.css`——三栏骨架依赖 `min-height:0` 约束链，改动布局时保持 `.app`(grid) → `.thread-col/.thread` → `.chat-messages` 的收缩链完整，否则长内容会把底部 Composer/设置按钮顶出视口。

**无边框窗口（M5 追加）**：main.go 窗口选项 `Frameless: true`（无系统标题栏/边框）；顶部 36px 标题栏为跨三列的 grid 首行，由 `frontend/src/components/TitleBar.tsx` 提供——整条 `--wails-draggable: drag` 拖拽区，右侧最小化/最大化/关闭按钮 `no-drag`，走 `@wailsio/runtime` 的 `Window.Minimise/Maximise/Restore/Close`。改动标题栏后需同步 `styles.css` 的 `.titlebar/.win-btn` 与 `.app` 的 `grid-template-rows: 36px ...`（内容行高 = 100vh - 36px，收缩链从 `.app` 首行之后开始）。

## 绑定（frontend/bindings/）

由 wails3 CLI 从 Go Service 生成，镜像包结构（`internal/agent/chatservice.ts`、`internal/service/*.ts`、`events/index.ts`）。**勿手改**；改了 Go Service 后重新生成（见 `wails3` skill 的 `references/bindings.md`）。

## 数据落盘位置

| 数据 | 路径 |
|---|---|
| 应用配置 | `%AppData%/ai-dog-agent/config.json` |
| 日志 | `%AppData%/ai-dog-agent/logs/` |
| 会话（含消息历史） | `%AppData%/ai-dog-agent/sessions/<sessionId>.json` |
| 写操作备份 | `%AppData%/ai-dog-agent/backups/<相对路径镜像>` |
| 项目子 Agent 配置 | `<workspace>/.aidog/agents/*.json` |

## 开发与调试

```bash
wails3 dev              # 开发调试（最小参数形式，Taskfile 已含全部配置）
wails3 build            # 生产构建
go test ./internal/...  # Go 测试
```

前端/界面改动验证：用 mcp-wails3-dev MCP 工具（`app_info`/`screenshot_dom`/`dom_query`/`js_eval`）对运行中的应用实测，不要只跑 build。
