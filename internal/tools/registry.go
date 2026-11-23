package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"ai-dog-agent/internal/workspace"
)

// ---------- 工具参数结构（JSON Schema 由 eino InferTool 自动生成） ----------

type readArgs struct {
	Path   string `json:"path" jsonschema:"相对 workspace 的文件路径"`
	Offset int    `json:"offset,omitempty" jsonschema:"起始行号(从1开始,0表示从头)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"读取行数(0表示默认2000)"`
}

type listArgs struct {
	Dir       string `json:"dir" jsonschema:"相对 workspace 的目录,为空表示根目录"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"是否递归列出子目录"`
	Max       int    `json:"max,omitempty" jsonschema:"结果上限(默认500)"`
}

type searchArgs struct {
	Pattern  string `json:"pattern" jsonschema:"检索内容(字面串或正则)"`
	Glob     string `json:"glob,omitempty" jsonschema:"文件名过滤(如 *.go,空为全部)"`
	IsRegex  bool   `json:"is_regex,omitempty" jsonschema:"pattern 是否为正则"`
	MaxCount int    `json:"max_results,omitempty" jsonschema:"结果上限(默认100)"`
}

// NoArgs 空参数结构（无参数工具使用）。
type NoArgs struct{}

// ReadOnlyToolset 构建只读工具集（免审批）：
// read_file / list_files / search_code / git_diff / git_status。
func ReadOnlyToolset(ws *workspace.Manager) []tool.BaseTool {
	toolList, err := buildAll(ws)
	if err != nil {
		// InferTool 仅在参数结构非法时报错；此处不可恢复，直接 panic 提示
		panic(fmt.Sprintf("构建只读工具集失败: %v", err))
	}
	return toolList
}

func buildAll(ws *workspace.Manager) ([]tool.BaseTool, error) {
	var toolList []tool.BaseTool

	readFileTool, err := utils.InferTool("read_file",
		"读取 workspace 内文本文件内容（带行号）。大文件用 offset/limit 分页。",
		func(ctx context.Context, a readArgs) (string, error) {
			return ReadFile(ws, a.Path, a.Offset, a.Limit)
		})
	if err != nil {
		return nil, err
	}
	toolList = append(toolList, readFileTool)

	listFilesTool, err := utils.InferTool("list_files",
		"列出 workspace 内目录内容。recursive 为 true 时递归；忽略 .git/node_modules/dist/vendor 等。",
		func(ctx context.Context, a listArgs) (string, error) {
			dir := a.Dir
			if dir == "" {
				dir = "."
			}
			entries, err := ListFiles(ws, dir, a.Recursive, a.Max)
			if err != nil {
				return "", err
			}
			out := fmt.Sprintf("共 %d 项:\n", len(entries))
			for _, e := range entries {
				if e.IsDir {
					out += "[目录] " + e.Path + "\n"
				} else {
					out += fmt.Sprintf("%8d  %s\n", e.Size, e.Path)
				}
			}
			return out, nil
		})
	if err != nil {
		return nil, err
	}
	toolList = append(toolList, listFilesTool)

	searchCodeTool, err := utils.InferTool("search_code",
		"在 workspace 内按行检索代码，返回 文件:行号:内容。默认忽略 .git/node_modules/dist/vendor。",
		func(ctx context.Context, a searchArgs) (string, error) {
			hits, err := SearchCode(ws, a.Pattern, a.Glob, a.IsRegex, a.MaxCount)
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				return "未找到匹配内容", nil
			}
			out := fmt.Sprintf("共 %d 处命中:\n", len(hits))
			for _, h := range hits {
				out += fmt.Sprintf("%s:%d: %s\n", h.File, h.Line, h.Text)
			}
			return out, nil
		})
	if err != nil {
		return nil, err
	}
	toolList = append(toolList, searchCodeTool)

	gitDiffTool, err := utils.InferTool("git_diff",
		"查看 workspace 相对 HEAD 的未提交变更 diff。",
		func(ctx context.Context, _ NoArgs) (string, error) {
			return GitDiff(ws)
		})
	if err != nil {
		return nil, err
	}
	toolList = append(toolList, gitDiffTool)

	gitStatusTool, err := utils.InferTool("git_status",
		"查看 workspace 的 git 状态（porcelain 格式）。",
		func(ctx context.Context, _ NoArgs) (string, error) {
			return GitStatus(ws)
		})
	if err != nil {
		return nil, err
	}
	toolList = append(toolList, gitStatusTool)

	return toolList, nil
}
