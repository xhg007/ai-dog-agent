package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	m := New()
	if err := m.SetRoot(root); err != nil {
		t.Fatalf("SetRoot 失败: %v", err)
	}
	return m, root
}

func TestSetRootValid(t *testing.T) {
	m, root := newTestManager(t)
	if m.Root() != filepath.Clean(root) {
		t.Fatalf("Root() = %q, want %q", m.Root(), root)
	}
	if !m.IsSet() {
		t.Fatal("IsSet() = false, want true")
	}
}

func TestSetRootInvalid(t *testing.T) {
	m := New()
	if err := m.SetRoot(filepath.Join(t.TempDir(), "不存在")); err == nil {
		t.Fatal("SetRoot 对不存在的目录应返回错误")
	}
	if m.IsSet() {
		t.Fatal("SetRoot 失败后不应处于已设置状态")
	}
}

func TestSetRootNotDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New()
	if err := m.SetRoot(f); err == nil {
		t.Fatal("SetRoot 对文件路径应返回错误")
	}
}

func TestResolveRelativeInside(t *testing.T) {
	m, root := newTestManager(t)
	sub := filepath.Join(root, "pkg", "a.go")
	if err := os.MkdirAll(filepath.Dir(sub), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := m.Resolve("pkg/a.go")
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if got != sub {
		t.Fatalf("Resolve = %q, want %q", got, sub)
	}
}

func TestResolveDotIsRoot(t *testing.T) {
	m, root := newTestManager(t)
	got, err := m.Resolve(".")
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if got != filepath.Clean(root) {
		t.Fatalf("Resolve(.) = %q, want %q", got, root)
	}
}

func TestResolveEscapeRejected(t *testing.T) {
	m, _ := newTestManager(t)
	for _, p := range []string{"../outside", `..\outside`, "pkg/../../outside"} {
		if _, err := m.Resolve(p); !errors.Is(err, ErrOutsideWorkspace) {
			t.Fatalf("Resolve(%q) 应返回 ErrOutsideWorkspace，got %v", p, err)
		}
	}
}

func TestResolveAbsoluteInsideAllowed(t *testing.T) {
	m, root := newTestManager(t)
	got, err := m.Resolve(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("Resolve 绝对路径(在 workspace 内)失败: %v", err)
	}
	if got != filepath.Join(root, "main.go") {
		t.Fatalf("Resolve = %q", got)
	}
}

func TestResolveAbsoluteOutsideRejected(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Resolve(t.TempDir()); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("Resolve(workspace 外绝对路径) 应返回 ErrOutsideWorkspace，got %v", err)
	}
}

func TestResolveSymlinkEscapeRejected(t *testing.T) {
	m, root := newTestManager(t)
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("当前环境无权限创建符号链接，跳过")
	}
	if _, err := m.Resolve("escape"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("Resolve(符号链接逃逸) 应返回 ErrOutsideWorkspace，got %v", err)
	}
}

func TestResolveNoWorkspace(t *testing.T) {
	m := New()
	if _, err := m.Resolve("a.go"); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("未设置 workspace 时应返回 ErrNoWorkspace，got %v", err)
	}
}

func TestResolveNonExistPathStillWithin(t *testing.T) {
	// 目标文件不存在也应能解析出路径（供后续创建/报错判断使用）
	m, root := newTestManager(t)
	got, err := m.Resolve("not/exist.txt")
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if got != filepath.Join(root, "not", "exist.txt") {
		t.Fatalf("Resolve = %q", got)
	}
}
