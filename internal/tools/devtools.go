package tools

// devtools 工具集：移植自 github.com/fpt/go-dev-mcp 的只读开发工具。
// 文档查询访问固定域名（pkg.go.dev / docs.rs / docs.python.org），
// 本地工具后续在 devtools_local.go 中补充；全部免审批。

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"ai-dog-agent/internal/devtools"
	"ai-dog-agent/internal/workspace"
)

// devtools 工具名（与 InferTool 注册名保持一致）。
const (
	ToolDevSearchGoDoc         = "dev_search_godoc"
	ToolDevReadGoDoc           = "dev_read_godoc"
	ToolDevSearchWithinGoDoc   = "dev_search_within_godoc"
	ToolDevSearchRustDoc       = "dev_search_rustdoc"
	ToolDevReadRustDoc         = "dev_read_rustdoc"
	ToolDevSearchWithinRustDoc = "dev_search_within_rustdoc"
	ToolDevSearchPyDoc         = "dev_search_pydoc"
	ToolDevReadPyDoc           = "dev_read_pydoc"
	ToolDevSearchWithinPyDoc   = "dev_search_within_pydoc"
	ToolDevTreeDir             = "dev_tree_dir"
	ToolDevSearchLocalFiles    = "dev_search_local_files"
	ToolDevScanMarkdown        = "dev_scan_markdown"
	ToolDevOutlineGoPackage    = "dev_outline_go_package"
	ToolDevValidateGoCode      = "dev_validate_go_code"
	ToolGroupDevTools          = "devtools"
)

// DevDocToolNames 文档查询工具名（9 个）。
var DevDocToolNames = []string{
	ToolDevSearchGoDoc, ToolDevReadGoDoc, ToolDevSearchWithinGoDoc,
	ToolDevSearchRustDoc, ToolDevReadRustDoc, ToolDevSearchWithinRustDoc,
	ToolDevSearchPyDoc, ToolDevReadPyDoc, ToolDevSearchWithinPyDoc,
}

// DevLocalToolNames 本地分析工具名（5 个）。
var DevLocalToolNames = []string{
	ToolDevTreeDir, ToolDevSearchLocalFiles, ToolDevScanMarkdown,
	ToolDevOutlineGoPackage, ToolDevValidateGoCode,
}

// devtools 参数结构（JSON Schema 由 eino InferTool 自动生成）。

type devSearchArgs struct {
	Query string `json:"query" jsonschema:"搜索关键词"`
}

type devReadGoDocArgs struct {
	PackageURL string `json:"package_url" jsonschema:"Go 包路径，如 golang.org/x/net/html"`
	Offset     int    `json:"offset,omitempty" jsonschema:"起始行号(从0开始)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"读取行数(默认100)"`
}

type devWithinGoDocArgs struct {
	PackageURL string `json:"package_url" jsonschema:"Go 包路径，如 golang.org/x/net/html"`
	Keyword    string `json:"keyword" jsonschema:"在文档中检索的关键词"`
	MaxMatches int    `json:"max_matches,omitempty" jsonschema:"命中上限(默认10)"`
}

type devReadRustDocArgs struct {
	CrateURL string `json:"crate_url" jsonschema:"crate 名或路径，如 serde、serde/de"`
	Offset   int    `json:"offset,omitempty" jsonschema:"起始行号(从0开始)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"读取行数(默认100)"`
}

type devWithinRustDocArgs struct {
	CrateURL   string `json:"crate_url" jsonschema:"crate 名或路径，如 serde、tokio/runtime"`
	Keyword    string `json:"keyword" jsonschema:"在文档中检索的关键词"`
	MaxMatches int    `json:"max_matches,omitempty" jsonschema:"命中上限(默认10)"`
}

type devReadPyDocArgs struct {
	ModuleName string `json:"module_name" jsonschema:"Python 模块名，如 json、os.path"`
	Offset     int    `json:"offset,omitempty" jsonschema:"起始行号(从0开始)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"读取行数(默认100)"`
}

