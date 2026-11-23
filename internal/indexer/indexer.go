// Package indexer 项目索引：go.mod 解析、入口检测、go/ast 符号表、正则引用检索。
//
// 实现说明（M2 决策）：目标场景以 Go 项目为主，符号提取直接使用标准库
// go/parser + go/ast（精确、无 CGO 依赖）；tree-sitter 作为多语言支持
// 延后到 M6。引用分析按用户决策使用正则 + 词边界匹配。
package indexer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
)

// ignoreDirs 遍历时忽略的目录。
var ignoreDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "vendor": true,
	"__pycache__": true, ".idea": true, ".vscode": true, "bin": true,
}

// ModuleInfo go.mod 解析结果。
type ModuleInfo struct {
	Module    string            `json:"module"`
	GoVersion string            `json:"goVersion"`
	Deps      []string          `json:"deps"`    // "path version"
	Replaces  map[string]string `json:"replaces"`
}

// EntryPoint 程序入口。
type EntryPoint struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// Symbol 代码符号。
type Symbol struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // func / method / struct / interface / type
	Recv string `json:"recv,omitempty"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// Reference 引用命中。
type Reference struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// ParseGoMod 解析 workspace 根目录的 go.mod。
func ParseGoMod(root string) (*ModuleInfo, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("读取 go.mod 失败: %w", err)
	}
	mf, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("解析 go.mod 失败: %w", err)
	}
	mi := &ModuleInfo{
		Module:    mf.Module.Mod.Path,
		GoVersion: mf.Go.Version,
		Replaces:  map[string]string{},
	}
	for _, r := range mf.Require {
		mi.Deps = append(mi.Deps, fmt.Sprintf("%s %s", r.Mod.Path, r.Mod.Version))
	}
	for _, r := range mf.Replace {
		mi.Replaces[r.Old.Path] = r.New.Path
	}
	return mi, nil
}

// walkGoFiles 遍历 root 下所有 .go 文件（忽略 vendor 等）。
func walkGoFiles(root string, fn func(path string)) {
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			fn(p)
		}
		return nil
	})
}

// FindEntryPoints 定位入口：package main 且包含 func main()。
func FindEntryPoints(root string) ([]EntryPoint, error) {
	var eps []EntryPoint
	walkGoFiles(root, func(p string) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil || f.Name.Name != "main" {
			return
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if ok && fd.Name.Name == "main" && fd.Recv == nil {
				eps = append(eps, EntryPoint{
					File: relTo(root, p),
					Line: fset.Position(fd.Pos()).Line,
				})
			}
		}
	})
	return eps, nil
}

// ScanSymbols 扫描全项目符号表（函数/方法/结构体/接口/类型别名）。
func ScanSymbols(root string) ([]Symbol, error) {
	var syms []Symbol
	walkGoFiles(root, func(p string) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return
		}
		rel := relTo(root, p)
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				s := Symbol{Name: d.Name.Name, File: rel, Line: fset.Position(d.Pos()).Line}
				if d.Recv != nil {
					s.Kind = "method"
					s.Recv = recvTypeName(d.Recv)
				} else {
					s.Kind = "func"
				}
				syms = append(syms, s)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					s := Symbol{Name: ts.Name.Name, File: rel, Line: fset.Position(ts.Pos()).Line}
					switch ts.Type.(type) {
					case *ast.StructType:
						s.Kind = "struct"
					case *ast.InterfaceType:
						s.Kind = "interface"
					default:
						s.Kind = "type"
					}
					syms = append(syms, s)
				}
			}
		}
	})
	return syms, nil
}

func recvTypeName(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// FindReferences 以词边界正则在 .go 文件中检索标识符引用。
func FindReferences(root, name string, max int) ([]Reference, error) {
	if max <= 0 {
		max = 100
	}
	re, err := regexp.Compile(`\b` + regexp.QuoteMeta(name) + `\b`)
	if err != nil {
		return nil, fmt.Errorf("非法标识符: %w", err)
	}
	var refs []Reference
	walkGoFiles(root, func(p string) {
		if len(refs) >= max {
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return
		}
		rel := relTo(root, p)
		fset := token.NewFileSet()
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				refs = append(refs, Reference{File: rel, Line: i + 1, Text: line})
				if len(refs) >= max {
					return
				}
			}
		}
		_ = fset
	})
	return refs, nil
}

// relTo 返回 p 相对 root 的路径。
func relTo(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}
