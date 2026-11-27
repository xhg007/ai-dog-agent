// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/app/ast.go — Go 包的依赖、导出声明与调用图概览。
package devtools

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

const declTypeFunction = "function"

// Declaration 导出声明的描述。
type Declaration struct {
	Name string
	Type string // function / type / interface / struct / const / var
	Info string
	Line int
}

// DeclarationExtractResult 单个文件的声明列表。
type DeclarationExtractResult struct {
	Filename     string
	Declarations []Declaration
}

// FunctionCall 一次函数调用。
type FunctionCall struct {
	Name    string
	Package string
}

// CallGraphEntry 函数及其调用的其他函数。
type CallGraphEntry struct {
	Function string
	Calls    []FunctionCall
}

// CallGraphResult 单个文件的调用图。
type CallGraphResult struct {
	Filename  string
	CallGraph []CallGraphEntry
}

// PackageImport 一条 import。
type PackageImport struct {
	Path     string
	Alias    string
	Line     int
	IsStdlib bool
	IsLocal  bool
}

// PackageDependency 单个 Go 文件的包名与导入。
type PackageDependency struct {
	FilePath    string
	PackageName string
	Imports     []PackageImport
}

// DependencyGraphResult 目录级依赖图。
type DependencyGraphResult struct {
	ProjectPath  string
	ModuleName   string
	Dependencies []PackageDependency
}

// OutlineGoPackageOptions 控制输出包含哪些区块。
type OutlineGoPackageOptions struct {
	SkipDependencies bool
	SkipDeclarations bool
	SkipCallGraph    bool
}

// ExtractDeclarations 提取目录下所有 Go 文件（跳过 _test.go）的导出声明。
func ExtractDeclarations(root string) []DeclarationExtractResult {
	var results []DeclarationExtractResult
	_ = walkFiles(root, ".go", func(filePath string) error {
		declarations := extractDeclarationsFromFile(filePath)
		if len(declarations) > 0 {
			results = append(results, DeclarationExtractResult{Filename: filePath, Declarations: declarations})
		}
		return nil
	})
	return results
}

func extractDeclarationsFromFile(filePath string) []Declaration {
	if strings.HasSuffix(filePath, "_test.go") {
		return nil
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil
	}

	var declarations []Declaration
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						declarations = append(declarations, Declaration{
							Name: s.Name.Name,
							Type: getTypeSpecType(s),
							Info: getTypeSpecInfo(s),
							Line: fset.Position(s.Pos()).Line,
						})
					}
				case *ast.ValueSpec:
					declType := "var"
					if d.Tok == token.CONST {
						declType = "const"
					}
					for _, name := range s.Names {
						if name.IsExported() {
							declarations = append(declarations, Declaration{
								Name: name.Name,
								Type: declType,
								Info: getValueSpecInfo(s),
								Line: fset.Position(name.Pos()).Line,
							})
						}
					}
				}
			}
		case *ast.FuncDecl:
			if d.Name == nil || !d.Name.IsExported() {
				continue
			}
			name := d.Name.Name
			info := declTypeFunction
			if d.Recv != nil {
				receiverType := getReceiverType(d.Recv)
				name = receiverType + "." + d.Name.Name
				info = "method on " + receiverType
			}
			declarations = append(declarations, Declaration{
				Name: name, Type: declTypeFunction, Info: info,
				Line: fset.Position(d.Pos()).Line,
			})
		}
	}
	return declarations
}

func getReceiverType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return "*" + ident.Name
		}
	}
	return "Unknown"
}

func getTypeSpecType(spec *ast.TypeSpec) string {
	switch spec.Type.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return "type"
	}
}

func getTypeSpecInfo(spec *ast.TypeSpec) string {
	switch t := spec.Type.(type) {
	case *ast.StructType:
		if n := len(t.Fields.List); n == 1 {
			return "1 field"
		} else {
			return fmt.Sprintf("%d fields", n)
		}
	case *ast.InterfaceType:
		if n := len(t.Methods.List); n == 1 {
			return "1 method"
		} else {
			return fmt.Sprintf("%d methods", n)
		}
	case *ast.Ident:
		return "alias to " + t.Name
	case *ast.ArrayType:
		return "array type"
	case *ast.MapType:
		return "map type"
	case *ast.ChanType:
		return "channel type"
	case *ast.FuncType:
		return "function type"
	default:
		return "custom type"
	}
}

func getValueSpecInfo(spec *ast.ValueSpec) string {
	if spec.Type != nil {
		return getTypeName(spec.Type)
	}
	if len(spec.Values) > 0 {
		return "inferred type"
	}
	return "no type info"
}

func getTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if pkg, ok := t.X.(*ast.Ident); ok {
			return pkg.Name + "." + t.Sel.Name
		}
		return "qualified type"
	case *ast.ArrayType:
		return "[]" + getTypeName(t.Elt)
	case *ast.MapType:
		return "map[" + getTypeName(t.Key) + "]" + getTypeName(t.Value)
	case *ast.StarExpr:
		return "*" + getTypeName(t.X)
	case *ast.ChanType:
		return "chan " + getTypeName(t.Value)
	default:
		return "complex type"
	}
}

// ExtractCallGraph 提取单个 Go 文件的导出函数调用关系。
func ExtractCallGraph(filePath string) (*CallGraphResult, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	result := &CallGraphResult{Filename: filePath, CallGraph: []CallGraphEntry{}}
	ast.Inspect(node, func(n ast.Node) bool {
		if funcDecl, ok := n.(*ast.FuncDecl); ok && funcDecl.Name != nil && funcDecl.Name.IsExported() {
			result.CallGraph = append(result.CallGraph, CallGraphEntry{
				Function: getFunctionSignature(funcDecl),
				Calls:    extractFunctionCalls(funcDecl),
			})
		}
		return true
	})
	return result, nil
}

func getFunctionSignature(funcDecl *ast.FuncDecl) string {
	if funcDecl.Recv != nil {
		return getReceiverType(funcDecl.Recv) + "." + funcDecl.Name.Name
	}
	return funcDecl.Name.Name
}

func extractFunctionCalls(funcDecl *ast.FuncDecl) []FunctionCall {
	callMap := make(map[string]FunctionCall)
	if funcDecl.Body == nil {
		return []FunctionCall{}
	}
	ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
		if callExpr, ok := n.(*ast.CallExpr); ok {
			call := analyzeFunctionCall(callExpr)
			if call.Name != "" {
				callMap[call.Package+"."+call.Name] = call
			}
		}
		return true
	})
	calls := make([]FunctionCall, 0, len(callMap))
	for _, call := range callMap {
		calls = append(calls, call)
	}
	return calls
}

func analyzeFunctionCall(callExpr *ast.CallExpr) FunctionCall {
	switch fun := callExpr.Fun.(type) {
	case *ast.Ident:
		return FunctionCall{Name: fun.Name}
	case *ast.SelectorExpr:
		if ident, ok := fun.X.(*ast.Ident); ok {
			return FunctionCall{Name: fun.Sel.Name, Package: ident.Name}
		}
	}
	return FunctionCall{}
}

// ExtractPackageDependencies 分析目录下 Go 文件的包名与导入依赖。
func ExtractPackageDependencies(root string) *DependencyGraphResult {
	result := &DependencyGraphResult{ProjectPath: root, Dependencies: []PackageDependency{}}
	moduleName, err := findModuleName(root)
	if err != nil {
		moduleName = filepath.Base(root)
	}
	result.ModuleName = moduleName

	_ = walkFiles(root, ".go", func(filePath string) error {
		dep, err := extractPackageDependencyFromFile(filePath, moduleName)
		if err == nil && dep != nil {
			result.Dependencies = append(result.Dependencies, *dep)
		}
		return nil
	})
	return result
}

func findModuleName(projectPath string) (string, error) {
	content, err := os.ReadFile(filepath.Join(projectPath, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}
	return "", fmt.Errorf("go.mod 中未找到 module 声明")
}

func extractPackageDependencyFromFile(filePath, moduleName string) (*PackageDependency, error) {
	if strings.HasSuffix(filePath, "_test.go") {
		return nil, nil
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		absFilePath = filePath
	}
	dep := &PackageDependency{FilePath: absFilePath, PackageName: node.Name.Name, Imports: []PackageImport{}}
	for _, imp := range node.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		dep.Imports = append(dep.Imports, PackageImport{
			Path:     importPath,
			Alias:    alias,
			Line:     fset.Position(imp.Pos()).Line,
			IsStdlib: isStandardLibrary(importPath),
			IsLocal:  strings.HasPrefix(importPath, moduleName),
		})
	}
	return dep, nil
}

func isStandardLibrary(importPath string) bool {
	if !strings.Contains(importPath, ".") {
		return true
	}
	for _, prefix := range []string{"golang.org/x/", "vendor/golang.org/x/"} {
		if strings.HasPrefix(importPath, prefix) {
			return true
		}
	}
	return false
}

