// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/app/rustdoc.go — docs.rs 文档搜索、分页读取与关键词检索。
package devtools

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// SearchRustDoc 在 docs.rs 搜索 crate。
func SearchRustDoc(query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", errors.New("搜索关键词不能为空")
	}
	u := fmt.Sprintf("https://docs.rs/releases/search?query=%s", url.QueryEscape(query))
	body, err := httpGet(u)
	if err != nil {
		return "", err
	}
	defer body.Close()

	doc, err := html.Parse(body)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}
	matched, document := parseDocsRsSearchResult(doc)
	if !matched {
		return "", errors.New("未找到匹配的 crate")
	}
	return document, nil
}

// ReadRustDocPaged 读取 crate 文档并分页。crateURL 形如 "serde"、"serde/de"、"tokio/runtime"。
func ReadRustDocPaged(crateURL string, offset, limit int) (string, int, bool, error) {
	document, err := fetchRustDoc(crateURL)
	if err != nil {
		return "", 0, false, err
	}
	content, total, hasMore := pageLines(document, offset, limit)
	return content, total, hasMore, nil
}

// RustDocSearchResult crate 内关键词检索结果。
type RustDocSearchResult struct {
	CrateURL  string
	Matches   []SearchMatch
	Truncated bool
}

// SearchWithinRustDoc 在指定 crate 文档内检索关键词。
func SearchWithinRustDoc(crateURL, keyword string, maxMatches int) (*RustDocSearchResult, error) {
	document, err := fetchRustDoc(crateURL)
	if err != nil {
		return nil, err
	}
	matches, truncated, err := SearchInContent(strings.NewReader(document), keyword, maxMatches)
	if err != nil {
		return nil, err
	}
	return &RustDocSearchResult{CrateURL: crateURL, Matches: matches, Truncated: truncated}, nil
}

func fetchRustDoc(crateURL string) (string, error) {
	if err := validateDocSlug("crate_url", crateURL); err != nil {
		return "", err
	}
	cacheKey := "rustdoc:" + crateURL
	if cached, ok := docCache.get(cacheKey); ok {
		return cached, nil
	}
	body, err := httpGet(buildDocsRsURL(crateURL))
	if err != nil {
		return "", err
	}
	defer body.Close()

	doc, err := html.Parse(body)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}
	matched, parsed := parseDocsRsDocument(doc)
	if !matched || strings.TrimSpace(parsed) == "" {
		return "", ErrNotFound
	}
	docCache.set(cacheKey, parsed)
	return parsed, nil
}

// buildDocsRsURL 由 crateURL 构造 docs.rs 地址：
//
//	"serde"      → https://docs.rs/serde/latest/serde/
//	"serde/de"   → https://docs.rs/serde/latest/serde/de/
//	"serde-json" → https://docs.rs/serde-json/latest/serde_json/
func buildDocsRsURL(crateURL string) string {
	parts := strings.SplitN(crateURL, "/", 2)
	crateName := parts[0]
	// Rust crate 名用连字符，模块路径用下划线。
	crateModule := strings.ReplaceAll(crateName, "-", "_")
	if len(parts) == 1 {
		return fmt.Sprintf("https://docs.rs/%s/latest/%s/", crateName, crateModule)
	}
	return fmt.Sprintf("https://docs.rs/%s/latest/%s/%s", crateName, crateModule, parts[1])
}

// cleanRustDocHeading 去掉 § 锚点与 "Copy item path" 按钮文本。
func cleanRustDocHeading(text string) string {
	text = strings.ReplaceAll(text, "§", "")
	text = strings.ReplaceAll(text, "Copy item path", "")
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return strings.TrimSpace(text)
}

// cleanRustDocSignature 规整 impl/method 签名：去掉 ⓘ，把换行/制表符压成单行。
func cleanRustDocSignature(text string) string {
	text = strings.ReplaceAll(text, "ⓘ", "")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\t", " ")
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return strings.TrimSpace(text)
}

