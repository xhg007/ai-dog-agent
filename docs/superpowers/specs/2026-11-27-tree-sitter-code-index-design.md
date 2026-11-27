# Tree-sitter 多语言代码索引设计

日期：2026-11-27
状态：已实现（2026-11-27）

## 意图与原因

当前 `search_code` 是逐行文本/正则匹配，`internal/indexer` 的符号表只解析 Go，且只用于项目摘要。
Agent 在非 Go 项目里只能靠字符串猜结构，无法稳定回答“这个函数在哪”“这个类有哪些方法”“这行属于哪个函数”。

本设计引入 tree-sitter，把每个源文件解析成语法树，建立跨语言符号索引，并让检索工具具备结构感知能力。

## 目标

- 覆盖 14 种语言：Go、TypeScript、TSX、JavaScript、Python、Rust、Java、C、C++、C#、Kotlin、Swift、Ruby、PHP、Bash。
- 新增 `search_symbols` 工具：按名称/类型检索函数、方法、类、接口、结构体等符号。
- `search_code` 命中行附带所属符号与代码块起止行。
- `BuildProjectContext` 的符号概览从 Go-only 扩展为多语言。
- 按需扫描 + mtime 缓存，文件变更后增量更新。
- 允许正常执行 `go mod tidy`（不再为依赖体积做手工 go.mod 维护）。

## 非目标

- 不做类型推导、跨文件引用解析、调用图或语义高亮。
- 不改前端 UI；能力只通过 Agent 工具与项目摘要暴露。
- 不替换 `go.mod` 解析、Go 入口检测（继续使用 `go/ast` 与 `x/mod`）。
- 不做文件系统监听（fsnotify）；刷新由检索调用触发，按 mtime 判断。
- 不索引依赖目录、构建产物、二进制或超大文件。

## 反目标（会让这次改动算失败）

- 索引让对话明显变慢（例如每次检索都全量重新解析）。
- 解析失败导致检索报错或阻塞；单个文件失败必须被跳过。
- 内存无上限增长（大仓库索引失控）。
- 引入需要用户手工维护 go.mod 的依赖组合。

## 约束

- Windows 优先，CGO 已启用（DuckDB 动态库模式）。
- 依赖固定为 `github.com/smacker/go-tree-sitter`（单模块，含全部语言子包），可正常 `go mod tidy`。
- 所有语言 grammar 编译进主二进制；预计增加 20–40MB，需实测。
- `go mod tidy` 会把 `go.duckdb.org/libduckdb/*` 重新写回 go.mod，这是已接受的代价；不再要求手工维护。
- 索引只在 workspace 已设置时启用。

## 实现方案

### 选择：smacker/go-tree-sitter（单模块）

- 一个模块覆盖全部目标语言，`go mod tidy` 可正常解析依赖。
- 语言子包路径：
  - `github.com/smacker/go-tree-sitter/golang`
  - `github.com/smacker/go-tree-sitter/typescript/typescript`
  - `github.com/smacker/go-tree-sitter/typescript/tsx`
  - `github.com/smacker/go-tree-sitter/javascript`
  - `.../python`、`.../rust`、`.../java`、`.../c`、`.../cpp`
  - `.../csharp`、`.../kotlin`、`.../swift`、`.../ruby`、`.../php`、`.../bash`
- API：`parser := sitter.NewParser()`、`parser.SetLanguage(lang.GetLanguage())`、
  `tree, err := parser.ParseCtx(ctx, nil, src)`、`node.Type()`、`node.NamedChild(i)`、
  `node.ChildByFieldName("name")`、`node.StartPoint()/EndPoint()`、`node.Content(src)`。

### 被排除的方案

- `tree-sitter/go-tree-sitter` 官方绑定：JavaScript、C、C++、Ruby、Bash、Swift 没有 `bindings/go`，无法覆盖 14 种。
- `go-sitter-forest` + `go-tree-sitter-bare`：覆盖完整，但需要维护两套第三方绑定，且用户已明确指定 smacker。
- 只用 `go/ast` 扩展现有索引：无法覆盖非 Go 语言。