type devWithinPyDocArgs struct {
	ModuleName string `json:"module_name" jsonschema:"Python 模块名，如 json、os.path"`
	Keyword    string `json:"keyword" jsonschema:"在文档中检索的关键词"`
	MaxMatches int    `json:"max_matches,omitempty" jsonschema:"命中上限(默认10)"`
}

// DevToolset 构建 devtools 工具集（只读、免审批）。
// ws 当前仅本地工具使用，文档工具不依赖工作区。
func DevToolset(ws *workspace.Manager) []tool.BaseTool {
	list, err := buildDevTools(ws)
	if err != nil {
		panic(fmt.Sprintf("构建 devtools 工具集失败: %v", err))
	}
	return list
}

func buildDevTools(ws *workspace.Manager) ([]tool.BaseTool, error) {
	var list []tool.BaseTool

	searchGo, err := utils.InferTool(ToolDevSearchGoDoc,
		"在 pkg.go.dev 搜索 Go 包（按包名/路径/简介匹配）。",
		func(ctx context.Context, a devSearchArgs) (string, error) {
			out, err := devtools.SearchGoDoc(a.Query)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("「%s」的 Go 包搜索结果：\n%s", a.Query, out), nil
		})
	if err != nil {
		return nil, err
	}
	list = append(list, searchGo)

	readGo, err := utils.InferTool(ToolDevReadGoDoc,
		"读取 pkg.go.dev 上某个 Go 包的文档，按行分页（offset/limit）。",
		func(ctx context.Context, a devReadGoDocArgs) (string, error) {
			return formatPaged("Go 包", a.PackageURL, a.Offset, a.Limit,
				func(offset, limit int) (string, int, bool, error) {
					return devtools.ReadGoDocPaged(a.PackageURL, offset, limit)
				})
		})
	if err != nil {
		return nil, err
	}
	list = append(list, readGo)

	withinGo, err := utils.InferTool(ToolDevSearchWithinGoDoc,
		"在指定 Go 包文档内检索关键词，返回命中行号与内容。",
		func(ctx context.Context, a devWithinGoDocArgs) (string, error) {
			return formatWithin("Go 包", a.PackageURL, a.Keyword, a.MaxMatches,
				func(max int) ([]devtools.SearchMatch, bool, error) {
					r, err := devtools.SearchWithinGoDoc(a.PackageURL, a.Keyword, max)
					if err != nil {
						return nil, false, err
					}
					return r.Matches, r.Truncated, nil
				})
		})
	if err != nil {
		return nil, err
	}
	list = append(list, withinGo)

	searchRust, err := utils.InferTool(ToolDevSearchRustDoc,
		"在 docs.rs 搜索 Rust crate。",
		func(ctx context.Context, a devSearchArgs) (string, error) {
			out, err := devtools.SearchRustDoc(a.Query)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("「%s」的 Rust crate 搜索结果：\n%s", a.Query, out), nil
		})
	if err != nil {
		return nil, err
	}
	list = append(list, searchRust)

	readRust, err := utils.InferTool(ToolDevReadRustDoc,
		"读取 docs.rs 上某个 crate 的文档，按行分页（offset/limit）。",
		func(ctx context.Context, a devReadRustDocArgs) (string, error) {
			return formatPaged("Rust crate", a.CrateURL, a.Offset, a.Limit,
				func(offset, limit int) (string, int, bool, error) {
					return devtools.ReadRustDocPaged(a.CrateURL, offset, limit)
				})
		})
	if err != nil {
		return nil, err
	}
	list = append(list, readRust)

	withinRust, err := utils.InferTool(ToolDevSearchWithinRustDoc,
		"在指定 Rust crate 文档内检索关键词，返回命中行号与内容。",
		func(ctx context.Context, a devWithinRustDocArgs) (string, error) {
			return formatWithin("Rust crate", a.CrateURL, a.Keyword, a.MaxMatches,
				func(max int) ([]devtools.SearchMatch, bool, error) {
					r, err := devtools.SearchWithinRustDoc(a.CrateURL, a.Keyword, max)
					if err != nil {
						return nil, false, err
					}
					return r.Matches, r.Truncated, nil
				})
		})
	if err != nil {
		return nil, err
	}
	list = append(list, withinRust)

	searchPy, err := utils.InferTool(ToolDevSearchPyDoc,
		"在 docs.python.org 的模块索引中搜索 Python 标准库模块。",
		func(ctx context.Context, a devSearchArgs) (string, error) {
			out, err := devtools.SearchPyDoc(a.Query)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("「%s」的 Python 模块搜索结果：\n%s", a.Query, out), nil
		})
	if err != nil {
		return nil, err
	}
	list = append(list, searchPy)

	readPy, err := utils.InferTool(ToolDevReadPyDoc,
		"读取 docs.python.org 上某个模块的文档，按行分页（offset/limit）。",
		func(ctx context.Context, a devReadPyDocArgs) (string, error) {
			return formatPaged("Python 模块", a.ModuleName, a.Offset, a.Limit,
				func(offset, limit int) (string, int, bool, error) {
					return devtools.ReadPyDocPaged(a.ModuleName, offset, limit)
				})
		})
	if err != nil {
		return nil, err
	}
	list = append(list, readPy)

	withinPy, err := utils.InferTool(ToolDevSearchWithinPyDoc,
		"在指定 Python 模块文档内检索关键词，返回命中行号与内容。",
		func(ctx context.Context, a devWithinPyDocArgs) (string, error) {
			return formatWithin("Python 模块", a.ModuleName, a.Keyword, a.MaxMatches,
				func(max int) ([]devtools.SearchMatch, bool, error) {
					r, err := devtools.SearchWithinPyDoc(a.ModuleName, a.Keyword, max)
					if err != nil {
						return nil, false, err
					}
					return r.Matches, r.Truncated, nil
				})
		})
	if err != nil {
		return nil, err
	}
	list = append(list, withinPy)

	local, err := buildDevLocalTools(ws)
	if err != nil {
		return nil, err
	}
	list = append(list, local...)
	return list, nil
}

