package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/winproc"
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
//   - ctx 取消（用户中断）会同时撤销审批等待并终止子进程。
func ShellRun(ctx context.Context, ws *workspace.Manager, whitelist []string, appr *approval.Manager, sessionID, command string) (string, error) {
	if !ws.IsSet() {
		return "", workspace.ErrNoWorkspace
	}
	fields, err := splitCommand(command)
	if err != nil {
		return "", err
	}
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
		if _, err := appr.RequestContext(ctx, sessionID, "run_shell", command, preview); err != nil {
			return "", err
		}
	}
	text, code, rerr := runCommand(ctx, ws, prog, args)
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
// exitCode 区分；启动失败、超时或 ctx 取消时 err 非 nil 且 exitCode 为 -1。
func runCommand(ctx context.Context, ws *workspace.Manager, prog string, args []string) (text string, exitCode int, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !ws.IsSet() {
		return "", -1, workspace.ErrNoWorkspace
	}
	shellSem <- struct{}{}
	defer func() { <-shellSem }()

	runCtx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, prog, args...)
	cmd.Dir = ws.Root()
	// GUI 进程没有控制台：阻止子进程分配可见窗口
	winproc.HideConsole(cmd)
	out, e := cmd.CombinedOutput()
	text = truncateOutput(string(out), MaxShellOutput)
	if e != nil {
		// 父 ctx 取消 = 用户中断；与自身超时区分开，便于前端提示
		if ctx.Err() != nil {
			return "", -1, fmt.Errorf("命令已中断: %w", ctx.Err())
		}
		if runCtx.Err() != nil {
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

// splitCommand 按空白分词，支持单/双引号包裹含空格的参数。
// 反斜杠按字面保留（兼容 Windows 路径）；双引号内支持 \" 转义。
// 不做变量展开、管道、重定向或通配符解析。
func splitCommand(command string) ([]string, error) {
	var (
		args     []string
		cur      strings.Builder
		quote    rune
		hasToken bool
	)
	push := func() {
		if hasToken {
			args = append(args, cur.String())
			cur.Reset()
			hasToken = false
		}
	}
	rs := []rune(command)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '"' && r == '\\' && i+1 < len(rs) && rs[i+1] == '"':
			// 仅在双引号内把 \" 还原为引号，避免吃掉 Windows 路径的反斜杠
			cur.WriteRune('"')
			hasToken = true
			i++
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
			hasToken = true
		case r == '"' || r == '\'':
			quote = r
			hasToken = true
		case unicode.IsSpace(r):
			push()
		default:
			cur.WriteRune(r)
			hasToken = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("命令存在未闭合的引号")
	}
	push()
	return args, nil
}

// classifyCommand 判定命令是否可执行：返回 (白名单放行, 需要强制审批)。
func classifyCommand(prog string, args []string, whitelist []string) (allowed bool, needsApproval bool) {
	if !containsToken(whitelist, prog) {
		return false, false // 白名单外：走审批（或拒绝）
	}
	if !subcommandReadOnly(prog, args) {
		return true, true
	}
	return true, false
}

// subcommandReadOnly 判定白名单程序的子命令是否为只读（可免审批执行）。
// go 只放行 build/test/vet/fmt/version 且参数不含执行外部程序/写文件的选项；
// git 只放行 status/diff/log/show 与 branch 的列分支形式。其余子命令
// （install/get、push/commit/reset、branch -D/-m 等）可联网、可写文件或可改
// 仓库状态，一律要求审批（spec 3.3）。
func subcommandReadOnly(prog string, args []string) bool {
	if len(args) == 0 {
		return true
	}
	sub := args[0]
	switch prog {
	case "go":
		if !goSafeSubcommands[sub] {
			return false
		}
		return goArgsReadOnly(args[1:])
	case "git":
		return gitReadOnly(sub, args[1:])
	}
	return true
}

var goSafeSubcommands = map[string]bool{
	"build": true, "test": true, "vet": true, "fmt": true, "version": true,
}

// goUnsafeFlags 会执行外部程序或写文件的 go 选项：即使子命令在白名单内也必须审批。
// 否则 run_shell 可用 `go test -exec` / `go build -toolexec` / `go vet -vettool`
// 绕过审批执行任意程序，或用 `-o` / `-coverprofile` 等往任意路径写文件。
var goUnsafeFlags = []string{
	"-exec", "-toolexec", "-vettool",
	"-o", "-outputdir",
	"-coverprofile", "-cpuprofile", "-memprofile",
	"-blockprofile", "-mutexprofile", "-trace",
}

// goArgsReadOnly 检查 go 子命令的其余参数是否只读（不含 -exec/-toolexec/-o）。
func goArgsReadOnly(args []string) bool {
	for _, a := range args {
		for _, bad := range goUnsafeFlags {
			if a == bad || strings.HasPrefix(a, bad+"=") {
				return false
			}
		}
	}
	return true
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

// truncateOutput 截断超长输出并附提示（按 UTF-8 边界切断，避免半个中文字符）。
func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return truncateUTF8(s, max) + fmt.Sprintf("\n…（输出超长，已截断至 %d 字节）", max)
}

// ShellArgs run_shell 工具参数。
type ShellArgs struct {
	Command string `json:"command" jsonschema:"要执行的完整命令（首 token 为程序名，经空格分词，不经 shell 解释）"`
}
