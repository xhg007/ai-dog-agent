# M4 执行与修复 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付 FR-3（go_test/go_vet/go_build + run_shell + 输出保护）与 FR-4.2/4.5（Shell 白名单 + 失败回滚 UI），主 Agent 具备「测试失败 → 分析 → 修复 → 重测」能力。

**Architecture:** 新增 `internal/tools/shell.go`（命令执行内核：白名单校验 + 审批 + 超时/截断/并发保护）与 `internal/tools/gotool.go`（Go 三件套，白名单内免审批，复用 shell 内核）；白名单进 `config.Config`；装配层把工具接入主 Agent 并更新稳定前缀提示词（修复循环由模型在对话内驱动，上限 3 轮——程序化 fixer 子 Agent 延后，避免与现有 ChatService 执行模型冲突）；前端补工具图标、白名单编辑、单文件回滚按钮（复用已有 `ApprovalService.RollbackFile`）。

**Tech Stack:** Go 1.26 + Eino ADK（`utils.InferTool`）+ Wails3 Service + React/TS。

**Spec:** `docs/REQUIREMENTS.md` FR-3.1~3.5、FR-4.2、FR-4.5（3.3 安全模型）。

## Global Constraints

- 提示词稳定前缀不可插入动态内容（`internal/agent/instruction.go` 注释约定）；修复轮数写死 3（spec 默认值），`MaxFixRounds` 可配置项延后 M5（YAGNI）。
- 所有文件/命令操作 cwd=workspace；路径经 `workspace.Manager.Resolve`（FR-4.1）。
- Shell 不经 cmd/PowerShell 解释：`exec.CommandContext(prog, args...)`，无注入面（同 `git.go` GitRun 模式）。
- 默认白名单：`go`(build/test/vet/fmt)、`git`(status/diff/log/show/branch 只读)、`dir`、`ls`；白名单外与 git 写子命令（push/commit/reset/restore/checkout/merge/rebase）一律审批（spec 3.3）。
- 命令超时 120s、输出截断 64KB、并发上限 1（FR-3.5，spec 固定值）。
- 工具注册一律 `utils.InferTool`，返回 `[]tool.BaseTool`，InferTool 错误按 `registry.go` 现有模式处理。
- 测试：`go test ./internal/...` 全绿；前端 `pnpm build` 通过；界面验证用 mcp-wails3-dev（WAILS_MCP=1 的 `wails3 dev`）。
- 不主动 git commit（项目尚无提交基线，用户未要求）。

## Review Focus

1. **命令分隔符**：`command` 含 `&&`/`;`/`|` —— 不经 shell，分隔符只是普通参数；期望程序报参数错误而非执行拼接命令。测试：Task 1 `TestShellRun_NoShellInterpretation`。
2. **dir 参数越界**：`go_test{dir:"../other"}` —— 期望 `ErrOutsideWorkspace`。测试：Task 2 `TestGoTest_RejectsOutsideDir`。
3. **挂起命令**：期望 120s（测试中缩短为 100ms）超时终止并返回错误。测试：Task 1 `TestShellRun_Timeout`。
4. **超大输出**：期望截断至 ≤64KB 并附截断提示。测试：Task 1 `TestTruncateOutput`。
5. **git 写子命令误放行**：`git push` —— 期望触发审批（白名单内 git 仅限只读子命令）。测试：Task 1 `TestShellRun_GitWriteRequiresApproval`。

---

### Task 1: Shell 执行内核

**Files:**
- Create: `internal/tools/shell.go`
- Test: `internal/tools/shell_test.go`

**Interfaces:**
- Consumes: `workspace.Manager.Resolve/Root/IsSet`、`approval.Manager.Request(sessionID, tool, path, preview) (bool, error)`
- Produces:
  - `func ShellRun(ws *workspace.Manager, whitelist []string, appr *approval.Manager, sessionID, command string) (string, error)`
  - `func truncateOutput(s string, max int) string`（纯函数）
  - 包级可测变量 `var shellTimeout = 120 * time.Second`（测试改短）；并发信号量 `var shellSem chan struct{}`（容量 1）
  - 白名单判定：`go` 限子命令 build/test/vet/fmt/version；`git` 限只读子命令（status/diff/log/show/branch），写子命令强制审批；`dir`/`ls` 及用户自定义白名单条目整体放行

- [ ] **Step 1: 写失败测试**（表驱动，参考 `internal/tools/fs_write_test.go` 与 `internal/approval/approval_test.go` 的 bus/approval 构造方式）：
  - `TestShellRun_WhitelistedExecutes`：白名单含 `go`，`go version` 返回含 "go" 的输出，无审批请求
  - `TestShellRun_OutsideWhitelistRequiresApproval`：白名单外命令 + 非空 appr → goroutine 中 `Decide(approved=true)` 放行并执行；`Decide(false)` → 返回错误含 `ErrDenied`
  - `TestShellRun_OutsideWhitelistNoApprover`：appr 为 nil → 返回明确错误（拒绝执行）
  - `TestShellRun_GitWriteRequiresApproval`：`git push` → 触发审批事件（即使 git 在白名单）
  - `TestShellRun_Timeout`：`shellTimeout=100ms`，白名单含 `ping`，`ping -n 30 127.0.0.1` → 超时错误
  - `TestShellRun_NoShellInterpretation`：白名单含 `git`，`git status && echo hacked` → 返回错误（非法参数），输出不含 "hacked"
  - `TestTruncateOutput`：>max 截断 + 含"截断"提示；<max 原样
