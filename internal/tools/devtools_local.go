package tools

// devtools 本地工具：目录树、文件检索、Markdown 标题、Go 包概览与代码检查。
// 所有路径参数都是 workspace 相对路径，统一经 workspace.Manager 校验，
// 越界返回 workspace.ErrOutsideWorkspace；空路径表示项目根。

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"ai-dog-agent/internal/devtools"
	"ai-dog-agent/internal/workspace"
)

type devTreeArgs struct {
	Dir       string `json:"dir,omitempty" jsonschema:"workspace 内相对目录，为空表示项目根"`
	IgnoreDot bool   `json:"ignore_dot,omitempty" jsonschema:"是否忽略点文件与点目录（.git 始终忽略）"`
	MaxDepth  int    `json:"max_depth,omitempty" jsonschema:"最大深度（默认 4）"`
}

type devSearchLocalArgs struct {
	Path       string `json:"path,omitempty" jsonschema:"workspace 内相对目录，为空表示项目根"`
	Query      string `json:"query" jsonschema:"要检索的字面文本（区分大小写）"`
	Extension  string `json:"extension,omitempty" jsonschema:"文件扩展名，如 go、ts；留空表示全部文件"`
	MaxMatches int    `json:"max_matches,omitempty" jsonschema:"每个文件最多返回的命中数（默认 10）"`
}

type devScanMarkdownArgs struct {
	Path string `json:"path,omitempty" jsonschema:"workspace 内的 .md 文件或目录，为空表示项目根"`
}

type devOutlineArgs struct {
	Dir              string `json:"dir,omitempty" jsonschema:"workspace 内包含 Go 源码的相对目录，为空表示项目根"`
	SkipDependencies bool   `json:"skip_dependencies,omitempty" jsonschema:"跳过依赖区块"`
	SkipDeclarations bool   `json:"skip_declarations,omitempty" jsonschema:"跳过导出声明区块"`
	SkipCallGraph    bool   `json:"skip_call_graph,omitempty" jsonschema:"跳过调用图区块（输出最大）"`
}

type devValidateArgs struct {
	Dir string `json:"dir,omitempty" jsonschema:"workspace 内要检查的相对目录，为空表示整个模块"`
}

// buildDevLocalTools 构建 5 个本地 devtools 工具。
func buildDevLocalTools(ws *workspace.Manager) ([]tool.BaseTool, error) {
	var list []tool.BaseTool

	treeTool, err := utils.InferTool(ToolDevTreeDir,
		"输出目录树（连接线风格，默认 4 层），用于快速理解项目结构。",
		func(ctx context.Context, a devTreeArgs) (string, error) {
			root, err := devResolve(ws, a.Dir)
			if err != nil {
				return "", err
			}
			out, err := devtools.TreeDir(root, a.IgnoreDot, a.MaxDepth)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(out) == "" {
				return "目录为空。", nil
			}
			return fmt.Sprintf("目录树: %s\n%s", relPath(ws.Root(), root), out), nil
		})
	if err != nil {
		return nil, err
	}
	list = append(list, treeTool)

	searchTool, err := utils.InferTool(ToolDevSearchLocalFiles,
		"在 workspace 内按扩展名检索文件内容（字面匹配），按文件聚合命中并标注行号。",
		func(ctx context.Context, a devSearchLocalArgs) (string, error) {
			root, err := devResolve(ws, a.Path)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(a.Query) == "" {
				return "", fmt.Errorf("query 不能为空")
			}
			ext := strings.TrimSpace(a.Extension)
			if ext != "" && !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			results, err := devtools.SearchLocalFiles(root, ext, a.Query, a.MaxMatches)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "未找到匹配内容。", nil
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "共 %d 个文件命中:\n", len(results))
			for _, file := range results {
				fmt.Fprintf(&sb, "File: %s\n", relPath(ws.Root(), file.Filename))
				for _, m := range file.Matches {
					fmt.Fprintf(&sb, "- Line %d\n```\n%s\n```\n", m.LineNo, m.Text)
				}
				if file.Truncated {
					sb.WriteString("... (更多命中已截断)\n")
				}
			}
			return sb.String(), nil
		})
	if err != nil {
		return nil, err
	}
	list = append(list, searchTool)

	markdownTool, err := utils.InferTool(ToolDevScanMarkdown,
		"扫描 Markdown 文件并提取标题层级与行号（单文件或整个目录）。",
		func(ctx context.Context, a devScanMarkdownArgs) (string, error) {
			root, err := devResolve(ws, a.Path)
			if err != nil {
				return "", err
			}
			files, err := devtools.ScanMarkdown(root)
			if err != nil {
				return "", err
			}
			return formatMarkdownScan(files, ws.Root()), nil
		})
	if err != nil {
		return nil, err
	}
	list = append(list, markdownTool)

	outlineTool, err := utils.InferTool(ToolDevOutlineGoPackage,
		"生成 Go 包概览：模块名、依赖、导出声明与调用图（可用 skip_* 精简输出）。",
		func(ctx context.Context, a devOutlineArgs) (string, error) {
			root, err := devResolve(ws, a.Dir)
			if err != nil {
				return "", err
			}
			return devtools.OutlineGoPackage(root, devtools.OutlineGoPackageOptions{
				SkipDependencies: a.SkipDependencies,
				SkipDeclarations: a.SkipDeclarations,
				SkipCallGraph:    a.SkipCallGraph,
			})
		})
	if err != nil {
		return nil, err
	}
	list = append(list, outlineTool)

	validateTool, err := utils.InferTool(ToolDevValidateGoCode,
		"检查 Go 代码：go vet、go build -n、gofmt -l、go mod tidy -diff，汇总通过/失败项。",
		func(ctx context.Context, a devValidateArgs) (string, error) {
			return validateGoCode(ctx, ws, a.Dir)
		})
	if err != nil {
		return nil, err
	}
	list = append(list, validateTool)

	return list, nil
}

