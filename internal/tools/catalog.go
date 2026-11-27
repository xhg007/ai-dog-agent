package tools

// 工具目录：work agent 按 Spec.Tools 从目录中选择工具。
// 空列表默认只读（安全默认）；未知工具名显式报错，避免配置拼写错误静默失效。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/workspace"
)

// 工具名常量（与 InferTool 注册名保持一致）。
const (
	ToolReadFile      = "read_file"
	ToolListFiles     = "list_files"
	ToolSearchCode    = "search_code"
	ToolSearchSymbols = "search_symbols"
	ToolGitDiff       = "git_diff"
	ToolGitStatus     = "git_status"
	ToolWriteFile     = "write_file"
	ToolEditFile      = "edit_file"
	ToolGoTest        = "go_test"
	ToolGoVet         = "go_vet"
	ToolGoBuild       = "go_build"
	ToolRunShell      = "run_shell"
)

// DefaultReadOnlyTools 空工具配置时的默认集合（只读，安全默认）。
var DefaultReadOnlyTools = []string{
	ToolReadFile, ToolListFiles, ToolSearchCode, ToolSearchSymbols, ToolGitDiff, ToolGitStatus,
}

// KnownToolNames 返回目录中的全部工具名（错误信息与前端提示用）。
func KnownToolNames() []string {
	return []string{
		ToolReadFile, ToolListFiles, ToolSearchCode, ToolSearchSymbols, ToolGitDiff, ToolGitStatus,
		ToolWriteFile, ToolEditFile, ToolGoTest, ToolGoVet, ToolGoBuild, ToolRunShell,
	}
}

// CatalogDeps 按名构建工具的依赖项。
type CatalogDeps struct {
	BackupDir string
	Approval  *approval.Manager
	SessionID string
	Whitelist []string
}

// Catalog 按名称构建工具集：
//   - names 为空 → DefaultReadOnlyTools（只读）；
//   - 未知名称返回错误（列出可用工具）；
//   - write_file/edit_file 需要审批管理器；
//   - go_* 仅适用于 Go 项目，由调用方按需声明。
func Catalog(ctx context.Context, ws *workspace.Manager, deps CatalogDeps, names []string) ([]tool.BaseTool, error) {
	want := names
	if len(want) == 0 {
		want = DefaultReadOnlyTools
	}
	seen := make(map[string]bool, len(want))
	ordered := make([]string, 0, len(want))
	for _, n := range want {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		ordered = append(ordered, n)
	}

	available := make(map[string]tool.BaseTool, len(KnownToolNames()))
	add := func(list []tool.BaseTool) error {
		for _, t := range list {
			info, err := t.Info(ctx)
			if err != nil {
				return fmt.Errorf("读取工具信息失败: %w", err)
			}
			available[info.Name] = t
		}
		return nil
	}

	if err := add(ReadOnlyToolset(ws)); err != nil {
		return nil, err
	}
	if seen[ToolWriteFile] || seen[ToolEditFile] {
		if deps.Approval == nil {
			return nil, errors.New("write_file/edit_file 需要审批管理器")
		}
		if err := add(WriteToolset(ws, deps.BackupDir, deps.Approval, deps.SessionID)); err != nil {
			return nil, err
		}
	}
	if seen[ToolGoTest] || seen[ToolGoVet] || seen[ToolGoBuild] {
		if err := add(GoToolset(ws)); err != nil {
			return nil, err
		}
	}
	if seen[ToolRunShell] {
		shellTool, err := ShellTool(ws, deps.Whitelist, deps.Approval, deps.SessionID)
		if err != nil {
			return nil, err
		}
		available[ToolRunShell] = shellTool
	}

	out := make([]tool.BaseTool, 0, len(ordered))
	var unknown []string
	for _, n := range ordered {
		t, ok := available[n]
		if !ok {
			unknown = append(unknown, n)
			continue
		}
		out = append(out, t)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("未知工具名: %s（可用: %s）",
			strings.Join(unknown, ", "), strings.Join(KnownToolNames(), ", "))
	}
	return out, nil
}

// ShellTool 构建 run_shell 工具：白名单内免审批，白名单外经审批门。
func ShellTool(ws *workspace.Manager, whitelist []string, appr *approval.Manager,
	sessionID string) (tool.BaseTool, error) {
	return utils.InferTool(ToolRunShell,
		"在项目工作区内执行 Shell 命令。白名单内命令免审批直接执行；白名单外命令需用户审批。命令不经 shell 解释（无注入面）。",
		func(ctx context.Context, args ShellArgs) (string, error) {
			return ShellRun(ctx, ws, whitelist, appr, sessionID, args.Command)
		})
}
