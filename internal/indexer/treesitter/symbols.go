package treesitter

// 符号提取：解析源码 AST，按 languageSpec 的 node kind 映射收集符号。
// 单文件解析失败返回空结果，不向上报错（索引整体不应被单个坏文件拖垮）。

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

const maxDeclaratorDepth = 16

// extractSymbols 解析 src 并提取符号；rel 为相对路径（写入 Symbol.File）。
func extractSymbols(ctx context.Context, spec *languageSpec, rel string, src []byte) []Symbol {
	if spec == nil || len(src) == 0 {
		return nil
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(spec.Language())

	tree, err := parser.ParseCtx(ctx, nil, src)
	if err != nil || tree == nil {
		return nil
	}
	// smacker 旧绑定没有 Tree.Close()，依赖 GC；索引通过并发与文件大小上限控制峰值。
	root := tree.RootNode()
	if root == nil {
		return nil
	}
	var out []Symbol
	walkSymbols(root, spec, rel, src, &out)
	return out
}

func walkSymbols(n *sitter.Node, spec *languageSpec, rel string, src []byte, out *[]Symbol) {
	if n == nil {
		return
	}
	nodeType := n.Type()
	if kind, ok := spec.Kinds[nodeType]; ok {
		k := kind
		if spec.Name == "go" && nodeType == "type_spec" {
			if t := n.ChildByFieldName("type"); t != nil {
				switch t.Type() {
				case "struct_type":
					k = KindStruct
				case "interface_type":
					k = KindInterface
				}
			}
		}
		// Swift 的 struct 复用 class_declaration；Kotlin 的 interface 也走 class_declaration。
		if spec.Name == "swift" && nodeType == "class_declaration" {
			if strings.HasPrefix(strings.TrimSpace(n.Content(src)), "struct ") {
				k = KindStruct
			}
		}
		if spec.Name == "kotlin" && nodeType == "class_declaration" {
			if strings.HasPrefix(strings.TrimSpace(n.Content(src)), "interface ") {
				k = KindInterface
			}
		}
		if name := symbolName(n, spec, src); name != "" {
			start, end := n.StartPoint(), n.EndPoint()
			sym := Symbol{
				Name:      name,
				Kind:      k,
				Language:  spec.Name,
				File:      rel,
				Line:      int(start.Row) + 1,
				StartLine: int(start.Row) + 1,
				EndLine:   int(end.Row) + 1,
			}
			if spec.Name == "go" && nodeType == "method_declaration" {
				sym.Recv = goReceiverName(n, src)
			}
			*out = append(*out, sym)
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walkSymbols(n.NamedChild(i), spec, rel, src, out)
	}
}

// symbolName 提取符号名称：一般取 name 字段；C/C++ 的函数名在 declarator 链中。
func symbolName(n *sitter.Node, spec *languageSpec, src []byte) string {
	if spec.Declarator && (n.Type() == "function_definition" || n.Type() == "type_definition") {
		if name := declaratorName(n, src); name != "" {
			return name
		}
	}
	if f := n.ChildByFieldName("name"); f != nil {
		return strings.TrimSpace(f.Content(src))
	}
	// 部分 grammar（如 Kotlin）没有 name 字段：取第一个标识符子节点。
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "identifier", "simple_identifier", "type_identifier", "field_identifier":
			return strings.TrimSpace(c.Content(src))
		}
	}
	return ""
}

// declaratorName 沿 declarator 字段向下找到标识符（C/C++ 函数声明器）。
func declaratorName(n *sitter.Node, src []byte) string {
	cur := n.ChildByFieldName("declarator")
	for depth := 0; cur != nil && depth < maxDeclaratorDepth; depth++ {
		switch cur.Type() {
		case "identifier", "field_identifier", "type_identifier", "operator_name", "destructor_name":
			return strings.TrimSpace(cur.Content(src))
		}
		next := cur.ChildByFieldName("declarator")
		if next == nil {
			next = lastIdentifier(cur)
		}
		cur = next
	}
	return ""
}

// lastIdentifier 在子树中查找最后一个标识符（declarator 结构的兜底）。
func lastIdentifier(n *sitter.Node) *sitter.Node {
	if n == nil {
		return nil
	}
	var found *sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Type() {
		case "identifier", "field_identifier", "type_identifier":
			found = c
		default:
			if deeper := lastIdentifier(c); deeper != nil {
				found = deeper
			}
		}
	}
	return found
}

// goReceiverName 提取 Go 方法接收者类型名（去掉指针与泛型参数）。
func goReceiverName(n *sitter.Node, src []byte) string {
	recv := n.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
	for i := 0; i < int(recv.NamedChildCount()); i++ {
		p := recv.NamedChild(i)
		if p == nil {
			continue
		}
		if t := p.ChildByFieldName("type"); t != nil {
			return normalizeGoRecv(t.Content(src))
		}
	}
	return ""
}

func normalizeGoRecv(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "*")
	if i := strings.IndexByte(s, '['); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
