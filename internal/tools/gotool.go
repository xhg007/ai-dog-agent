package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"ai-dog-agent/internal/workspace"
)

// GoToolset 构建 Go 构建工具集：go_test / go_vet / go_build。
//
// 三者均属默认白名单内的 go 只读子命令（build/test/vet），免审批（FR-4.2），
// 但仍复用 ShellRun 内核的超时/截断/并发保护（FR-3.5）。
// dir 参数越出 workspace 时被路径安全层拒绝（FR-4.1）。
func GoToolset(ws *workspace.Manager) []tool.BaseTool {
	testTool, err := utils.InferTool("go_test",
		"运行 Go 测试（go test）。测试未通过时返回失败用例名摘要（属正常结果，供分析修复）；dir 为 workspace 内相对目录（递归其子包），缺省为整个模块。",
		func(ctx context.Context, args GoTestArgs) (string, error) {
			target, err := resolveGoTarget(ws, args.Dir)
			if err != nil {
				return "", err
			}
			argv := []string{"test"}
			if args.Run != "" {
				argv = append(argv, "-run", args.Run)
			}
			if args.Verbose {
				argv = append(argv, "-v")
			}
			argv = append(argv, target)
			out, code, rerr := runCommand(ctx, ws, "go", argv)
			if rerr != nil {
				return "", rerr // 启动失败/超时才是工具错误
			}
			if code != 0 {
				return "测试未通过（退出码 " + itoa(code) + "）:\n" + augmentTestFailures(out), nil
			}
			return out, nil
		})
	if err != nil {
		return []tool.BaseTool{}
	}

	vetTool, err := utils.InferTool("go_vet",
		"运行 go vet 静态检查。发现问题返回问题详情（属正常结果）；dir 为 workspace 内相对目录（递归其子包），缺省为整个模块。",
		func(ctx context.Context, args GoDirArgs) (string, error) {
			target, err := resolveGoTarget(ws, args.Dir)
			if err != nil {
				return "", err
			}
			out, code, rerr := runCommand(ctx, ws, "go", []string{"vet", target})
			if rerr != nil {
				return "", rerr
			}
			if code != 0 {
				return "go vet 发现问题（退出码 " + itoa(code) + "）:\n" + out, nil
			}
			return outOrEmpty(out), nil
		})
	if err != nil {
		return []tool.BaseTool{}
	}

	buildTool, err := utils.InferTool("go_build",
		"编译模块（go build）。编译失败返回错误详情（属正常结果，供分析修复）；dir 为 workspace 内相对目录（递归其子包），缺省为整个模块。",
		func(ctx context.Context, args GoDirArgs) (string, error) {
			target, err := resolveGoTarget(ws, args.Dir)
			if err != nil {
				return "", err
			}
			// 输出到临时目录：go build 默认把 main 包的可执行文件写到 cwd
			//（即 workspace 根），会在用户项目里留下未跟踪产物。
			outDir, terr := os.MkdirTemp("", "aidog-gobuild-")
			if terr != nil {
				return "", fmt.Errorf("创建临时输出目录失败: %w", terr)
			}
			defer os.RemoveAll(outDir)
			out, code, rerr := runCommand(ctx, ws, "go", []string{"build", "-o", outDir, target})
			if rerr != nil {
				return "", rerr
			}
			if code != 0 {
				return "编译失败（退出码 " + itoa(code) + "）:\n" + out, nil
			}
			return outOrEmpty(out), nil
		})
	if err != nil {
		return []tool.BaseTool{}
	}

	return []tool.BaseTool{testTool, vetTool, buildTool}
}

// GoTestArgs go_test 参数。
type GoTestArgs struct {
	Dir     string `json:"dir,omitempty" jsonschema:"运行测试的目录（workspace 内相对路径，缺省为整个模块）"`
	Run     string `json:"run,omitempty" jsonschema:"仅运行匹配的测试（go test -run 的值）"`
	Verbose bool   `json:"verbose,omitempty" jsonschema:"是否加 -v 输出详细日志"`
}

// GoDirArgs go_vet / go_build 参数。
type GoDirArgs struct {
	Dir string `json:"dir,omitempty" jsonschema:"目标目录（workspace 内相对路径，缺省为整个模块）"`
}

// resolveGoTarget 解析目标包路径模式：dir 为空返回 "./..."（相对 workspace 根），
// 否则经路径安全层解析为绝对路径并追加 "..." 递归子包
// （go test <dir> 只测目录自身的包，不递归；含空格路径也安全——不经 shell 分词）。
func resolveGoTarget(ws *workspace.Manager, dir string) (string, error) {
	if dir == "" {
		return "./...", nil
	}
	abs, err := ws.Resolve(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, "..."), nil
}

// outOrEmpty 空输出转为提示文本（模型可读）。
func outOrEmpty(out string) string {
	if strings.TrimSpace(out) == "" {
		return "（无输出，命令成功）"
	}
	return out
}

// itoa 整数转字符串。
func itoa(n int) string {
	return strconv.Itoa(n)
}

// failLineRe 匹配 go test 输出的失败用例行。
var failLineRe = regexp.MustCompile(`(?m)^\s*--- FAIL: ([^\s(]+)`)

// augmentTestFailures 从 go test 输出中提取失败用例，追加摘要行。
func augmentTestFailures(out string) string {
	var names []string
	for _, m := range failLineRe.FindAllStringSubmatch(out, 32) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		return out
	}
	return out + "\n失败用例: " + strings.Join(names, ", ")
}
