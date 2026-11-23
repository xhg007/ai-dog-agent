package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/workspace"
)

// ShellRun 在 workspace 内执行 Shell 命令（不经 cmd/PowerShell 解释，无注入面）。
//
// 安全策略（FR-4.2 / spec 3.3）：
//   - 白名单内命令免审批直接执行；白名单外命令必须经审批门（无审批器时拒绝）；
//   - go / git 即使在白名单内，也仅放行只读子命令，其余子命令（install/get、
//     push/commit/reset 等）强制审批；
//   - 超时 shellTimeout（默认 120s，FR-3.5）、输出截断 MaxShellOutput（64KB）、
//     进程内并发上限 1（shellSem）。
func ShellRun(ws *workspace.Manager, whitelist []string, appr *approval.Manager, sessionID, command string) (string, error) {
	if !ws.IsSet() {
		return "", workspace.ErrNoWorkspace
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", fmt.Errorf("命令为空")
	}
	prog, args := fields[0], fields[1:]

	allowed, needsApproval := classifyCommand(prog, args, whitelist)
	if needsApproval || !allowed {
		if appr == nil {
			return "", fmt.Errorf("命令 %q 不在白名单内且无审批通道，已拒绝执行", prog)
		}
		preview := "命令: " + command
		if !allowed {
			preview += "\n（不在 Shell 白名单内，需要审批）"
		} else {
			preview += "\n（该子命令为写操作/超出只读范围，需要审批）"
		}
		if _, err := appr.Request(sessionID, "run_shell", command, preview); err != nil {
			return "", err
		}
	}
	text, code, rerr := runCommand(ws, prog, args)
	if rerr != nil {
		return "", rerr
	}
	if code != 0 {
		// 命令本身执行完成但返回非零：把完整输出交给模型分析
		return "", fmt.Errorf("命令失败（退出码 %d）:\n%s", code, text)
	}
	return text, nil
}

// runCommand 执行内核：并发限制、超时、输出截断。
// 调用方负责白名单判定与审批（ShellRun 分词后调用；GoToolset 直传参数数组，
// 规避含空格路径的字符串分词问题）。
// 返回值：命令成功或"正常失败"（非零退出码，如编译/测试失败）时 err 为 nil，
// exitCode 区分；启动失败或超时时 err 非 nil 且 exitCode 为 -1。
func runCommand(ws *workspace.Manager, prog string, args []string) (text string, exitCode int, err error) {
	if !ws.IsSet() {
		return "", -1, workspace.ErrNoWorkspace
	}
	shellSem <- struct{}{}
	defer func() { <-shellSem }()

	ctx, cancel := context.WithTimeout(context.Background(), shellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Dir = ws.Root()
	out, e := cmd.CombinedOutput()
	text = truncateOutput(string(out), MaxShellOutput)
	if e != nil {
		if ctx.Err() != nil {
			return "", -1, fmt.Errorf("命令超时（>%v），已终止", shellTimeout)
		}
		var exitErr *exec.ExitError
		if errors.As(e, &exitErr) {
			// 命令执行完成但非零退出（如编译失败、测试失败）：属预期业务结果
			return text, exitErr.ExitCode(), nil
		}
		return "", -1, fmt.Errorf("启动命令失败: %w", e)
	}
	return text, 0, nil
}

// classifyCommand 判定命令是否可执行：返回 (白名单放行, 需要强制审批)。
func classifyCommand(prog string, args []string, whitelist []string) (allowed bool, needsApproval bool) {
	if !containsToken(whitelist, prog) {
		return false, false // 白名单外：走审批（或拒绝）
	}
	if len(args) == 0 {
		return true, false
	}
	sub := args[0]
	switch prog {
	case "go":
		// 仅放行构建/测试类只读子命令；install/get 等（可联网下载执行代码）强制审批
		if !goSafeSubcommands[sub] {
			return true, true
		}
	case "git":
		// 仅放行只读子命令；push/commit/reset 等写操作强制审批（spec 3.3）
		if !gitReadOnlySubcommands[sub] {
			return true, true
		}
	}
	return true, false
}

var goSafeSubcommands = map[string]bool{
	"build": true, "test": true, "vet": true, "fmt": true, "version": true,
}

// containsToken 判断白名单是否包含该程序名（忽略大小写与路径形式）。
func containsToken(whitelist []string, prog string) bool {
	base := strings.ToLower(prog)
	for _, w := range whitelist {
		if strings.ToLower(strings.TrimSpace(w)) == base {
			return true
		}
	}
	return false
}

// MaxShellOutput 命令输出截断上限：64KB（FR-3.5）。
const MaxShellOutput = 64 << 10

// shellTimeout 命令超时（包级变量便于测试注入）。
var shellTimeout = 120 * time.Second

// shellSem 进程内并发上限：同时 1 个 Shell 任务（FR-3.5）。
var shellSem = make(chan struct{}, 1)

// truncateOutput 截断超长输出并附提示。
func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n…（输出超长，已截断至 %d 字节）", max)
}

// ShellArgs run_shell 工具参数。
type ShellArgs struct {
	Command string `json:"command" jsonschema:"要执行的完整命令（首 token 为程序名，经空格分词，不经 shell 解释）"`
}
