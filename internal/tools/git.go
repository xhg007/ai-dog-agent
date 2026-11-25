package tools

import (
	"fmt"
	"os/exec"
	"strings"

	"ai-dog-agent/internal/workspace"
)

// gitReadOnlySubcommands 允许的 git 只读子命令（安全白名单）。
var gitReadOnlySubcommands = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "branch": true,
}

// GitRun 在 workspace 内执行只读 git 命令（不经 shell，无注入面）。
// 仅放行白名单子命令；其余一律拒绝（写操作走 M3 审批流程）。
func GitRun(ws *workspace.Manager, args []string) (string, error) {
	if !ws.IsSet() {
		return "", workspace.ErrNoWorkspace
	}
	if len(args) == 0 {
		return "", fmt.Errorf("缺少 git 子命令")
	}
	sub := args[0]
	if !gitReadOnlySubcommands[sub] {
		return "", fmt.Errorf("git %q 不在只读白名单内（允许: status/diff/log/show/branch）", sub)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = ws.Root()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("git %s 失败: %w", sub, err)
	}
	// 只去掉末尾换行，保留 porcelain 首行的前导空格（否则第一行状态位/路径会被吃掉）
	return strings.TrimRight(string(out), "\r\n"), nil
}

// GitDiff 返回 workspace 的变更 diff。
// 有提交时相对 HEAD；空仓库（unborn HEAD，尚无任何提交）降级为工作区 diff，
// 并附上未跟踪文件清单，避免 exit 128。
func GitDiff(ws *workspace.Manager) (string, error) {
	out, err := GitRun(ws, []string{"diff", "HEAD"})
	if err == nil {
		return out, nil
	}
	// unborn HEAD：git diff HEAD 返回 128（ambiguous argument 'HEAD'）
	if strings.Contains(out, "unknown revision") || strings.Contains(out, "ambiguous argument") || strings.Contains(out, "bad revision") {
		tracked, derr := GitRun(ws, []string{"diff"})
		if derr != nil {
			return "", derr
		}
		untracked, uerr := GitRun(ws, []string{"status", "--porcelain"})
		if uerr != nil {
			untracked = ""
		}
		var sb strings.Builder
		if tracked != "" {
			sb.WriteString(tracked)
			sb.WriteString("\n")
		}
		if untracked != "" {
			sb.WriteString("## 空仓库：尚无任何提交，以下为全部未跟踪/变更文件\n")
			sb.WriteString(untracked)
			sb.WriteString("\n")
		}
		return strings.TrimRight(sb.String(), "\r\n"), nil
	}
	return out, err
}

// GitStatus 返回工作区状态（porcelain 格式）。
func GitStatus(ws *workspace.Manager) (string, error) {
	return GitRun(ws, []string{"status", "--porcelain"})
}
