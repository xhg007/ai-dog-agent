---
name: aidog-m4-execution-fix
description: ai-dog-agent M4「执行与修复」：run_shell 白名单执行内核、go_test/go_vet/go_build 工具、失败用例提取、修复循环提示词、Shell 白名单配置与设置页、失败回滚按钮。在修改命令执行、白名单、Go 构建工具、工具失败返回策略、设置页安全 Tab 或审查面板回滚时使用。触发词：run_shell、ShellRun、go_test、go_vet、go_build、白名单、ShellWhitelist、修复循环、回滚。
---

# M4：执行与修复（run_shell + go 工具 + 白名单 + 回滚）

## 核心文件

- `internal/tools/shell.go` — `ShellRun(ws, whitelist, appr, sessionID, command)`（分词→白名单/审批判定→`runCommand`）；`runCommand(ws, prog, args)`（并发信号量 1、超时 `shellTimeout=120s` 可测注入、输出截断 `MaxShellOutput=64KB`）；`classifyCommand`；`truncateOutput`
- `internal/tools/gotool.go` — `GoToolset(ws)` 注册 `go_test`/`go_vet`/`go_build`（`utils.InferTool`）；`resolveGoTarget`（dir 非空 → `filepath.Join(abs,"...")` 递归子包）；`augmentTestFailures`（`--- FAIL:` 提取）；`outOrEmpty`/`itoa`
- `internal/config/config.go` — `Config.ShellWhitelist`、`DefaultShellWhitelist()`（go/git/dir/ls）、`Manager.ShellWhitelist()`（空则回退默认）、clone 深拷贝
- `internal/service/config_service.go` — `GetConfig` 对空 ShellWhitelist 填充默认值（前端设置页展示生效值的关键）
- `internal/agent/assembly.go` — `OrchestratorTools`/`BuildOrchestrator` 多了 `whitelist []string` 参数（`extras` 变参保持最后）；注册 `run_shell`
- `internal/agent/instruction.go` — 稳定前缀含「# 执行与修复循环」段（最多 3 轮，超限输出未解决清单）
- `internal/agent/chat.go` — `initAgentLocked` 传 `s.cfg.ShellWhitelist()`
- 前端：`SettingsPanel.tsx`（「安全」Tab 白名单增删保存）、`ReviewPanel.tsx`（file-item `↩ 回滚` → `ApprovalService.RollbackFile`）、`ThreadPanel.tsx`（toolIcons 含 run_shell/go_test/go_vet/go_build/write_file/edit_file）、`styles.css`（`.whitelist-row`/`.rollback-btn`）

## 安全模型（spec 3.3）

- 命令不经 cmd/PowerShell：`exec.CommandContext(prog, args...)`，分隔符（&&/;|）只是普通参数，无注入面。
- 白名单匹配按程序名；go 仅放行 build/test/vet/fmt/version，git 仅放行 status/diff/log/show/branch（复用 git.go 只读表），**其余子命令即使程序在白名单也强制审批**（git push / go install 等）。
- 白名单外 + `appr==nil` → 直接拒绝；`appr!=nil` → `appr.Request(sessionID,"run_shell",command,preview)` 阻塞等审批。
- 并发 1（`shellSem`）、超时杀进程、64KB 截断。

## 关键设计：工具失败 ≠ 工具错误（重要）

`runCommand` 返回 `(text, exitCode, err)` 三元组：**非零退出码（编译/测试失败）是预期业务结果**，err 为 nil；只有启动失败/超时才 err。go_test/go_vet/go_build 把失败作为**普通文本结果**返回（如「测试未通过…\n失败用例: TestX」），绝不能返回 error——error 会让 eino ADK 抛 `NodeRunError` 直接中断整轮对话，模型无法继续修复循环。`ShellRun`（run_shell）保留非零退出→error 的语义。

## 已知坑

- `go test <dir>` 不递归子目录（父目录无 .go 文件时报 "no Go files"），必须 `<abs>\...`。
- Windows 下 `strings.Fields` 分词对含空格路径不安全——GoToolset 绕过 ShellRun 直传参数数组给 runCommand。
- 会话 Agent 在首条消息时惰性构建（`initAgentLocked`）：改白名单/提示词后**新建会话**才生效；旧会话复用已构建的 Agent。
- **deepseek-flash 模型端 function calling 不稳定**：会把工具调用输出为文本（DSML 标记 `<｜DSML｜>` 泄漏），`dispatch_explorer` 等旧工具同样受影响——模型供应商行为，非本项目缺陷；建议换 deepseek-chat 验证端到端工具调用。
- `wails3 generate bindings` 重新生成期间 Vite 会瞬时报 "Failed to load url /bindings/..."（先删后写窗口），且 Go watcher 热重启中断运行中会话；等 dev 重启完成后刷新即可，勿误判为 bindings 缺失。

## 验证方式

- 单元测试：`go test ./internal/tools/ -run "ShellRun|Truncate|GoTest|GoVet"`（含审批放行/拒绝、git 写子命令审批、超时、无 shell 解释、dir 越界、递归子包、失败摘要）。
- 端到端 UI：`wails3 dev`（WAILS_MCP=1）+ mcp-wails3-dev MCP——设置页「安全」Tab 实测增删保存持久化；对话触发 go_test（注意上面的模型端问题）。
