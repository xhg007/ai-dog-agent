// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/app/godoc.go — pkg.go.dev 文档搜索、分页读取与关键词检索。
package devtools

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ErrNotFound 文档页面结构未命中（源站改版或包不存在）。
var ErrNotFound = errors.New("未找到文档内容（包不存在或页面结构已变化）")

// godocNodeFilter 在默认过滤基础上跳过 pkg.go.dev 的版本徽标。
func godocNodeFilter(n *html.Node) bool {
	if !DefaultNodeFilter(n) {
		return false
	}
	if n.Type == html.ElementNode && n.Data == "span" &&
		GetAttr(n, "class") == "Documentation-sinceVersion" {
		return false
	}
	return true
}

// SearchGoDoc 搜索 pkg.go.dev 包。
func SearchGoDoc(query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", errors.New("搜索关键词不能为空")
	}
	u := fmt.Sprintf("https://pkg.go.dev/search?q=%s", url.QueryEscape(query))
	body, err := httpGet(u)
	if err != nil {
		return "", err
	}
	defer body.Close()

	doc, err := html.Parse(body)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}
	matched, document := parseSearchResult(doc)
	if !matched {
		matched, readme := parseReadme(doc)
		if matched {
			return readme, nil
		}
		_, document = parseDocument(doc)
	}
	return document, nil
}

// ReadGoDocPaged 读取 Go 包文档并分页。packageURL 形如 "golang.org/x/net/html"。
// 返回：内容、总行数、是否还有更多、错误。
func ReadGoDocPaged(packageURL string, offset, limit int) (string, int, bool, error) {
	document, err := fetchGoDoc(packageURL)
	if err != nil {
		return "", 0, false, err
	}
	content, total, hasMore := pageLines(document, offset, limit)
	return content, total, hasMore, nil
}

// GoDocSearchResult 包内关键词检索结果。
type GoDocSearchResult struct {
	PackageURL string
	Matches    []SearchMatch
	Truncated  bool
}

// SearchWithinGoDoc 在指定包文档内检索关键词。
func SearchWithinGoDoc(packageURL, keyword string, maxMatches int) (*GoDocSearchResult, error) {
	document, err := fetchGoDoc(packageURL)
	if err != nil {
		return nil, err
	}
	matches, truncated, err := SearchInContent(strings.NewReader(document), keyword, maxMatches)
	if err != nil {
		return nil, err
	}
	return &GoDocSearchResult{PackageURL: packageURL, Matches: matches, Truncated: truncated}, nil
}

// fetchGoDoc 抓取并解析 pkg.go.dev 文档页，结果按 packageURL 缓存 30 分钟。
func fetchGoDoc(packageURL string) (string, error) {
	if err := validateDocSlug("package_url", packageURL); err != nil {
		return "", err
	}
	cacheKey := "godoc:" + packageURL
	if cached, ok := docCache.get(cacheKey); ok {
		return cached, nil
	}
	body, err := httpGet("https://pkg.go.dev/" + packageURL)
	if err != nil {
		return "", err
	}
	defer body.Close()

	doc, err := html.Parse(body)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}
	matched, parsed := parseDocument(doc)
	if !matched {
		matched, parsed = parseReadme(doc)
	}
	if !matched || strings.TrimSpace(parsed) == "" {
		return "", ErrNotFound
	}
	docCache.set(cacheKey, parsed)
	return parsed, nil
}

func parseSearchResult(doc *html.Node) (bool, string) {
	builder := strings.Builder{}

	headerMatcher := NewNodeMatcher(
		NewMatchFunc("h2"),
		func(n *html.Node) {
			builder.WriteString(fmt.Sprintf("* %s\n", InnerText(n, true)))
		},
		NewNodeMatcher(
			NewMatchFunc("span"),
			func(n *html.Node) {
				u := InnerText(n, false)
				u = strings.Trim(u, "()")
				builder.WriteString(fmt.Sprintf("\tURL: %s\n", u))
			},
		),
	)
	pMatcher := NewNodeMatcher(
		NewMatchFunc("p"),
		func(n *html.Node) {
			builder.WriteString(fmt.Sprintf("\tDescription: %s\n", InnerText(n, true)))
		},
	)
	infoMatcher := NewNodeMatcher(
		NewMatchFunc("div.SearchSnippet-infoLabel"),
		func(n *html.Node) {
			if v := findVersionInNode(n); v != "" {
				builder.WriteString(fmt.Sprintf("\tVersion: %s\n", v))
			}
		},
	)
	snippetMatcher := NewNodeMatcher(
		NewMatchFunc("div.SearchSnippet"),
		nil,
		headerMatcher,
		infoMatcher,
		pMatcher,
	)

	var matched bool
	rootMatcher := NewNodeMatcher(
		NewMatchFunc("div.SearchResults"),
		func(n *html.Node) {
			matched = true
		},
		NewNodeMatcher(
			NewMatchFunc("div.SearchResults-summary"),
			func(n *html.Node) {
				builder.WriteString(fmt.Sprintf("Summary: %s\n", InnerText(n, true)))
			},
		),
		NewNodeMatcher(
			NewMatchFunc("div"),
			nil,
			snippetMatcher,
		),
	)

	Traverse(doc, []Matcher{rootMatcher})
	return matched, builder.String()
}

