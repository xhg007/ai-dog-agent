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
		ToolWriteFile, ToolEditFile, ToolRunShell,
		ToolDevSearchGoDoc, ToolDevReadGoDoc, ToolDevSearchWithinGoDoc,
		ToolDevSearchRustDoc, ToolDevReadRustDoc, ToolDevSearchWithinRustDoc,
		ToolDevSearchPyDoc, ToolDevReadPyDoc, ToolDevSearchWithinPyDoc,
		ToolDevTreeDir, ToolDevSearchLocalFiles, ToolDevScanMarkdown,
		ToolDevOutlineGoPackage, ToolDevValidateGoCode,
	}
}

// ToolOption 工具目录项：供设置页多选与 agent-smith 参考。
type ToolOption struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Group       string `json:"group"` // readonly / write / exec / devtools
}

// 工具分组（配置页按组展示）。
const (
	ToolGroupReadOnly = "readonly"
	ToolGroupWrite    = "write"
	ToolGroupExec     = "exec"
)

var toolOptions = []ToolOption{
	// 只读
	{ToolReadFile, "读取 workspace 内文本文件（offset/limit 分页）", ToolGroupReadOnly},
	{ToolListFiles, "列出目录内容（可递归）", ToolGroupReadOnly},
	{ToolSearchCode, "按文本/正则检索代码，命中带所属符号", ToolGroupReadOnly},
	{ToolSearchSymbols, "跨语言检索代码符号（函数/类型/类等）", ToolGroupReadOnly},
	{ToolGitDiff, "查看相对 HEAD 的未提交变更", ToolGroupReadOnly},
	{ToolGitStatus, "查看 git 状态（porcelain）", ToolGroupReadOnly},
	// 写入（需审批 + 自动备份）
	{ToolWriteFile, "写入文件（需用户审批，自动备份）", ToolGroupWrite},
	{ToolEditFile, "精确替换文件片段（需用户审批，自动备份）", ToolGroupWrite},
	// 执行
	{ToolRunShell, "执行 Shell 命令（白名单内免审批，白名单外需审批）", ToolGroupExec},
	// devtools 文档查询
	{ToolDevSearchGoDoc, "在 pkg.go.dev 搜索 Go 包", ToolGroupDevTools},
	{ToolDevReadGoDoc, "分页读取 pkg.go.dev 上的 Go 包文档", ToolGroupDevTools},
	{ToolDevSearchWithinGoDoc, "在指定 Go 包文档内检索关键词", ToolGroupDevTools},
	{ToolDevSearchRustDoc, "在 docs.rs 搜索 Rust crate", ToolGroupDevTools},
	{ToolDevReadRustDoc, "分页读取 docs.rs 上的 crate 文档", ToolGroupDevTools},
	{ToolDevSearchWithinRustDoc, "在指定 crate 文档内检索关键词", ToolGroupDevTools},
	{ToolDevSearchPyDoc, "在 docs.python.org 搜索标准库模块", ToolGroupDevTools},
	{ToolDevReadPyDoc, "分页读取 Python 模块文档", ToolGroupDevTools},
	{ToolDevSearchWithinPyDoc, "在指定 Python 模块文档内检索关键词", ToolGroupDevTools},
	// devtools 本地分析
	{ToolDevTreeDir, "输出目录树（连接线风格）", ToolGroupDevTools},
	{ToolDevSearchLocalFiles, "按扩展名检索文件内容", ToolGroupDevTools},
	{ToolDevScanMarkdown, "扫描 Markdown 标题与行号", ToolGroupDevTools},
	{ToolDevOutlineGoPackage, "Go 包概览：依赖、导出声明、调用图", ToolGroupDevTools},
	{ToolDevValidateGoCode, "Go 静态检查：vet/build/gofmt/mod tidy", ToolGroupDevTools},
}

// ToolOptions 返回工具目录副本（名称、用途、分组）。
func ToolOptions() []ToolOption {
	return append([]ToolOption(nil), toolOptions...)
}

// KnownToolGroups 返回可用的工具组名。配置里写组名可一次启用整组。
func KnownToolGroups() []string { return []string{ToolGroupDevTools} }

// devToolNameSet devtools 工具名集合（组名展开与按需构建共用）。
var devToolNameSet = func() map[string]bool {
	m := make(map[string]bool, len(DevDocToolNames)+len(DevLocalToolNames))
	for _, n := range DevDocToolNames {
		m[n] = true
	}
	for _, n := range DevLocalToolNames {
		m[n] = true
	}
	return m
}()

// ExpandToolGroups 把组名展开为具体工具名；非组名原样保留。
// 未知名称最终由 Catalog 以「未知工具名」报错，避免配置拼写错误静默失效。
func ExpandToolGroups(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if n == ToolGroupDevTools {
			out = append(out, DevDocToolNames...)
			out = append(out, DevLocalToolNames...)
			continue
		}
		out = append(out, n)
	}
	return out
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
//   - 支持组名 "devtools"，展开为 14 个移植工具；
//   - 未知名称返回错误（列出可用工具）；
//   - write_file/edit_file 需要审批管理器；
//   - go_* 仅适用于 Go 项目，由调用方按需声明。
func Catalog(ctx context.Context, ws *workspace.Manager, deps CatalogDeps, names []string) ([]tool.BaseTool, error) {
	want := ExpandToolGroups(names)
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
	needsDev := false
	for _, n := range ordered {
		if devToolNameSet[n] {
			needsDev = true
			break
		}
	}
	if needsDev {
		if err := add(DevToolset(ws)); err != nil {
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