// writeRustDocBlock 渲染 div.docblock 的段落、代码块、列表与标题，
// 每行加 indent 以便嵌套在所属条目下。
func writeRustDocBlock(builder *strings.Builder, docblock *html.Node, indent string) {
	headerMatcher := NewNodeMatcher(
		NewMatchFunc("h1,h2,h3,h4,h5,h6"),
		func(n *html.Node) {
			text := cleanRustDocHeading(InnerText(n, true))
			if text != "" {
				builder.WriteString(fmt.Sprintf("%s**%s**\n", indent, text))
			}
		},
	)
	pMatcher := NewNodeMatcher(
		NewMatchFunc("p"),
		func(n *html.Node) {
			text := strings.TrimSpace(InnerText(n, true))
			if text != "" {
				builder.WriteString(fmt.Sprintf("%s%s\n", indent, text))
			}
		},
	)
	preMatcher := NewNodeMatcher(
		NewMatchFunc("pre"),
		func(n *html.Node) {
			code := strings.TrimRight(RawInnerText(n, true), "\n")
			builder.WriteString(indent)
			builder.WriteString("```\n")
			for _, line := range strings.Split(code, "\n") {
				builder.WriteString(indent)
				builder.WriteString(line)
				builder.WriteString("\n")
			}
			builder.WriteString(indent)
			builder.WriteString("```\n")
		},
	)
	listMatcher := NewNodeMatcher(
		NewMatchFunc("ul,ol"),
		nil,
		NewNodeMatcher(
			NewMatchFunc("li"),
			func(n *html.Node) {
				text := strings.TrimSpace(InnerText(n, true))
				if text != "" {
					builder.WriteString(fmt.Sprintf("%s- %s\n", indent, text))
				}
			},
		),
	)

	Traverse(docblock, []Matcher{headerMatcher, pMatcher, preMatcher, listMatcher})
}

func parseDocsRsSearchResult(doc *html.Node) (bool, string) {
	builder := strings.Builder{}

	var currentName string
	var currentHref string

	nameMatcher := NewNodeMatcher(
		NewMatchFunc("div.name"),
		func(n *html.Node) {
			currentName = strings.TrimSpace(InnerText(n, true))
		},
	)
	descMatcher := NewNodeMatcher(
		NewMatchFunc("div.description"),
		func(n *html.Node) {
			desc := strings.TrimSpace(InnerText(n, true))
			builder.WriteString(fmt.Sprintf("* %s\n", currentName))
			if currentHref != "" {
				builder.WriteString(fmt.Sprintf("\tURL: https://docs.rs%s\n", currentHref))
			}
			if desc != "" {
				builder.WriteString(fmt.Sprintf("\tDescription: %s\n", desc))
			}
		},
	)

	releaseMatcher := NewNodeMatcher(
		NewMatchFunc("a.release"),
		func(n *html.Node) {
			currentHref = GetHref(n)
		},
		NewNodeMatcher(
			NewMatchFunc("div"),
			nil,
			nameMatcher,
			descMatcher,
		),
	)

	listMatcher := NewNodeMatcher(
		NewMatchFunc("ul"),
		nil,
		NewNodeMatcher(
			NewMatchFunc("li"),
			nil,
			releaseMatcher,
		),
	)

	var matched bool
	rootMatcher := NewNodeMatcher(
		NewMatchFunc("div.recent-releases-container"),
		func(n *html.Node) {
			matched = true
		},
		listMatcher,
	)

	Traverse(doc, []Matcher{rootMatcher})
	return matched, builder.String()
}

