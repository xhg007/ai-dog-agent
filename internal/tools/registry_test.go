package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestReadOnlyToolsetNames(t *testing.T) {
	ws := setupWS(t)
	toolset := ReadOnlyToolset(ws)
	want := map[string]bool{"read_file": false, "list_files": false, "search_code": false, "git_diff": false, "git_status": false}
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