### 包结构

新增 `internal/indexer/treesitter`：

```
internal/indexer/treesitter/
├── language.go     // 扩展名 → 语言与符号规则
├── index.go        // 缓存、扫描、增量刷新
├── symbols.go      // AST 遍历与符号提取
├── search.go       // search_symbols / 结构感知查询
└── *_test.go
```

`internal/tools` 增加 `search_symbols` 工具，并让 `SearchCode` 接受可选索引。
`internal/agent/assembly.go` 的 `BuildProjectContext` 改用多语言符号概览。
`ChatService` 持有一个按 workspace root 的 `*treesitter.Index`，切换项目时重建。

### 语言与扩展名

| 语言 | 扩展名 | 子包 |
|---|---|---|
| Go | `.go` | `golang` |
| TypeScript | `.ts` | `typescript/typescript` |
| TSX | `.tsx` | `typescript/tsx` |
| JavaScript | `.js` `.jsx` `.mjs` `.cjs` | `javascript` |
| Python | `.py` | `python` |
| Rust | `.rs` | `rust` |
| Java | `.java` | `java` |
| C | `.c` `.h` | `c` |
| C++ | `.cc` `.cpp` `.cxx` `.hpp` `.hh` `.hxx` | `cpp` |
| C# | `.cs` | `csharp` |
| Kotlin | `.kt` `.kts` | `kotlin` |
| Swift | `.swift` | `swift` |
| Ruby | `.rb` `.rake` | `ruby` |
| PHP | `.php` | `php` |
| Bash | `.sh` `.bash` `.zsh` `.ksh` | `bash` |

`.h` 归 C；如需 C++ 头文件，用 `.hpp/.hh/.hxx`。该选择写入文档，避免按内容猜语言。

### 符号规则

每种语言一个 `languageSpec`：

```go
type languageSpec struct {
    Language  func() *sitter.Language
    Exts      []string
    // node type -> 符号类型（func/method/class/interface/struct/type/enum/trait/protocol/module）
    Kinds     map[string]SymbolKind
    NameField string // 一般为 "name"；C 的函数需要沿 declarator 向下找 identifier
}
```

初始映射（实现时用 fixture 校准）：

- Go：`function_declaration`→func、`method_declaration`→method、`type_declaration` 下的 `struct_type`/`interface_type`/其他→struct/interface/type。
- TypeScript/TSX/JavaScript：`function_declaration`、`method_definition`、`class_declaration`、`interface_declaration`、`type_alias_declaration`、`enum_declaration`；箭头函数可选（`lexical_declaration` 中的函数值）。
- Python：`function_definition`→func、`class_definition`→class。
- Rust：`function_item`、`impl_item`（方法：impl 内的 function_item）、`struct_item`、`trait_item`、`enum_item`、`type_item`。
- Java：`method_declaration`、`class_declaration`、`interface_declaration`、`enum_declaration`、`record_declaration`。
- C：`function_definition`、`struct_specifier`、`enum_specifier`、`union_specifier`、`type_definition`。
- C++：`function_definition`、`class_specifier`、`struct_specifier`、`namespace_definition`、`enum_specifier`。
- C#：`method_declaration`、`class_declaration`、`interface_declaration`、`struct_declaration`、`enum_declaration`、`record_declaration`。
- Kotlin：`function_declaration`、`class_declaration`、`object_declaration`；interface 通过 `class_declaration` 的 `interface` 关键字判定（fixture 校准）。
- Swift：`function_declaration`、`class_declaration`、`struct_declaration`、`protocol_declaration`、`enum_declaration`、`extension_declaration`。
- Ruby：`method`、`singleton_method`、`class`、`module`。
- PHP：`function_definition`、`method_declaration`、`class_declaration`、`interface_declaration`、`trait_declaration`。
- Bash：`function_definition`。

