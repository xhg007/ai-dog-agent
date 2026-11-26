package tools

// 本次缺陷修复的回归测试：大文件分页、list_files 相对路径、
// UTF-8 安全截断、git 只读子命令判定。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
)

// read_file 对 >1MB 的文件也必须能用 offset/limit 分页（FR-2.1）。
func TestReadFileLargeFilePagination(t *testing.T) {
	ws := setupWS(t)
	var sb strings.Builder
	sb.WriteString("first line\n")
	for sb.Len() < 1<<21 {
		sb.WriteString("padding line for large file\n")
	}
	writeFile(t, ws.Root(), "big.go", sb.String())

	out, err := ReadFile(ws, "big.go", 1, 2)
	if err != nil {
		t.Fatalf("大文件分页读取失败: %v", err)
	}
	if !strings.Contains(out, "1: first line") || !strings.Contains(out, "2: padding line") {
		t.Fatalf("分页内容不符:\n%s", out)
	}
	if strings.Contains(out, "3: ") {
		t.Fatalf("应只返回请求的 2 行:\n%s", out)
	}
}

// 大文件从小节偏移处读取也应可用（不需要从头扫描全部输出）。
func TestReadFileLargeFileOffset(t *testing.T) {
	ws := setupWS(t)
	var sb strings.Builder
	for sb.Len() < 1<<21 {
		sb.WriteString("filler\n")
	}
	sb.WriteString("needle line\n")
	writeFile(t, ws.Root(), "big.txt", sb.String())

	total := strings.Count(sb.String(), "\n")
	out, err := ReadFile(ws, "big.txt", total, 1)
	if err != nil {
		t.Fatalf("大文件偏移读取失败: %v", err)
	}
	if !strings.Contains(out, "needle line") {
		t.Fatalf("应读到末行内容:\n%s", out)
	}
}