// findVersionInNode 在节点下查找以 "v" 开头的 <strong>（pkg.go.dev 搜索结果里的版本号）。
func findVersionInNode(n *html.Node) string {
	for _, strong := range FindAll(n, "strong") {
		text := strings.TrimSpace(InnerText(strong, false))
		if strings.HasPrefix(text, "v") {
			return text
		}
	}
	return ""
}

func parseDocument(doc *html.Node) (bool, string) {
	builder := strings.Builder{}
	innerText := func(n *html.Node) string {
		return InnerTextWithFilter(n, true, godocNodeFilter)
	}

	headerMatcher := NewNodeMatcher(
		NewMatchFunc("h1,h2,h3,h4,h5,h6"),
		func(n *html.Node) {
			h := strings.TrimPrefix(n.Data, "h")
			hn, _ := strconv.Atoi(h)
			builder.WriteString(fmt.Sprintf("\n%s %s\n", strings.Repeat("#", hn), innerText(n)))
		},
	)
	listMatcher := NewNodeMatcher(
		NewMatchFunc("ul,ol"),
		nil,
		NewNodeMatcher(
			NewMatchFunc("li"),
			func(n *html.Node) {
				if HasChild(n, "a") {
					builder.WriteString(fmt.Sprintf("- %s\n", innerText(n)))
				}
			},
			NewNodeMatcher(
				NewMatchFunc("ul,ol"),
				nil,
				NewNodeMatcher(
					NewMatchFunc("li"),
					func(n *html.Node) {
						builder.WriteString(fmt.Sprintf("    - %s\n", innerText(n)))
					},
				),
			),
		),
	)
	pMatcher := NewNodeMatcher(
		NewMatchFunc("p"),
		func(n *html.Node) {
			builder.WriteString(fmt.Sprintf("%s\n", innerText(n)))
		},
	)
	preMatcher := NewNodeMatcher(
		NewMatchFunc("pre"),
		func(n *html.Node) {
			builder.WriteString(fmt.Sprintf("```\n%s\n```\n", RawInnerText(n, true)))
		},
	)
	sectionMatcher := NewNodeMatcher(
		NewMatchFunc("section"),
		func(n *html.Node) {
			// section 是多个 div 的容器，内容由子 matcher 输出。
		},
		headerMatcher,
		NewNodeMatcher(
			NewMatchFunc("div"),
			nil,
			listMatcher,
			headerMatcher,
			pMatcher,
			preMatcher,
		),
		pMatcher,
		preMatcher,
	)

	var matched bool
	rootMatcher := NewNodeMatcher(
		NewMatchFunc("div.Documentation"),
		func(n *html.Node) {
			matched = true
		},
		NewNodeMatcher(
			NewMatchFunc("div"),
			nil,
			headerMatcher,
			sectionMatcher,
			listMatcher,
		),
	)

	Traverse(doc, []Matcher{rootMatcher})
	return matched, builder.String()
}

func parseReadme(doc *html.Node) (bool, string) {
	builder := strings.Builder{}
	innerText := func(n *html.Node) string {
		return InnerTextWithFilter(n, true, godocNodeFilter)
	}

	headerMatcher := NewNodeMatcher(
		NewMatchFunc("h1,h2,h3,h4,h5,h6"),
		func(n *html.Node) {
			h := strings.TrimPrefix(n.Data, "h")
			hn, _ := strconv.Atoi(h)
			builder.WriteString(fmt.Sprintf("\n%s %s\n", strings.Repeat("#", hn), innerText(n)))
		},
	)
	listMatcher := NewNodeMatcher(
		NewMatchFunc("ul,ol"),
		nil,
		NewNodeMatcher(
			NewMatchFunc("li"),
			func(n *html.Node) {
				builder.WriteString(fmt.Sprintf("- %s\n", innerText(n)))
			},
		),
	)
	pMatcher := NewNodeMatcher(
		NewMatchFunc("p"),
		func(n *html.Node) {
			builder.WriteString(fmt.Sprintf("%s\n", innerText(n)))
		},
	)
	preMatcher := NewNodeMatcher(
		NewMatchFunc("pre"),
		func(n *html.Node) {
			builder.WriteString(fmt.Sprintf("```\n%s\n```\n", RawInnerText(n, true)))
		},
	)

	var matched bool
	rootMatcher := NewNodeMatcher(
		NewMatchFunc("div.Overview-readmeContent"),
		func(n *html.Node) {
			matched = true
		},
		headerMatcher,
		listMatcher,
		pMatcher,
		preMatcher,
	)

	Traverse(doc, []Matcher{rootMatcher})
	return matched, builder.String()
}
