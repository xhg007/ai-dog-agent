package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type wantSymbol struct {
	name string
	kind SymbolKind
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symbolNames(syms []Symbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, string(s.Kind)+" "+s.Name)
	}
	return out
}

func TestExtractSymbolsLanguages(t *testing.T) {
	fixtures := []struct {
		name string
		file string
		src  string
		want []wantSymbol
	}{
		{
			name: "go", file: "a.go",
			src: `package demo

type Server struct{}
type Handler interface{ Handle() }

func NewServer() *Server { return &Server{} }
func (s *Server) Start() {}
`,
			want: []wantSymbol{
				{"Server", KindStruct}, {"Handler", KindInterface},
				{"NewServer", KindFunc}, {"Start", KindMethod},
			},
		},
		{
			name: "typescript", file: "a.ts",
			src: `interface User { id: number }
type ID = string
class Repo { save() {} }
function load() {}
`,
			want: []wantSymbol{
				{"User", KindInterface}, {"ID", KindType},
				{"Repo", KindClass}, {"load", KindFunc}, {"save", KindMethod},
			},
		},
		{
			name: "tsx", file: "a.tsx",
			src: `class Repo { save() {} }
function load() {}
`,
			want: []wantSymbol{{"Repo", KindClass}, {"load", KindFunc}, {"save", KindMethod}},
		},
		{
			name: "javascript", file: "a.js",
			src: `class Repo { save() {} }
function load() {}
`,
			want: []wantSymbol{{"Repo", KindClass}, {"load", KindFunc}, {"save", KindMethod}},
		},
		{
			name: "python", file: "a.py",
			src: `class Repo:
    def save(self):
        pass

def load():
    pass
`,
			want: []wantSymbol{{"Repo", KindClass}, {"save", KindFunc}, {"load", KindFunc}},
		},
		{
			name: "rust", file: "a.rs",
			src: `struct Repo;
trait Store {}
enum Kind { A }
fn load() {}
`,
			want: []wantSymbol{
				{"Repo", KindStruct}, {"Store", KindTrait},
				{"Kind", KindEnum}, {"load", KindFunc},
			},
		},
		{
			name: "java", file: "A.java",
			src: `interface Repo {}
class Impl implements Repo { void save() {} }
`,
			want: []wantSymbol{{"Repo", KindInterface}, {"Impl", KindClass}, {"save", KindMethod}},
		},
		{
			name: "c", file: "a.c",
			src: `struct Point { int x; };
int add(int a, int b) { return a + b; }
`,
			want: []wantSymbol{{"Point", KindStruct}, {"add", KindFunc}},
		},
		{
			name: "cpp", file: "a.cpp",
			src: `class Repo { public: void save() {} };
int add(int a, int b) { return a + b; }
`,
			want: []wantSymbol{{"Repo", KindClass}, {"add", KindFunc}, {"save", KindFunc}},
		},
		{
			name: "csharp", file: "A.cs",
			src: `interface IRepo {}
class Repo : IRepo { void Save() {} }
`,
			want: []wantSymbol{{"IRepo", KindInterface}, {"Repo", KindClass}, {"Save", KindMethod}},
		},
		{
			name: "kotlin", file: "A.kt",
			src: `class Repo { fun save() {} }
fun load() {}
`,
			want: []wantSymbol{{"Repo", KindClass}, {"save", KindFunc}, {"load", KindFunc}},
		},
		{
			name: "swift", file: "A.swift",
			src: `protocol Repo {}
struct Impl: Repo {}
func load() {}
`,
			want: []wantSymbol{{"Repo", KindProtocol}, {"Impl", KindStruct}, {"load", KindFunc}},
		},
		{
			name: "ruby", file: "a.rb",
			src: `class Repo
  def save
  end
end

def load
end
`,
			want: []wantSymbol{{"Repo", KindClass}, {"save", KindMethod}, {"load", KindMethod}},
		},
		{
			name: "php", file: "a.php",
			src: `<?php
interface Repo {}
class Impl implements Repo { function save() {} }
function load() {}
`,
			want: []wantSymbol{
				{"Repo", KindInterface}, {"Impl", KindClass},
				{"save", KindMethod}, {"load", KindFunc},
			},
		},
		{
			name: "bash", file: "a.sh",
			src: `load() { echo hi; }
`,
			want: []wantSymbol{{"load", KindFunc}},
		},
	}

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, dir, f.file, f.src)
			idx := New(dir)
			if err := idx.EnsureFresh(context.Background()); err != nil {
				t.Fatalf("EnsureFresh: %v", err)
			}
			got := make(map[string]Symbol, len(idx.Symbols()))
			for _, s := range idx.Symbols() {
				got[s.Name] = s
			}
			for _, w := range f.want {
				s, ok := got[w.name]
				if !ok {
					t.Fatalf("%s: 缺少符号 %q，实际: %v", f.name, w.name, symbolNames(idx.Symbols()))
				}
				if s.Kind != w.kind {
					t.Fatalf("%s: 符号 %q kind=%s，期望 %s", f.name, w.name, s.Kind, w.kind)
				}
			}
		})
	}
}

