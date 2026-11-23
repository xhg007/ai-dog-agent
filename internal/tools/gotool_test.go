package tools

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"ai-dog-agent/internal/workspace"
)

// --- go_test / go_vet / go_build 工具 ---

// setupGoModule 在临时 workspace 内创建最小 Go module。
func setupGoModule(t *testing.T, ws *workspace.Manager) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go 命令不可用，跳过")
	}
	files := map[string]string{
		"go.mod":  "module example.com/mini\n\ngo 1.21\n",
		"main.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n\nfunc main() { _ = Add(1, 2) }\n",
		"main_test.go": `package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("1+2 != 3")
	}
}
`,
	}
	for rel, content := range files {
		if _, err := WriteFileContent(ws, rel, content); err != nil {
			t.Fatalf("写入 %s 失败: %v", rel, err)
		}
	}
}

// invokeGoTool 按名称调用工具集中工具，返回 (输出, 错误)。
func invokeGoTool(t *testing.T, toolset []tool.BaseTool, name, argsJSON string) (string, error) {
	t.Helper()
	for _, tl := range toolset {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info 失败: %v", err)
		}
		if info.Name != name {
			continue
		}
		inv, ok := tl.(tool.InvokableTool)
		if !ok {
			t.Fatalf("工具 %s 未实现 InvokableTool", name)
		}
		return inv.InvokableRun(context.Background(), argsJSON)
	}
	t.Fatalf("未找到工具 %s", name)
	return "", nil
}

// TestGoTest_Pass go_test 正常通过场景。
func TestGoTest_Pass(t *testing.T) {
	ws, _ := writeSetupWS(t)
	setupGoModule(t, ws)
	toolset := GoToolset(ws)
	if len(toolset) != 3 {
		t.Fatalf("应有 3 个工具，got %d", len(toolset))
	}
	out, err := invokeGoTool(t, toolset, "go_test", `{}`)
	if err != nil {
		t.Fatalf("go_test 失败: %v 输出: %s", err, out)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("输出应含 ok: %s", out)
	}
}

// TestGoTest_FailExtractsNames 测试失败作为正常文本结果返回（不中断对话），
// 且包含失败用例摘要。
func TestGoTest_FailExtractsNames(t *testing.T) {
	ws, _ := writeSetupWS(t)
	setupGoModule(t, ws)
	if _, err := WriteFileContent(ws, "broken_test.go", `package main

import "testing"

func TestBroken(t *testing.T) {
	t.Fatal("故意失败")
}
`); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	out, err := invokeGoTool(t, GoToolset(ws), "go_test", `{}`)
	if err != nil {
		t.Fatalf("测试失败应作为文本结果而非工具错误: %v", err)
	}
	if !strings.Contains(out, "测试未通过") {
		t.Fatalf("结果应标注未通过: %s", out)
	}
	if !strings.Contains(out, "--- FAIL: TestBroken") {
		t.Fatalf("应包含失败用例行: %s", out)
	}
	if !strings.Contains(out, "失败用例: TestBroken") {
		t.Fatalf("应含失败用例摘要: %s", out)
	}
}

// TestGoTest_DirRecursesSubpackages 指定 dir 时递归其子包（父目录自身无 Go 文件也应正常）。
func TestGoTest_DirRecursesSubpackages(t *testing.T) {
	ws, _ := writeSetupWS(t)
	setupGoModule(t, ws)
	// pkg/ 子包带一个通过的测试
	if _, err := WriteFileContent(ws, "pkg/pkg.go", `package pkg

func Twice(n int) int { return n * 2 }
`); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if _, err := WriteFileContent(ws, "pkg/pkg_test.go", `package pkg

import "testing"

func TestTwice(t *testing.T) {
	if Twice(2) != 4 {
		t.Fatal("2*2 != 4")
	}
}
`); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	// "." 是 workspace 根：自身无 Go 文件，但子包应被递归测试
	out, err := invokeGoTool(t, GoToolset(ws), "go_test", `{"dir":"."}`)
	if err != nil {
		t.Fatalf("dir 递归测试失败: %v out=%s", err, out)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("输出应含 ok: %s", out)
	}
}

// TestGoTest_RejectsOutsideDir dir 参数越出 workspace 被拒绝。
func TestGoTest_RejectsOutsideDir(t *testing.T) {
	ws, _ := writeSetupWS(t)
	_, err := invokeGoTool(t, GoToolset(ws), "go_test", `{"dir":"../outside"}`)
	if !errors.Is(err, workspace.ErrOutsideWorkspace) {
		t.Fatalf("dir 越界应拒绝，got %v", err)
	}
}

// TestGoVet_Build go_vet 与 go_build 正常场景。
func TestGoVet_Build(t *testing.T) {
	ws, _ := writeSetupWS(t)
	setupGoModule(t, ws)
	if _, err := invokeGoTool(t, GoToolset(ws), "go_vet", `{}`); err != nil {
		t.Fatalf("go_vet 失败: %v", err)
	}
	if _, err := invokeGoTool(t, GoToolset(ws), "go_build", `{}`); err != nil {
		t.Fatalf("go_build 失败: %v", err)
	}
}