- [ ] **Step 2: 运行确认失败**：`go test ./internal/tools/ -run ShellRun -v` → 编译错误（函数未定义）
- [ ] **Step 3: 实现 `shell.go`**：`strings.Fields` 分词 → 首 token 为程序名 → 白名单判定 → 需审批时 `appr.Request(sessionID, "run_shell", command, "命令: "+command)` → `shellSem` 抢占 → `exec.CommandContext(ctx, prog, args...)` + `cmd.Dir = ws.Root()` → `CombinedOutput` → `truncateOutput(out, MaxShellOutput)`；非零退出码附在错误/输出中
- [ ] **Step 4: 测试通过**：`go test ./internal/tools/ -v` PASS
- [ ] **Step 5: 全量回归**：`go test ./internal/...` PASS

### Task 2: go_test / go_vet / go_build 工具

**Files:**
- Create: `internal/tools/gotool.go`
- Test: `internal/tools/gotool_test.go`

**Interfaces:**
- Consumes: Task 1 `ShellRun`（固定传 `[]string{"go"}` 白名单、appr=nil → 免审批）
- Produces:
  - `func GoToolset(ws *workspace.Manager) []tool.BaseTool`（工具名：`go_test` / `go_vet` / `go_build`）
  - 参数结构：`GoTestArgs{Dir string \`json:"dir,omitempty"\`; Run string \`json:"run,omitempty"\`; Verbose bool \`json:"verbose,omitempty"\`}`、`GoVetArgs{Dir string}`、`GoBuildArgs{Dir string}`
  - dir 安全解析：非空时 `ws.Resolve(dir)`，`cmd.Dir` 用解析后绝对路径（ShellRun 内统一处理：command 相对路径以 workspace 为基准，gotool 对 dir 先 Resolve 再拼绝对路径进参数）

- [ ] **Step 1: 写失败测试**（temp workspace 内建最小 go module，`go` 不可用时 `t.Skip`）：
  - `TestGoTest_Pass`：hello_test.go 通过 → 输出含 "ok"
  - `TestGoTest_FailExtractsNames`：构造失败的 `TestBroken` → 输出含 `--- FAIL: TestBroken` 与失败用例摘要行
  - `TestGoTest_RejectsOutsideDir`：`dir:"../outside"` → 错误含 `ErrOutsideWorkspace`
  - `TestGoVet_Build`：`go vet ./...` 与 `go build ./...` 正常执行（合法包）
- [ ] **Step 2: 确认失败**：`go test ./internal/tools/ -run GoTest -v` → 编译错误
- [ ] **Step 3: 实现 `gotool.go`**：三个工具闭包拼命令串（`go test [-run X] [-v] ./...` / `go vet ./...` / `go build ./...`）→ `ShellRun`；go_test 额外解析 `--- FAIL:` 行追加「失败用例: A, B」摘要
- [ ] **Step 4: 测试通过**：`go test ./internal/tools/ -v` PASS
- [ ] **Step 5: 回归**：`go test ./internal/...` PASS

### Task 3: 白名单配置

**Files:**
- Modify: `internal/config/config.go`（`Config`、`defaultConfig`、`clone`、新增方法）
- Test: `internal/config/config_test.go`（无则创建）

**Interfaces:**
- Consumes: 无
- Produces:
  - `Config.ShellWhitelist []string \`json:"shellWhitelist,omitempty"\``
  - `func DefaultShellWhitelist() []string` → `["go","git","dir","ls"]`
  - `func (m *Manager) ShellWhitelist() []string`（未配置时返回默认值；返回副本）
  - `clone` 深拷贝 ShellWhitelist

- [ ] **Step 1: 写失败测试**：`TestDefaultShellWhitelist`（默认文件含 4 条）、`TestShellWhitelist_Fallback`（空配置返回默认）、`TestClone_ShellWhitelistIndependent`（改副本不影响原配置）
- [ ] **Step 2: 确认失败**：`go test ./internal/config/ -v` → 编译错误
- [ ] **Step 3: 实现**：字段 + 默认值 + clone 深拷贝 + 方法
- [ ] **Step 4: 测试通过 + 回归**：`go test ./internal/...` PASS

### Task 4: 装配接入主 Agent + 提示词

**Files:**
- Modify: `internal/agent/assembly.go`（`OrchestratorTools`、`BuildOrchestrator` 签名）、`internal/agent/instruction.go`、`internal/agent/chat.go`（`initAgentLocked` 调用处）
- Test: Modify `internal/agent/assembly_test.go`