名称提取：优先 `ChildByFieldName("name")`；C/C++ 的函数名在 declarator 链中，递归寻找最后一个 `identifier`/`field_identifier`。
方法接收者：Go 用 `receiver` 字段；Rust 用 `impl_item` 的 type；其他语言可留空。

### 索引与缓存

```go
type Index struct {
    root    string
    mu      sync.Mutex
    files   map[string]fileEntry // 相对路径
    symbols []Symbol
    byName  map[string][]int
}

type fileEntry struct {
    mtime   int64
    size    int64
    symbols []Symbol
}
```

- `EnsureFresh()`：`WalkDir`，跳过 `ignoreDirs`（复用 indexer 的忽略规则）、二进制文件、单文件 >1MB。
- 对每个受支持扩展名：
  - mtime+size 未变化 → 复用旧符号；
  - 变化/新增 → 解析并提取；
  - 已删除 → 移除。
- 解析并发：worker 数 `min(8, NumCPU)`；每个 worker 独立 `sitter.Parser`（smacker Parser 非线程安全）。
- 上限：最多 5000 个文件、50000 个符号；超出时停止新增并记录一次警告。
- `SearchSymbols(pattern, kind, language, max)`：在内存符号表上过滤，不重新解析。
- `SymbolAt(file, line)`：按行范围查所属符号（取跨度最小的）。
- 缓存生命周期：`ChatService` 按 workspace root 持有一个 Index；切换 workspace 重建；进程内不落盘（避免索引文件版本/权限问题）。

### 工具

1. 新增 `search_symbols`：
   - 参数：`pattern`（默认大小写不敏感子串）、`kind`（可选）、`language`（可选）、`max`（默认 100，上限 500）。
   - 输出：`file:line kind name`（方法附接收者）。
2. `search_code` 增强：
   - `SearchHit` 增加 `Symbol`、`StartLine`、`EndLine`。
   - 输出：`file:line: text  [kind name Lx-Ly]`。
   - 索引不可用（无 workspace/CGO 失败）时降级为现有行为。
3. `BuildProjectContext`：
   - 用索引生成“语言统计 + 前 80 个符号”；保留 go.mod、入口、README、项目文件线索。
   - 索引失败时不阻塞提示词构建，回退到现有 Go-only 概览。

### 错误处理

- 单个文件解析失败：跳过并计入 `skipped`，不影响其他文件。
- tree-sitter 返回 nil tree / 根节点错误：跳过。
- 文件读取失败/权限错误：跳过。
- 索引整体失败：记录警告，工具降级为纯文本检索。
- 不因索引失败阻塞对话或任务。

### 测试

- 每种语言一个最小 fixture（内联字符串或 `testdata/`），断言符号名称、kind、起始行。
- 缓存测试：首次索引 → 修改文件 mtime/content → 只重新解析该文件。
- `search_code` 结构附加测试：命中行落在函数内时返回符号与起止行。
- `search_symbols` 过滤测试：pattern/kind/language/max。
- 大文件/忽略目录/解析失败跳过测试。
- 回归：现有 `internal/indexer` 的 Go 测试保持通过。

## 已决定（用户确认）

- 使用 `github.com/smacker/go-tree-sitter`，允许 `go mod tidy`，不担心磁盘占用。
- 覆盖 14 种语言。
- 三项能力全要：符号索引 + `search_symbols`、结构感知 `search_code`、项目摘要多语言化。
- 按需扫描 + mtime 缓存。

## 留给建造者决定

- 各语言符号 node kind 的最终映射与名称提取细节（以 fixture 测试为准）。
- worker 数、文件/符号上限的具体值（默认 8 / 5000 / 50000）。
- 是否需要为箭头函数、匿名类等补充“低置信度符号”（默认不做，避免噪声）。
- 索引是否需要在设置页暴露“重建/统计”入口（默认不做）。