func TestEnsureFreshIncremental(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "package a\nfunc A() {}\n")
	writeTestFile(t, dir, "b.py", "def b():\n    pass\n")

	ctx := context.Background()
	idx := New(dir)
	if err := idx.EnsureFresh(ctx); err != nil {
		t.Fatalf("首次索引: %v", err)
	}
	if got := len(idx.Symbols()); got < 2 {
		t.Fatalf("首次索引符号数 = %d，期望 >= 2", got)
	}

	time.Sleep(15 * time.Millisecond)
	writeTestFile(t, dir, "a.go", "package a\nfunc A() {}\nfunc B() {}\n")
	idx.Invalidate()
	if err := idx.EnsureFresh(ctx); err != nil {
		t.Fatalf("增量索引: %v", err)
	}
	names := map[string]bool{}
	for _, s := range idx.Symbols() {
		names[s.Name] = true
	}
	if !names["B"] || !names["b"] {
		t.Fatalf("增量索引应保留未变文件并加入新符号，实际: %v", symbolNames(idx.Symbols()))
	}

	if err := os.Remove(filepath.Join(dir, "b.py")); err != nil {
		t.Fatal(err)
	}
	idx.Invalidate()
	if err := idx.EnsureFresh(ctx); err != nil {
		t.Fatalf("删除后索引: %v", err)
	}
	for _, s := range idx.Symbols() {
		if s.Name == "b" {
			t.Fatalf("已删除文件的符号仍在索引中: %v", symbolNames(idx.Symbols()))
		}
	}
}

func TestEnsureFreshSkips(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "node_modules/x.js", "function ignored() {}\n")
	writeTestFile(t, dir, "vendor/y.go", "package y\nfunc Y() {}\n")
	writeTestFile(t, dir, "note.txt", "not code")
	writeTestFile(t, dir, "big.go", "package big\n"+strings.Repeat("// padding\n", 120000))
	writeTestFile(t, dir, "bad.go", "\x00\x01\x02 not go source")

	idx := New(dir)
	if err := idx.EnsureFresh(context.Background()); err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}
	stats := idx.Stats()
	if stats.Files != 1 {
		t.Fatalf("Files = %d，期望仅 bad.go（忽略目录/大文件/非代码扩展名）", stats.Files)
	}
	if stats.Skipped == 0 {
		t.Fatal("大文件/解析失败应计入 Skipped")
	}
}

func TestSearchSymbolsAndSymbolAt(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", `package a

type Server struct{}

func (s *Server) Start() {}
`)
	idx := New(dir)
	if err := idx.EnsureFresh(context.Background()); err != nil {
		t.Fatalf("EnsureFresh: %v", err)
	}

	hits := idx.SearchSymbols("serv", "", "", 10)
	if len(hits) != 1 || hits[0].Name != "Server" {
		t.Fatalf("SearchSymbols(serv) = %v", symbolNames(hits))
	}
	funcs := idx.SearchSymbols("", KindMethod, "go", 10)
	if len(funcs) != 1 || funcs[0].Name != "Start" || funcs[0].Recv != "Server" {
		t.Fatalf("SearchSymbols(method/go) = %v", symbolNames(funcs))
	}

	sym := idx.SymbolAt("a.go", 5)
	if sym == nil || sym.Name != "Start" {
		t.Fatalf("SymbolAt(a.go:5) = %+v", sym)
	}
	if sym := idx.SymbolAt("a.go", 1); sym != nil {
		t.Fatalf("a.go:1 不应落在符号内，实际 %+v", sym)
	}
}

func TestForWorkspaceShared(t *testing.T) {
	root := t.TempDir()
	a := ForWorkspace(root)
	b := ForWorkspace(root)
	if a != b {
		t.Fatal("同一 root 应返回同一个索引实例")
	}
	Release(root)
	c := ForWorkspace(root)
	if c == a {
		t.Fatal("Release 后应创建新实例")
	}
	Release(root)
}