**Interfaces:**
- Consumes: Task 1 `tools.ShellRun`、Task 2 `tools.GoToolset`、Task 3 `cfg.ShellWhitelist()`、现有 `tools.WriteToolset`
- Produces:
  - `OrchestratorTools(ctx, ws, cm, appr, backupRoot, sessionID, whitelist []string)`（追加参数）
  - `BuildOrchestrator(ctx, ws, cm, appr, backupRoot, sessionID, whitelist []string, extras ...string)`
  - `run_shell` 工具：`ShellArgs{Command string \`json:"command"\`}` → `ShellRun(ws, whitelist, appr, sessionID, args.Command)`（appr 经 WriteToolset 同款传参）
  - `chat.go`：`initAgentLocked` 中 `wl := s.cfg.ShellWhitelist()` 传入

- [ ] **Step 1: 写失败测试**（`assembly_test.go` 现有 fakeModel 模式）：`TestBuildOrchestratorWithExecTools`——带 ws+appr 构建后断言工具名集合含 `run_shell`/`go_test`/`go_vet`/`go_build`
- [ ] **Step 2: 确认失败**：`go test ./internal/agent/ -run Orchestrator -v` → 编译错误
- [ ] **Step 3: 实现**：签名追加 `whitelist []string`（变参 `extras` 必须保持最后一位）；`OrchestratorTools` 追加 `GoToolset(ws)` 与 `run_shell` 工具；更新 `chat.go` 调用处
- [ ] **Step 4: 更新 `instruction.go`**（稳定前缀，一次性改对）：
  - 角色段：「代码修改、命令执行等写操作工具将在后续版本接入」→ 改为已接入 write_file/edit_file/run_shell/go_test/go_vet/go_build 的能力描述
  - 新增「# 执行与修复循环」段：构建/测试失败时按「读错误 → read/edit 修复 → 重跑验证」循环，最多 3 轮；超限停止并输出未解决问题清单；go_test/go_vet/go_build 免审批，run_shell 白名单内免审批、白名单外需用户审批
- [ ] **Step 5: 测试通过 + 全量回归**：`go test ./internal/...` PASS

### Task 5: 前端——回滚按钮 + 工具图标 + 白名单编辑

**Files:**
- Modify: `frontend/src/components/ReviewPanel.tsx`（file-item 回滚按钮）
- Modify: `frontend/src/components/ThreadPanel.tsx`（`toolIcons` 增加 run_shell/go_test/go_vet/go_build）
- Modify: `frontend/src/components/SettingsPanel.tsx`（Shell 白名单编辑）
- Modify: `frontend/src/styles.css`（回滚按钮/白名单行样式，复用现有 `.side-btn`/`.file-item` 体系）
- Modify: `frontend/bindings/...`（wails3 重新生成，勿手改）

**Interfaces:**
- Consumes: `ApprovalService.RollbackFile(relPath)`（已存在）、`ConfigService.GetConfig/SaveConfig`、`config.Config.shellWhitelist`（重新生成 bindings 后可用）
- Produces: 无新接口

- [ ] **Step 1: 重新生成 bindings**：`wails3 generate bindings`（或在 wails3 dev 会话中自动生成），确认 `bindings/.../config/models.ts` 出现 `shellWhitelist`
- [ ] **Step 2: ReviewPanel**：文件列表每项加「↩ 回滚」按钮 → `confirm('回滚该文件到写前备份？')` → `ApprovalService.RollbackFile(c.path)` → 成功/失败提示 + `refresh()`；`.aidog` 内说明：备份不存在时展示后端错误文本
- [ ] **Step 3: ThreadPanel**：`toolIcons` 补 4 个图标（run_shell '⌨'、go_test '🧪'、go_vet '🔍'、go_build '🔨'）
- [ ] **Step 4: SettingsPanel**：新增「安全」分区（Shell 白名单）：读取 `cfg.shellWhitelist ?? []`，列表行可删除、输入框可添加，随现有保存按钮走 `SaveConfig`
- [ ] **Step 5: 验证**：`pnpm build` PASS

### Task 6: 端到端验证

- [ ] **Step 1**: `go build ./... && go test ./internal/...` 全绿
- [ ] **Step 2**: `wails3 dev`（WAILS_MCP=1）+ mcp-wails3-dev 实测：设置页白名单可增删保存；对话内让 Agent 执行 `go_test`（本工作区）→ 事件流出现 🧪 工具卡片；`run_shell` 白名单外命令弹出审批卡片
- [ ] **Step 3**: 更新文档与 skill：`.codebuddy/skills/aidog-m4-execution-fix/SKILL.md` 新建（结构同既有 aidog-* skill）；`aidog-project-overview` 里程碑表/索引加 M4 行；`README.md` 里程碑表 M4 → 完成；`docs/REQUIREMENTS.md` FR-3/FR-4.2/4.5 勾选
