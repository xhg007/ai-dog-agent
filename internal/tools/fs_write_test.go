package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/workspace"
)

func writeSetupWS(t *testing.T) (*workspace.Manager, string) {
	t.Helper()
	root := t.TempDir()
	ws := workspace.New()
	if err := ws.SetRoot(root); err != nil {
		t.Fatalf("SetRoot 失败: %v", err)
	}
	return ws, root
}

func mustApprove(m *approval.Manager) {
	deadline := time.Now().Add(2 * time.Second)
	for len(m.ListPending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	for _, p := range m.ListPending() {
		_ = m.Decide(p.ID, true, false)
	}
}

// --- write_file 纯函数 ---

func TestWriteFileNew(t *testing.T) {
	ws, root := writeSetupWS(t)
	n, err := WriteFileContent(ws, filepath.Join("dir", "new.txt"), "hello\n")
	if err != nil {
		t.Fatalf("WriteFileContent 失败: %v", err)
	}
	if n != 6 {
		t.Fatalf("写入字节数不符: %d", n)
	}
	data, err := os.ReadFile(filepath.Join(root, "dir", "new.txt"))
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("文件内容不符: %q err=%v", data, err)
	}
}

func TestWriteFileEscapeRejected(t *testing.T) {
	ws, _ := writeSetupWS(t)
	if _, err := WriteFileContent(ws, "../evil.txt", "x"); !errors.Is(err, workspace.ErrOutsideWorkspace) {
		t.Fatalf("路径逃逸应被拒绝，got %v", err)
	}
}

func TestWriteFileBackupCreated(t *testing.T) {
	ws, root := writeSetupWS(t)
	backup := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFileWithBackup(ws, "a.txt", "newer", backup); err != nil {
		t.Fatalf("WriteFileWithBackup 失败: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(backup, "a.txt"))
	if len(matches) != 1 {
		t.Fatalf("备份文件未创建: %v", matches)
	}
	b, _ := os.ReadFile(matches[0])
	if string(b) != "old" {
		t.Fatalf("备份内容应为旧内容，got %q", b)
	}
}

// --- edit_file 纯函数 ---

func TestEditFileReplace(t *testing.T) {
	ws, root := writeSetupWS(t)
	if err := os.WriteFile(filepath.Join(root, "f.go"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := EditFileContent(ws, "f.go", "b", "B")
	if err != nil {
		t.Fatalf("EditFileContent 失败: %v", err)
	}
	if n == 0 {
		t.Fatal("应报告替换次数")
	}
	data, _ := os.ReadFile(filepath.Join(root, "f.go"))
	if string(data) != "a\nB\nc\n" {
		t.Fatalf("替换结果不符: %q", data)
	}
}

func TestEditFileNotFound(t *testing.T) {
	ws, root := writeSetupWS(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EditFileContent(ws, "f.txt", "xyz", "abc"); err == nil {
		t.Fatal("查找串不存在应报错")
	}
}

func TestEditFileAmbiguous(t *testing.T) {
	ws, root := writeSetupWS(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := EditFileContent(ws, "f.txt", "x", "y")
	if err == nil || !strings.Contains(err.Error(), "匹配 2 次") {
		t.Fatalf("多次匹配应报错，got %v", err)
	}
}

func TestEditFileEscapeRejected(t *testing.T) {
	ws, _ := writeSetupWS(t)
	if _, err := EditFileContent(ws, "../f.txt", "a", "b"); !errors.Is(err, workspace.ErrOutsideWorkspace) {
		t.Fatalf("路径逃逸应被拒绝，got %v", err)
	}
}

// --- 审批门工具 ---

func TestWriteToolApprovedExecutes(t *testing.T) {
	ws, root := writeSetupWS(t)
	m := approval.New(eventbus.New())
	set := WriteToolset(ws, t.TempDir(), m, "s1")
	tl := findTool(t, set, "write_file")
	inv, ok := tl.(tool.InvokableTool)
	if !ok {
		t.Fatal("write_file 不是 InvokableTool")
	}
	go mustApprove(m)
	out, err := inv.InvokableRun(context.Background(), `{"path":"g.txt","content":"hi"}`)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if !strings.Contains(out, "g.txt") {
		t.Fatalf("返回应包含路径: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(root, "g.txt"))
	if string(data) != "hi" {
		t.Fatalf("文件未写入: %q", data)
	}
}

func TestWriteToolRejectedDenied(t *testing.T) {
	ws, root := writeSetupWS(t)
	m := approval.New(eventbus.New())
	set := WriteToolset(ws, t.TempDir(), m, "s1")
	tl := findTool(t, set, "write_file")
	inv := tl.(tool.InvokableTool)
	go mustReject(m)
	if _, err := inv.InvokableRun(context.Background(), `{"path":"g.txt","content":"hi"}`); err == nil {
		t.Fatal("拒绝后应返回错误")
	}
	if _, err := os.Stat(filepath.Join(root, "g.txt")); !os.IsNotExist(err) {
		t.Fatal("拒绝后不应写文件")
	}
}

func mustReject(m *approval.Manager) {
	deadline := time.Now().Add(2 * time.Second)
	for len(m.ListPending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	for _, p := range m.ListPending() {
		_ = m.Decide(p.ID, false, false)
	}
}

func findTool(t *testing.T, set []tool.BaseTool, name string) tool.BaseTool {
	t.Helper()
	for _, tl := range set {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info 失败: %v", err)
		}
		if info.Name == name {
			return tl
		}
	}
	t.Fatalf("工具集缺少 %s", name)
	return nil
}