func parseDocsRsDocument(doc *html.Node) (bool, string) {
	builder := strings.Builder{}

	var inFields, fieldOpen bool

	titleMatcher := NewNodeMatcher(
		NewMatchFunc("div.main-heading"),
		nil,
		NewNodeMatcher(
			NewMatchFunc("h1"),
			func(n *html.Node) {
				text := cleanRustDocHeading(InnerText(n, true))
				builder.WriteString(fmt.Sprintf("# %s\n", text))
			},
		),
	)

	itemDeclMatcher := NewNodeMatcher(
		NewMatchFunc("pre.item-decl"),
		func(n *html.Node) {
			decl := strings.TrimSpace(RawInnerText(n, true))
			if decl != "" {
				builder.WriteString(fmt.Sprintf("```rust\n%s\n```\n", decl))
			}
		},
	)

	docblockHeaderMatcher := NewNodeMatcher(
		NewMatchFunc("h1,h2,h3,h4,h5,h6"),
		func(n *html.Node) {
			text := cleanRustDocHeading(InnerText(n, true))
			h := strings.TrimPrefix(n.Data, "h")
			hn, _ := strconv.Atoi(h)
			builder.WriteString(fmt.Sprintf("\n%s %s\n", strings.Repeat("#", hn), text))
		},
	)
	docblockPMatcher := NewNodeMatcher(
		NewMatchFunc("p"),
		func(n *html.Node) {
			text := strings.TrimSpace(InnerText(n, true))
			if text != "" {
				builder.WriteString(fmt.Sprintf("%s\n", text))
			}
		},
	)
	docblockPreMatcher := NewNodeMatcher(
		NewMatchFunc("pre"),
		func(n *html.Node) {
			builder.WriteString(fmt.Sprintf("```\n%s\n```\n", RawInnerText(n, true)))
		},
	)
	docblockListMatcher := NewNodeMatcher(
		NewMatchFunc("ul,ol"),
		nil,
		NewNodeMatcher(
			NewMatchFunc("li"),
			func(n *html.Node) {
				text := strings.TrimSpace(InnerText(n, true))
				if text != "" {
					builder.WriteString(fmt.Sprintf("- %s\n", text))
				}
			},
		),
	)

	overviewMatcher := NewNodeMatcher(
		NewMatchFunc("details.top-doc"),
		nil,
		NewNodeMatcher(
			NewMatchFunc("div.docblock"),
			nil,
			docblockHeaderMatcher,
			docblockPMatcher,
			docblockPreMatcher,
			docblockListMatcher,
		),
	)

	sectionHeaderMatcher := NewNodeMatcher(
		NewMatchFunc("h2.section-header"),
		func(n *html.Node) {
			text := cleanRustDocHeading(InnerText(n, true))
			inFields = strings.HasPrefix(text, "Fields")
			switch text {
			case "Auto Trait Implementations", "Blanket Implementations":
				return
			}
			builder.WriteString(fmt.Sprintf("\n## %s\n", text))
		},
	)

	var currentItem string
	dtMatcher := NewNodeMatcher(
		NewMatchFunc("dt"),
		func(n *html.Node) {
			currentItem = strings.TrimSpace(InnerText(n, true))
		},
	)
	ddMatcher := NewNodeMatcher(
		NewMatchFunc("dd"),
		func(n *html.Node) {
			desc := strings.TrimSpace(InnerText(n, true))
			if currentItem != "" {
				builder.WriteString(fmt.Sprintf("- %s: %s\n", currentItem, desc))
				currentItem = ""
			}
		},
	)
	itemTableMatcher := NewNodeMatcher(
		NewMatchFunc("dl.item-table"),
		nil,
		dtMatcher,
		ddMatcher,
	)

	implHeaderMatcher := NewNodeMatcher(
		NewMatchFunc("h3.code-header"),
		func(n *html.Node) {
			sig := cleanRustDocSignature(RawInnerText(n, true))
			if sig != "" {
				builder.WriteString(fmt.Sprintf("\n### %s\n", sig))
			}
		},
	)
	methodSigMatcher := NewNodeMatcher(
		NewMatchFunc("h4.code-header"),
		func(n *html.Node) {
			sig := cleanRustDocSignature(RawInnerText(n, true))
			if sig != "" {
				builder.WriteString(fmt.Sprintf("- `%s`\n", sig))
			}
		},
	)
	methodDocMatcher := NewNodeMatcher(
		NewMatchFunc("div.docblock"),
		func(n *html.Node) {
			writeRustDocBlock(&builder, n, "  ")
		},
	)
	implsMatcher := NewNodeMatcher(
		NewMatchFunc("div#implementations-list,div#trait-implementations-list"),
		nil,
		implHeaderMatcher,
		methodSigMatcher,
		methodDocMatcher,
	)

	variantSigMatcher := NewNodeMatcher(
		NewMatchFunc("h3.code-header"),
		func(n *html.Node) {
			sig := cleanRustDocSignature(RawInnerText(n, true))
			if sig != "" {
				builder.WriteString(fmt.Sprintf("- `%s`\n", sig))
			}
		},
	)
	variantsMatcher := NewNodeMatcher(
		NewMatchFunc("div.variants"),
		nil,
		variantSigMatcher,
		methodDocMatcher,
	)

	structFieldMatcher := NewNodeMatcher(
		NewMatchFunc("span.structfield"),
		func(n *html.Node) {
			code := FindOne(n, "code")
			if code == nil {
				return
			}
			sig := cleanRustDocSignature(RawInnerText(code, true))
			if sig != "" {
				builder.WriteString(fmt.Sprintf("- `%s`\n", sig))
				fieldOpen = true
			}
		},
	)
	fieldDocMatcher := NewNodeMatcher(
		NewMatchFunc("div.docblock"),
		func(n *html.Node) {
			if !inFields || !fieldOpen {
				return
			}
			fieldOpen = false
			writeRustDocBlock(&builder, n, "  ")
		},
	)

	var matched bool
	rootMatcher := NewNodeMatcher(
		NewMatchFunc("section#main-content"),
		func(n *html.Node) {
			matched = true
		},
		titleMatcher,
		itemDeclMatcher,
		overviewMatcher,
		sectionHeaderMatcher,
		itemTableMatcher,
		structFieldMatcher,
		fieldDocMatcher,
		variantsMatcher,
		implsMatcher,
	)

	Traverse(doc, []Matcher{rootMatcher})
	return matched, builder.String()
}
