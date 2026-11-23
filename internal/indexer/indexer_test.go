package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", `module example.com/demo

go 1.26

require (
	github.com/foo/bar v1.2.3
	golang.org/x/text v0.20.0
)

replace github.com/foo/bar => ../local/bar
`)
	write("main.go", `package main

import "fmt"

func main() {
	fmt.Println(Greet())
}
`)
	write("greet.go", `package main

// Greet 返回问候语。
func Greet() string {
	return "hi"
}
`)
	write("pkg/svc/service.go", `package svc

import "fmt"

// Service 业务接口。
type Service interface {
	Do() error
}

type Config struct {
	Name string
}

// Runner 实现适配。
type Runner struct{}

// Run 执行。
func (r *Runner) Run() error {
	fmt.Println("run")
	return nil
}

func helper() {}
`)
	return root
}

func TestParseGoMod(t *testing.T) {
	root := writeFixture(t)
	mi, err := ParseGoMod(root)
	if err != nil {
		t.Fatalf("ParseGoMod 失败: %v", err)
	}
	if mi.Module != "example.com/demo" {
		t.Fatalf("Module = %q", mi.Module)
	}
	if !strings.HasPrefix(mi.GoVersion, "1.") {
		t.Fatalf("GoVersion = %q", mi.GoVersion)
	}
	if len(mi.Deps) != 2 {
		t.Fatalf("Deps 数量 = %d, want 2: %+v", len(mi.Deps), mi.Deps)
	}
	if mi.Replaces["github.com/foo/bar"] != "../local/bar" {
		t.Fatalf("Replaces = %+v", mi.Replaces)
	}
}

func TestParseGoModMissing(t *testing.T) {
	if _, err := ParseGoMod(t.TempDir()); err == nil {
		t.Fatal("无 go.mod 应返回错误")
	}
}

func TestFindEntryPoints(t *testing.T) {
	root := writeFixture(t)
	eps, err := FindEntryPoints(root)
	if err != nil {
		t.Fatalf("FindEntryPoints 失败: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("应找到 1 个入口，got %d: %+v", len(eps), eps)
	}
	if filepath.Base(eps[0].File) != "main.go" || eps[0].Line != 5 {
		t.Fatalf("入口应为 main.go:5，got %+v", eps[0])
	}
}

func TestScanSymbols(t *testing.T) {
	root := writeFixture(t)
	syms, err := ScanSymbols(root)
	if err != nil {
		t.Fatalf("ScanSymbols 失败: %v", err)
	}
	get := func(name string) *Symbol {
		for i := range syms {
			if syms[i].Name == name {
				return &syms[i]
			}
		}
		return nil
	}

	greet := get("Greet")
	if greet == nil || greet.Kind != "func" || !strings.HasSuffix(greet.File, "greet.go") {
		t.Fatalf("Greet 符号不符: %+v", greet)
	}
	svc := get("Service")
	if svc == nil || svc.Kind != "interface" {
		t.Fatalf("Service 符号不符: %+v", svc)
	}
	cfg := get("Config")
	if cfg == nil || cfg.Kind != "struct" {
		t.Fatalf("Config 符号不符: %+v", cfg)
	}
	run := get("Run")
	if run == nil || run.Kind != "method" || run.Recv != "Runner" {
		t.Fatalf("Run 符号不符: %+v", run)
	}
	if get("helper") == nil {
		t.Fatal("应包含未导出函数 helper")
	}
}

func TestFindReferences(t *testing.T) {
	root := writeFixture(t)
	refs, err := FindReferences(root, "Greet", 50)
	if err != nil {
		t.Fatalf("FindReferences 失败: %v", err)
	}
	// main.go 中调用 fmt.Println(Greet())，greet.go 中为定义处
	if len(refs) < 2 {
		t.Fatalf("Greet 引用应 ≥2 处，got %d: %+v", len(refs), refs)
	}
	foundCall := false
	for _, r := range refs {
		if strings.HasSuffix(r.File, "main.go") && strings.Contains(r.Text, "Greet()") {
			foundCall = true
		}
	}
	if !foundCall {
		t.Fatalf("应包含 main.go 中的调用处: %+v", refs)
	}
}

func TestFindReferencesWordBoundary(t *testing.T) {
	root := writeFixture(t)
	// 词边界：查询 "Servic" 不应命中 "Service"（仅子串）
	refs, err := FindReferences(root, "Servic", 50)
	if err != nil {
		t.Fatalf("FindReferences 失败: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("部分词不应命中（词边界失效）: %+v", refs)
	}
	// 查询 "Run" 应命中方法与注释，但不能因 "Runner" 子串误报额外行
	refs, err = FindReferences(root, "Run", 50)
	if err != nil {
		t.Fatalf("FindReferences 失败: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("Run 应有命中")
	}
}