// validateGoCode 在 workspace 根执行 4 项只读检查（runCommand 提供超时/截断/并发保护）。
func validateGoCode(ctx context.Context, ws *workspace.Manager, dir string) (string, error) {
	abs, err := devResolve(ws, dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(ws.Root(), abs)
	if err != nil {
		return "", fmt.Errorf("解析相对路径失败: %w", err)
	}
	target := "./..."
	if rel != "." {
		target = "./" + filepath.ToSlash(rel) + "/..."
	}

	checks := []struct {
		name string
		prog string
		args []string
	}{
		{devtools.CheckGoVet, "go", []string{"vet", target}},
		{devtools.CheckGoBuild, "go", []string{"build", "-n", target}},
		{devtools.CheckGoModTidy, "go", []string{"mod", "tidy", "-diff"}},
		{devtools.CheckGofmt, "gofmt", []string{"-l", abs}},
	}

	results := make([]devtools.ValidationResult, 0, len(checks))
	for _, c := range checks {
		out, code, rerr := runCommand(ctx, ws, c.prog, c.args)
		if c.name == devtools.CheckGofmt {
			out = relativizeLines(out, ws.Root())
		}
		results = append(results, devtools.SummarizeCheck(c.name, out, "", code, rerr))
	}
	return devtools.FormatValidationReport(devtools.BuildValidationReport(relPath(ws.Root(), abs), results)), nil
}

// devResolve 解析 workspace 相对路径；空串表示项目根。
func devResolve(ws *workspace.Manager, path string) (string, error) {
	if ws == nil || !ws.IsSet() {
		return "", workspace.ErrNoWorkspace
	}
	p := strings.TrimSpace(path)
	if p == "" {
		p = "."
	}
	return ws.Resolve(p)
}

// relPath 返回相对 workspace 根的路径（统一斜杠），失败时回退原路径。
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// relativizeLines 把输出行中的 workspace 绝对路径替换为相对路径。
func relativizeLines(out, root string) string {
	if strings.TrimSpace(out) == "" {
		return out
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		line = strings.ReplaceAll(line, absRoot+string(filepath.Separator), "")
		line = strings.ReplaceAll(line, absRoot, ".")
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// formatMarkdownScan 输出相对路径的标题树。
func formatMarkdownScan(files []devtools.MarkdownFile, root string) string {
	if len(files) == 0 {
		return "未找到 Markdown 文件。"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "共 %d 个 Markdown 文件:\n\n", len(files))
	for _, file := range files {
		sb.WriteString(relPath(root, file.FileName))
		sb.WriteString("\n")
		for _, h := range file.Headings {
			indent := strings.Repeat("  ", h.Level-1)
			fmt.Fprintf(&sb, "%s%s %s (line %d)\n", indent, strings.Repeat("#", h.Level), h.Title, h.Line)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