// 非递归 list_files 的 Path 必须是相对 workspace 的路径（与递归一致）。
func TestListFilesNonRecursiveRelativePath(t *testing.T) {
	ws := setupWS(t)
	writeFile(t, ws.Root(), "internal/tools/fs.go", "package tools\n")

	entries, err := ListFiles(ws, "internal/tools", false, 0)
	if err != nil {
		t.Fatalf("ListFiles 失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("期望 1 项，实际 %d: %+v", len(entries), entries)
	}
	want := filepath.Join("internal", "tools", "fs.go")
	if entries[0].Path != want {
		t.Fatalf("Path = %q，期望相对 workspace 的 %q", entries[0].Path, want)
	}
}

// 输出截断必须落在 UTF-8 边界上（中文不被切成半个字符）。
func TestTruncatePreservesUTF8Boundary(t *testing.T) {
	s := strings.Repeat("中文", 20)
	for cut := 1; cut < len(s); cut++ {
		got := truncateOutput(s, cut)
		body, _, _ := strings.Cut(got, "\n…")
		if !utf8.ValidString(body) {
			t.Fatalf("cut=%d 截断后不是合法 UTF-8: %q", cut, body)
		}
		if len(body) > cut {
			t.Fatalf("cut=%d 截断后超过上限: %d 字节", cut, len(body))
		}
	}

	line := strings.Repeat("汉", 200)
	got := strings.TrimSuffix(truncate(line, 300), "…")
	if !utf8.ValidString(got) {
		t.Fatalf("search_code 行截断后不是合法 UTF-8: %q", got)
	}
}

// git 子命令只读判定：branch 的写形式与 --output 必须走审批。
func TestClassifyGitSubcommandReadOnly(t *testing.T) {
	cases := []struct {
		args         []string
		wantApproval bool
	}{
		{[]string{"status"}, false},
		{[]string{"status", "--porcelain"}, false},
		{[]string{"diff", "HEAD"}, false},
		{[]string{"log", "--oneline"}, false},
		{[]string{"show"}, false},
		{[]string{"branch"}, false},
		{[]string{"branch", "-a"}, false},
		{[]string{"branch", "--show-current"}, false},
		{[]string{"branch", "-D", "main"}, true},
		{[]string{"branch", "-d", "main"}, true},
		{[]string{"branch", "-m", "old", "new"}, true},
		{[]string{"branch", "feature"}, true},
		{[]string{"push", "origin", "main"}, true},
		{[]string{"commit", "-m", "x"}, true},
		{[]string{"diff", "--output=patch.txt"}, true},
		{[]string{"log", "--output", "log.txt"}, true},
	}
	for _, c := range cases {
		allowed, needsApproval := classifyCommand("git", c.args, []string{"git"})
		if !allowed {
			t.Fatalf("git %v 应在白名单内（走审批），却直接被拒", c.args)
		}
		if needsApproval != c.wantApproval {
			t.Fatalf("git %v: needsApproval=%v，期望 %v", c.args, needsApproval, c.wantApproval)
		}
	}

	if allowed, _ := classifyCommand("git", []string{"status"}, nil); allowed {
		t.Fatal("空白名单不应放行 git")
	}
}

// GitRun 只读封装同样拒绝 branch 的写形式（判定发生在执行之前，不依赖 git）。
func TestGitRunRejectsBranchDelete(t *testing.T) {
	ws := setupWS(t)
	if _, err := GitRun(ws, []string{"branch", "-D", "main"}); err == nil {
		t.Fatal("GitRun 应拒绝 git branch -D")
	}
}

// 文件写入走 tmp+rename：内容正确且不留下临时文件。
func TestWriteFileAtomicNoTempLeftovers(t *testing.T) {
	ws, _ := writeSetupWS(t)
	if _, err := WriteFileContent(ws, "a.txt", "v0"); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFileContent(ws, "a.txt", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := EditFileContent(ws, "a.txt", "v1", "v1-edited"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(ws.Root(), "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v1-edited" {
		t.Fatalf("内容不符: %q", data)
	}

	entries, err := os.ReadDir(ws.Root())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".aidog-write-") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

// go 子命令即使白名单内，含 -exec/-toolexec/-o 也必须走审批。
func TestClassifyGoArgs(t *testing.T) {
	safe := [][]string{
		{"test", "./..."},
		{"build", "./..."},
		{"vet", "./..."},
		{"test", "-run", "TestX", "-count", "1"},
		{"build", "-ldflags", "-s -w", "./..."},
	}
	for _, args := range safe {
		ok, needs := classifyCommand("go", args, []string{"go"})
		if !ok || needs {
			t.Fatalf("go %v 应免审批直接执行（ok=%v needs=%v）", args, ok, needs)
		}
	}

	needsApproval := [][]string{
		{"test", "-exec", "malware", "./..."},
		{"test", "-exec=malware", "./..."},
		{"build", "-toolexec", "prog", "./..."},
		{"build", "-toolexec=prog", "./..."},
		{"vet", "-vettool", "prog", "./..."},
		{"build", "-o", "out.exe", "./..."},
		{"build", "-o=out.exe", "./..."},
		{"test", "-coverprofile", "cov.out", "./..."},
		{"test", "-cpuprofile", "cpu.out", "./..."},
		{"install", "./..."},
	}
	for _, args := range needsApproval {
		ok, needs := classifyCommand("go", args, []string{"go"})
		if !ok || !needs {
			t.Fatalf("go %v 应要求审批（ok=%v needs=%v）", args, ok, needs)
		}
	}
}

// 无审批通道时写工具必须拒绝执行（而不是 nil deref panic）。
func TestWriteToolsetNilApprover(t *testing.T) {
	ws := setupWS(t)
	for _, tl := range WriteToolset(ws, t.TempDir(), nil, "s1") {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != "write_file" {
			continue
		}
		it, ok := tl.(tool.InvokableTool)
		if !ok {
			t.Fatal("write_file 不是 InvokableTool")
		}
		if _, err := it.InvokableRun(context.Background(), `{"path":"a.txt","content":"x"}`); err == nil {
			t.Fatal("无审批通道时应拒绝写入")
		}
		if _, serr := os.Stat(filepath.Join(ws.Root(), "a.txt")); !os.IsNotExist(serr) {
			t.Fatal("拒绝后不应创建文件")
		}
		return
	}
	t.Fatal("未找到 write_file 工具")
}

// go_build 不应在 workspace 根留下可执行文件（产物应写入临时目录）。
func TestGoBuildDoesNotPolluteWorkspace(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go 未安装")
	}
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.21\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")

	for _, tl := range GoToolset(ws) {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name != "go_build" {
			continue
		}
		it, ok := tl.(tool.InvokableTool)
		if !ok {
			t.Fatal("go_build 不是 InvokableTool")
		}
		out, err := it.InvokableRun(context.Background(), `{}`)
		if err != nil {
			t.Fatalf("go_build 失败: %v\n%s", err, out)
		}
		entries, rerr := os.ReadDir(root)
		if rerr != nil {
			t.Fatal(rerr)
		}
		for _, e := range entries {
			if !e.IsDir() && e.Name() != "go.mod" && e.Name() != "main.go" {
				t.Fatalf("workspace 根出现构建产物: %s", e.Name())
			}
		}
		return
	}
	t.Fatal("未找到 go_build 工具")
}