// formatPaged 统一分页输出：标题行 + 内容 + 下一页提示。
func formatPaged(kind, name string, offset, limit int,
	read func(offset, limit int) (string, int, bool, error)) (string, error) {
	content, total, hasMore, err := read(offset, limit)
	if err != nil {
		return "", err
	}
	startLine := offset + 1
	endLine := offset + len(strings.Split(strings.TrimSpace(content), "\n"))
	if strings.TrimSpace(content) == "" {
		endLine = offset
	}
	out := fmt.Sprintf("%s「%s」文档（第 %d-%d 行，共 %d 行）：\n%s",
		kind, name, startLine, endLine, total, content)
	if hasMore {
		next := offset + limit
		if limit <= 0 {
			next = offset + devtools.DefaultLinesPerPage
		}
		out += fmt.Sprintf("\n...（可用 offset=%d 继续读取）", next)
	}
	return out, nil
}

// formatWithin 统一关键词检索输出。
func formatWithin(kind, name, keyword string, maxMatches int,
	search func(max int) ([]devtools.SearchMatch, bool, error)) (string, error) {
	if maxMatches <= 0 {
		maxMatches = 10
	}
	matches, truncated, err := search(maxMatches)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return fmt.Sprintf("%s「%s」中未找到「%s」", kind, name, keyword), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s「%s」中「%s」的命中：\n", kind, name, keyword)
	for _, m := range matches {
		fmt.Fprintf(&sb, "- 第 %d 行\n```\n%s\n```\n", m.LineNo, m.Text)
	}
	if truncated {
		sb.WriteString("...（更多命中已截断）\n")
	}
	return sb.String(), nil
}