// OutlineGoPackage 生成 Go 包概览：依赖、导出声明与调用图。
func OutlineGoPackage(root string, opts OutlineGoPackageOptions) (string, error) {
	var sb strings.Builder
	depResult := ExtractPackageDependencies(root)

	fmt.Fprintf(&sb, "Go 包概览: %s\n", root)
	fmt.Fprintf(&sb, "Module: %s\n\n", depResult.ModuleName)

	if !opts.SkipDependencies {
		sb.WriteString("== 依赖 ==\n")
		formatDepsRelative(&sb, depResult, root)
	}

	if !opts.SkipDeclarations {
		declResults := ExtractDeclarations(root)
		sb.WriteString("== 导出声明 ==\n")
		if len(declResults) == 0 {
			sb.WriteString("未找到导出声明。\n")
		}
		for _, result := range declResults {
			fmt.Fprintf(&sb, "File: %s\n", relOrAbs(root, result.Filename))
			for _, decl := range result.Declarations {
				if decl.Info != "" {
					fmt.Fprintf(&sb, "- %s: %s (%s) [line %d]\n", decl.Type, decl.Name, decl.Info, decl.Line)
				} else {
					fmt.Fprintf(&sb, "- %s: %s [line %d]\n", decl.Type, decl.Name, decl.Line)
				}
			}
		}
		sb.WriteString("\n")
	}

	if !opts.SkipCallGraph {
		sb.WriteString("== 调用图 ==\n")
		callGraphCount := 0
		_ = walkFiles(root, ".go", func(filePath string) error {
			if strings.HasSuffix(filePath, "_test.go") {
				return nil
			}
			result, err := ExtractCallGraph(filePath)
			if err != nil || len(result.CallGraph) == 0 {
				return nil
			}
			callGraphCount++
			fmt.Fprintf(&sb, "File: %s\n", relOrAbs(root, result.Filename))
			for _, entry := range result.CallGraph {
				calls := filterCallGraphNoise(entry.Calls)
				if len(calls) == 0 {
					continue
				}
				fmt.Fprintf(&sb, "  %s\n", entry.Function)
				for _, call := range calls {
					if call.Package != "" {
						fmt.Fprintf(&sb, "    -> %s.%s\n", call.Package, call.Name)
					} else {
						fmt.Fprintf(&sb, "    -> %s\n", call.Name)
					}
				}
			}
			return nil
		})
		if callGraphCount == 0 {
			sb.WriteString("未找到导出函数的调用。\n")
		}
	}
	return sb.String(), nil
}

func relOrAbs(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}

func formatDepsRelative(sb *strings.Builder, result *DependencyGraphResult, root string) {
	if len(result.Dependencies) == 0 {
		sb.WriteString("未找到包含 import 的 Go 文件。\n")
		return
	}
	for _, dep := range result.Dependencies {
		if len(dep.Imports) == 0 {
			continue
		}
		fmt.Fprintf(sb, "%s (package %s)\n", relOrAbs(root, dep.FilePath), dep.PackageName)
		var local, stdlib, external []PackageImport
		for _, imp := range dep.Imports {
			switch {
			case imp.IsLocal:
				local = append(local, imp)
			case imp.IsStdlib:
				stdlib = append(stdlib, imp)
			default:
				external = append(external, imp)
			}
		}
		writeImportGroup(sb, "  Local: ", local)
		writeImportGroup(sb, "  External: ", external)
		writeImportGroup(sb, "  Stdlib: ", stdlib)
	}
	sb.WriteString("\n")
}

func writeImportGroup(sb *strings.Builder, label string, imports []PackageImport) {
	if len(imports) == 0 {
		return
	}
	sb.WriteString(label)
	for i, imp := range imports {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(imp.Path)
	}
	sb.WriteString("\n")
}

var callGraphNoisePackages = map[string]bool{
	"fmt": true, "strings": true, "strconv": true, "bytes": true,
	"sort": true, "slices": true, "maps": true,
	"log": true, "slog": true,
}

var callGraphNoiseBuiltins = map[string]bool{
	"len": true, "cap": true, "make": true, "new": true,
	"append": true, "copy": true, "delete": true, "close": true,
	"panic": true, "recover": true, "print": true, "println": true,
	"error": true,
}

func filterCallGraphNoise(calls []FunctionCall) []FunctionCall {
	var filtered []FunctionCall
	for _, c := range calls {
		if c.Package == "" && callGraphNoiseBuiltins[c.Name] {
			continue
		}
		if callGraphNoisePackages[c.Package] {
			continue
		}
		if c.Package != "" && len(c.Package) <= 2 {
			continue
		}
		if c.Package == "" && len(c.Name) > 0 && c.Name[0] >= 'a' && c.Name[0] <= 'z' {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
}
