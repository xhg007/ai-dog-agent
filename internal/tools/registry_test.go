package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestReadOnlyToolsetNames(t *testing.T) {
	ws := setupWS(t)
	toolset := ReadOnlyToolset(ws)
	want := map[string]bool{"read_file": false, "list_files": false, "search_code": false, "search_symbols": false, "git_diff": false, "git_status": false}
	for _, tl := range toolset {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info 失败: %v", err)
		}
		if _, ok := want[info.Name]; ok {
			want[info.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("工具集缺少 %s", name)
		}
	}
}

func TestToolReadFileInvoke(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var readFileTool tool.InvokableTool
	for _, tl := range ReadOnlyToolset(ws) {
		info, _ := tl.Info(context.Background())
		if info.Name == "read_file" {
			readFileTool = tl.(tool.InvokableTool)
		}
	}
	if readFileTool == nil {
		t.Fatal("未找到 read_file 工具")
	}
	out, err := readFileTool.InvokableRun(context.Background(), `{"path":"a.txt"}`)
	if err != nil {
		t.Fatalf("InvokableRun 失败: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("结果为空")
	}

	// 逃逸路径应通过工具层返回错误
	if _, err := readFileTool.InvokableRun(context.Background(), `{"path":"../x.txt"}`); err == nil {
		t.Fatal("逃逸路径应报错")
	}
}

func TestSearchSymbolsTool(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	if err := os.WriteFile(filepath.Join(root, "a.go"),
		[]byte("package a\n\ntype Server struct{}\n\nfunc Load() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var symTool tool.InvokableTool
	for _, tl := range ReadOnlyToolset(ws) {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if info.Name == ToolSearchSymbols {
			symTool = tl.(tool.InvokableTool)
		}
	}
	if symTool == nil {
		t.Fatal("未找到 search_symbols 工具")
	}

	out, err := symTool.InvokableRun(context.Background(), `{"pattern":"serv"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "Server") || !strings.Contains(out, "struct") {
		t.Fatalf("search_symbols 结果不符: %s", out)
	}

	out, err = symTool.InvokableRun(context.Background(), `{"kind":"func"}`)
	if err != nil {
		t.Fatalf("InvokableRun(kind): %v", err)
	}
	if !strings.Contains(out, "Load") || strings.Contains(out, "Server") {
		t.Fatalf("kind 过滤不符: %s", out)
	}

	out, err = symTool.InvokableRun(context.Background(), `{"language":"python"}`)
	if err != nil {
		t.Fatalf("InvokableRun(language): %v", err)
	}
	if !strings.Contains(out, "未找到") {
		t.Fatalf("language 过滤应无结果: %s", out)
	}
}
