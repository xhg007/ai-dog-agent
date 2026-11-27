package tools

// devtools 工具组与本地工具的单测：组名展开、路径越界、tree/markdown/outline/validate 输出。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"ai-dog-agent/internal/workspace"
)

func TestExpandToolGroups(t *testing.T) {
	got := ExpandToolGroups([]string{"read_file", "devtools", "devtools"})
	wantCount := 1 + len(DevDocToolNames) + len(DevLocalToolNames) + len(DevDocToolNames) + len(DevLocalToolNames)
	if len(got) != wantCount {
		t.Fatalf("展开数量不符: got %d want %d", len(got), wantCount)
	}
	if got[0] != ToolReadFile {
		t.Fatalf("非组名应原样保留: %v", got[:1])
	}
	if _, err := Catalog(context.Background(), newCatalogWS(t), CatalogDeps{},
		[]string{"devtools"}); err != nil {
		t.Fatalf("Catalog(devtools): %v", err)
	}
}

func TestCatalog_DevToolsGroup(t *testing.T) {
	ws := newCatalogWS(t)
	list, err := Catalog(context.Background(), ws, CatalogDeps{}, []string{ToolGroupDevTools})
	if err != nil {
		t.Fatalf("Catalog(devtools): %v", err)
	}
	names := catalogToolNames(t, list)
	if len(names) != len(DevDocToolNames)+len(DevLocalToolNames) {
		t.Fatalf("devtools 组应有 14 个工具，got %d: %v", len(names), names)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	for _, want := range append(append([]string{}, DevDocToolNames...), DevLocalToolNames...) {
		if !found[want] {
			t.Fatalf("缺少工具 %s", want)
		}
	}
}

func TestCatalog_UnknownToolStillFails(t *testing.T) {
	if _, err := Catalog(context.Background(), newCatalogWS(t), CatalogDeps{},
		[]string{"devtoolz"}); err == nil || !strings.Contains(err.Error(), "未知工具名") {
		t.Fatalf("未知名称应显式报错: %v", err)
	}
}

// TestKnownToolNamesConsistency 工具名录与 devtools 组保持一致，且组名不与工具名冲突。
func TestKnownToolNamesConsistency(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range KnownToolNames() {
		if seen[n] {
			t.Fatalf("工具名重复: %s", n)
		}
		seen[n] = true
	}
	for _, n := range DevDocToolNames {
		if !seen[n] {
			t.Fatalf("文档工具未登记到 KnownToolNames: %s", n)
		}
	}
	for _, n := range DevLocalToolNames {
		if !seen[n] {
			t.Fatalf("本地工具未登记到 KnownToolNames: %s", n)
		}
	}
	for _, g := range KnownToolGroups() {
		if seen[g] {
			t.Fatalf("工具组名与工具名冲突: %s", g)
		}
	}
}

func TestDevTreeDirAndOutside(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "a.go", "package a\n")
	writeFile(t, root, "sub/b.txt", "hello\n")

	out, err := invokeDevTool(t, ws, ToolDevTreeDir, `{"dir":"","max_depth":2}`)
	if err != nil {
		t.Fatalf("dev_tree_dir 失败: %v", err)
	}
	for _, want := range []string{"a.go", "sub", "b.txt"} {
		if !strings.Contains(out, want) {
			t.Fatalf("目录树缺少 %q:\n%s", want, out)
		}
	}

	_, err = invokeDevTool(t, ws, ToolDevTreeDir, `{"dir":"../outside"}`)
	if !errors.Is(err, workspace.ErrOutsideWorkspace) {
		t.Fatalf("越界路径应返回 ErrOutsideWorkspace，got %v", err)
	}
}

func TestDevSearchLocalFiles(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "a.go", "package demo\n\nfunc Run() {}\n")
	writeFile(t, root, "b.md", "package 不是 Go 文件\n")

	out, err := invokeDevTool(t, ws, ToolDevSearchLocalFiles,
		`{"path":"","query":"package","extension":"go","max_matches":5}`)
	if err != nil {
		t.Fatalf("dev_search_local_files 失败: %v", err)
	}
	if !strings.Contains(out, "File: a.go") || !strings.Contains(out, "- Line 1") {
		t.Fatalf("检索结果不符:\n%s", out)
	}
	if strings.Contains(out, "b.md") {
		t.Fatalf("扩展名过滤未生效:\n%s", out)
	}
}

func TestDevScanMarkdown(t *testing.T) {
	ws := setupWS(t)
	writeFile(t, ws.Root(), "doc.md", "# 标题\n\n内容\n\n## 子标题\n")
	out, err := invokeDevTool(t, ws, ToolDevScanMarkdown, `{"path":"doc.md"}`)
	if err != nil {
		t.Fatalf("dev_scan_markdown 失败: %v", err)
	}
	for _, want := range []string{"doc.md", "# 标题 (line 1)", "## 子标题 (line 5)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Markdown 扫描缺少 %q:\n%s", want, out)
		}
	}
}

func TestDevOutlineGoPackage(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "go.mod", "module example.com/demo\n\ngo 1.26.0\n")
	writeFile(t, root, "main.go", `package demo

import "strings"

// Run 执行任务。
func Run() string {
	return strings.ToUpper("ok")
}
`)
	out, err := invokeDevTool(t, ws, ToolDevOutlineGoPackage, `{"dir":""}`)
	if err != nil {
		t.Fatalf("dev_outline_go_package 失败: %v", err)
	}
	for _, want := range []string{"Module: example.com/demo", "== 导出声明 ==", "Run", "== 调用图 =="} {
		if !strings.Contains(out, want) {
			t.Fatalf("包概览缺少 %q:\n%s", want, out)
		}
	}
}

func TestDevValidateGoCode(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "go.mod", "module example.com/demo\n\ngo 1.26.0\n")
	writeFile(t, root, "main.go", "package demo\n\nfunc Add(a, b int) int { return a + b }\n")

	out, err := invokeDevTool(t, ws, ToolDevValidateGoCode, `{"dir":""}`)
	if err != nil {
		t.Fatalf("dev_validate_go_code 失败: %v", err)
	}
	for _, want := range []string{"go vet", "gofmt check", "go.mod", "检查"} {
		if !strings.Contains(out, want) {
			t.Fatalf("检查报告缺少 %q:\n%s", want, out)
		}
	}
}

// invokeDevTool 按工具名从 DevToolset 中找到并执行。
func invokeDevTool(t *testing.T, ws *workspace.Manager, name, args string) (string, error) {
	t.Helper()
	for _, tl := range DevToolset(ws) {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if info.Name == name {
			invokable, ok := tl.(tool.InvokableTool)
			if !ok {
				t.Fatalf("%s 不是可调用工具", name)
			}
			return invokable.InvokableRun(context.Background(), args)
		}
	}
	t.Fatalf("未找到工具 %s", name)
	return "", nil
}
