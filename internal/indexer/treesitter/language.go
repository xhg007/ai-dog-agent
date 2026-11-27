// Package treesitter 多语言代码索引：用 tree-sitter 提取符号，供检索工具与项目摘要使用。
package treesitter

// 语言表：扩展名 → grammar 与符号 node kind 映射。
// 具体 node kind 以各语言 fixture 测试校准；此处给出初始集合。

import (
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/swift"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	ts "github.com/smacker/go-tree-sitter/typescript/typescript"
)

// SymbolKind 符号类型（跨语言统一）。
type SymbolKind string

const (
	KindFunc      SymbolKind = "func"
	KindMethod    SymbolKind = "method"
	KindClass     SymbolKind = "class"
	KindInterface SymbolKind = "interface"
	KindStruct    SymbolKind = "struct"
	KindType      SymbolKind = "type"
	KindEnum      SymbolKind = "enum"
	KindTrait     SymbolKind = "trait"
	KindProtocol  SymbolKind = "protocol"
	KindModule    SymbolKind = "module"
)

// Symbol 索引中的一个符号。Line/StartLine/EndLine 均为 1-based。
type Symbol struct {
	Name      string     `json:"name"`
	Kind      SymbolKind `json:"kind"`
	Recv      string     `json:"recv,omitempty"`
	Language  string     `json:"language"`
	File      string     `json:"file"`
	Line      int        `json:"line"`
	StartLine int        `json:"startLine"`
	EndLine   int        `json:"endLine"`
}

// languageSpec 一种语言的索引规则。
type languageSpec struct {
	Name     string
	Language func() *sitter.Language
	Exts     []string
	// Kinds: tree-sitter node type → 符号类型。
	Kinds map[string]SymbolKind
	// Declarator: 函数名不在 name 字段，而在 declarator 链中（C/C++）。
	Declarator bool
}

var tsKinds = map[string]SymbolKind{
	"function_declaration":   KindFunc,
	"method_definition":      KindMethod,
	"class_declaration":      KindClass,
	"interface_declaration":  KindInterface,
	"type_alias_declaration": KindType,
	"enum_declaration":       KindEnum,
}

var specs = []*languageSpec{
	{
		Name:     "go",
		Language: golang.GetLanguage,
		Exts:     []string{".go"},
		Kinds: map[string]SymbolKind{
			"function_declaration": KindFunc,
			"method_declaration":   KindMethod,
			"type_spec":            KindType, // struct/interface 在 symbols.go 中细化
		},
	},
	{Name: "typescript", Language: ts.GetLanguage, Exts: []string{".ts"}, Kinds: tsKinds},
	{Name: "tsx", Language: tsx.GetLanguage, Exts: []string{".tsx"}, Kinds: tsKinds},
	{
		Name:     "javascript",
		Language: javascript.GetLanguage,
		Exts:     []string{".js", ".jsx", ".mjs", ".cjs"},
		Kinds:    tsKinds,
	},
	{
		Name:     "python",
		Language: python.GetLanguage,
		Exts:     []string{".py"},
		Kinds: map[string]SymbolKind{
			"function_definition": KindFunc,
			"class_definition":    KindClass,
		},
	},
	{
		Name:     "rust",
		Language: rust.GetLanguage,
		Exts:     []string{".rs"},
		Kinds: map[string]SymbolKind{
			"function_item": KindFunc,
			"struct_item":   KindStruct,
			"trait_item":    KindTrait,
			"enum_item":     KindEnum,
			"type_item":     KindType,
		},
	},
	{
		Name:     "java",
		Language: java.GetLanguage,
		Exts:     []string{".java"},
		Kinds: map[string]SymbolKind{
			"method_declaration":    KindMethod,
			"class_declaration":     KindClass,
			"interface_declaration": KindInterface,
			"enum_declaration":      KindEnum,
			"record_declaration":    KindClass,
		},
	},
	{
		Name:       "c",
		Language:   c.GetLanguage,
		Exts:       []string{".c", ".h"},
		Declarator: true,
		Kinds: map[string]SymbolKind{
			"function_definition": KindFunc,
			"struct_specifier":    KindStruct,
			"union_specifier":     KindStruct,
			"enum_specifier":      KindEnum,
			"type_definition":     KindType,
		},
	},
	{
		Name:       "cpp",
		Language:   cpp.GetLanguage,
		Exts:       []string{".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"},
		Declarator: true,
		Kinds: map[string]SymbolKind{
			"function_definition":  KindFunc,
			"class_specifier":      KindClass,
			"struct_specifier":     KindStruct,
			"namespace_definition": KindModule,
			"enum_specifier":       KindEnum,
		},
	},
	{
		Name:     "csharp",
		Language: csharp.GetLanguage,
		Exts:     []string{".cs"},
		Kinds: map[string]SymbolKind{
			"method_declaration":    KindMethod,
			"class_declaration":     KindClass,
			"interface_declaration": KindInterface,
			"struct_declaration":    KindStruct,
			"enum_declaration":      KindEnum,
			"record_declaration":    KindClass,
		},
	},
	{
		Name:     "kotlin",
		Language: kotlin.GetLanguage,
		Exts:     []string{".kt", ".kts"},
		Kinds: map[string]SymbolKind{
			"function_declaration": KindFunc,
			"class_declaration":    KindClass, // interface 也是 class_declaration
			"object_declaration":   KindClass,
		},
	},
	{
		Name:     "swift",
		Language: swift.GetLanguage,
		Exts:     []string{".swift"},
		Kinds: map[string]SymbolKind{
			"function_declaration":  KindFunc,
			"class_declaration":     KindClass,
			"struct_declaration":    KindStruct,
			"protocol_declaration":  KindProtocol,
			"enum_declaration":      KindEnum,
			"extension_declaration": KindClass,
		},
	},
	{
		Name:     "ruby",
		Language: ruby.GetLanguage,
		Exts:     []string{".rb", ".rake"},
		Kinds: map[string]SymbolKind{
			"method":           KindMethod,
			"singleton_method": KindMethod,
			"class":            KindClass,
			"module":           KindModule,
		},
	},
	{
		Name:     "php",
		Language: php.GetLanguage,
		Exts:     []string{".php"},
		Kinds: map[string]SymbolKind{
			"function_definition":   KindFunc,
			"method_declaration":    KindMethod,
			"class_declaration":     KindClass,
			"interface_declaration": KindInterface,
			"trait_declaration":     KindTrait,
		},
	},
	{
		Name:     "bash",
		Language: bash.GetLanguage,
		Exts:     []string{".sh", ".bash", ".zsh", ".ksh"},
		Kinds: map[string]SymbolKind{
			"function_definition": KindFunc,
		},
	},
}

// byExt 扩展名（小写，含点）→ 语言。
var byExt = map[string]*languageSpec{}

func init() {
	for _, spec := range specs {
		for _, ext := range spec.Exts {
			byExt[ext] = spec
		}
	}
}

// specForFile 按扩展名返回语言规则；不支持时返回 nil。
func specForFile(path string) *languageSpec {
	return byExt[strings.ToLower(filepath.Ext(path))]
}

// supportedExt 判断扩展名是否受支持。
func supportedExt(path string) bool {
	return specForFile(path) != nil
}
