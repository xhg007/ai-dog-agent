package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ai-dog-agent/internal/workspace"
)

func setupWS(t *testing.T) *workspace.Manager {
	t.Helper()
	m := workspace.New()
	if err := m.SetRoot(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------- read_file ----------

func TestReadFileNumberedLines(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "a.txt", "l1\nl2\nl3\n")

	out, err := ReadFile(ws, "a.txt", 0, 0)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	for _, want := range []string{"1: l1", "2: l2", "3: l3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q:\n%s", want, out)
		}
	}
}

func TestReadFileOffsetLimit(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "a.txt", "l1\nl2\nl3\nl4\nl5\n")

	out, err := ReadFile(ws, "a.txt", 2, 2)
	if err != nil {
		t.Fatalf("ReadFile 失败: %v", err)
	}
	if !strings.Contains(out, "2: l2") || !strings.Contains(out, "3: l3") {
		t.Fatalf("应从第2行起读2行:\n%s", out)
	}
	if strings.Contains(out, "1: l1") || strings.Contains(out, "4: l4") {
		t.Fatalf("不应包含范围外的行:\n%s", out)
	}
}

func TestReadFileBinaryRejected(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{0x00, 0x01, 0x02, 'x'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(ws, "bin.dat", 0, 0); err == nil {
		t.Fatal("二进制文件应被拒绝")
	}
}

func TestReadFileEscapeRejected(t *testing.T) {
	ws := setupWS(t)
	if _, err := ReadFile(ws, "../secret.txt", 0, 0); !errors.Is(err, workspace.ErrOutsideWorkspace) {
		t.Fatalf("路径逃逸应被拒绝，got %v", err)
	}
	empty := workspace.New()
	if _, err := ReadFile(empty, "a.txt", 0, 0); !errors.Is(err, workspace.ErrNoWorkspace) {
		t.Fatalf("未设置 workspace 应返回 ErrNoWorkspace，got %v", err)
	}
}

// ---------- list_files ----------

func TestListFilesBasic(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "main.go", "package main")
	writeFile(t, root, "pkg/sub/x.go", "package sub")
	writeFile(t, root, ".git/config", "x")
	writeFile(t, root, "node_modules/y.js", "x")

	entries, err := ListFiles(ws, ".", false, 500)
	if err != nil {
		t.Fatalf("ListFiles 失败: %v", err)
	}
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	joined := strings.Join(paths, ";")
	if !strings.Contains(joined, "main.go") || !strings.Contains(joined, "pkg") {
		t.Fatalf("应包含 main.go 与 pkg 目录: %s", joined)
	}
	for _, e := range entries {
		if strings.Contains(e.Path, ".git") || strings.Contains(e.Path, "node_modules") {
			t.Fatalf("忽略目录不应出现: %s", e.Path)
		}
	}
}

func TestListFilesRecursive(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "pkg/sub/x.go", "package sub")

	entries, err := ListFiles(ws, ".", true, 500)
	if err != nil {
		t.Fatalf("ListFiles 失败: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Path, "pkg"+string(filepath.Separator)+"sub"+string(filepath.Separator)+"x.go") ||
			strings.HasSuffix(e.Path, `pkg\sub\x.go`) || strings.HasSuffix(e.Path, "pkg/sub/x.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("递归模式应包含深层文件: %+v", entries)
	}
}

func TestListFilesMaxEntries(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	for i := 0; i < 20; i++ {
		writeFile(t, root, filepath.Join("d", string(rune('a'+i))+".txt"), "x")
	}
	entries, err := ListFiles(ws, ".", true, 5)
	if err != nil {
		t.Fatalf("ListFiles 失败: %v", err)
	}
	if len(entries) > 5 {
		t.Fatalf("应截断到 5 条，got %d", len(entries))
	}
}

// ---------- search_code ----------

func TestSearchCodeLiteral(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "a.go", "func main() {}\n// TODO fix\n")
	writeFile(t, root, "b.txt", "nothing here\nTODO later\n")

	hits, err := SearchCode(ws, "TODO", "", false, 100)
	if err != nil {
		t.Fatalf("SearchCode 失败: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("应命中 2 处，got %d: %+v", len(hits), hits)
	}
	files := map[string]bool{}
	for _, h := range hits {
		files[filepath.Base(h.File)] = true
	}
	if !files["a.go"] || !files["b.txt"] {
		t.Fatalf("命中文件不符: %+v", hits)
	}
}

func TestSearchCodeRegex(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "a.go", "func Hello() {}\nfunc World() {}\n")

	hits, err := SearchCode(ws, `func (Hello|World)`, "", true, 100)
	if err != nil {
		t.Fatalf("SearchCode 失败: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("正则应命中 2 处，got %d", len(hits))
	}
}

func TestSearchCodeInvalidRegex(t *testing.T) {
	ws := setupWS(t)
	if _, err := SearchCode(ws, "(unclosed", "", true, 100); err == nil {
		t.Fatal("非法正则应返回错误")
	}
}

func TestSearchCodeIgnoresVendorDirs(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, ".git/hooks/x.txt", "needle\n")
	writeFile(t, root, "node_modules/x.txt", "needle\n")
	writeFile(t, root, "src/x.txt", "needle\n")

	hits, err := SearchCode(ws, "needle", "", false, 100)
	if err != nil {
		t.Fatalf("SearchCode 失败: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("应只命中 src/x.txt，got %+v", hits)
	}
}

func TestSearchCodeMaxResults(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("needle\n")
	}
	writeFile(t, root, "big.txt", sb.String())

	hits, err := SearchCode(ws, "needle", "", false, 10)
	if err != nil {
		t.Fatalf("SearchCode 失败: %v", err)
	}
	if len(hits) > 10 {
		t.Fatalf("应截断到 10 条，got %d", len(hits))
	}
}

func TestSearchCodeSymbolContext(t *testing.T) {
	ws := setupWS(t)
	root := ws.Root()
	writeFile(t, root, "a.go", `package a

func Foo() {
	// needle
}
`)
	hits, err := SearchCode(ws, "needle", "", false, 10)
	if err != nil {
		t.Fatalf("SearchCode 失败: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("应命中 1 处，got %+v", hits)
	}
	h := hits[0]
	if h.Symbol != "Foo" || h.Kind != "func" || h.StartLine != 3 || h.EndLine != 5 {
		t.Fatalf("命中行未附带正确符号上下文: %+v", h)
	}
}

func TestSearchCodeFallbackWithoutSymbol(t *testing.T) {
	ws := setupWS(t)
	writeFile(t, ws.Root(), "note.unknownext", "needle here\n")
	hits, err := SearchCode(ws, "needle", "", false, 10)
	if err != nil {
		t.Fatalf("SearchCode 失败: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("应命中 1 处，got %+v", hits)
	}
	if hits[0].Symbol != "" || hits[0].Kind != "" {
		t.Fatalf("不支持索引的文件应降级为纯文本: %+v", hits[0])
	}
}

// ---------- git 只读封装 ----------

func TestGitReadOnlyWhitelist(t *testing.T) {
	ws := setupWS(t)
	// 非白名单子命令应被拒绝，不依赖 git 是否安装
	if _, err := GitRun(ws, []string{"push", "origin", "main"}); err == nil {
		t.Fatal("git push 应被拒绝")
	}
	if _, err := GitRun(ws, []string{"reset", "--hard"}); err == nil {
		t.Fatal("git reset 应被拒绝")
	}
}

func TestGitStatusInRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 未安装")
	}
	ws := setupWS(t)
	root := ws.Root()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v 失败: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	writeFile(t, root, "a.txt", "hello")
	run("add", ".")
	run("commit", "-m", "init")
	writeFile(t, root, "b.txt", "new file")

	out, err := GitRun(ws, []string{"status", "--porcelain"})
	if err != nil {
		t.Fatalf("GitRun 失败: %v", err)
	}
	if !strings.Contains(out, "b.txt") {
		t.Fatalf("status 应包含未跟踪的 b.txt:\n%s", out)
	}
}

func TestGitRunNoWorkspace(t *testing.T) {
	ws := workspace.New()
	if _, err := GitRun(ws, []string{"status"}); !errors.Is(err, workspace.ErrNoWorkspace) {
		t.Fatalf("未设置 workspace 应返回 ErrNoWorkspace，got %v", err)
	}
}
