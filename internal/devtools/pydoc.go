// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/app/pydoc.go — docs.python.org 模块索引搜索、分页读取与关键词检索。
package devtools

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type pyModEntry struct {
	Name        string
	Description string
	URL         string
}

// pyModCache 缓存模块索引（解析后的结构，非字符串）。
var pyModCache = newTTLCache[[]pyModEntry](30 * time.Minute)

// SearchPyDoc 在 Python 标准库模块索引中按名称/描述搜索。
func SearchPyDoc(query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", errors.New("搜索关键词不能为空")
	}
	entries, err := loadPyModIndex()
	if err != nil {
		return "", err
	}

	queryLower := strings.ToLower(query)
	builder := strings.Builder{}
	count := 0
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Name), queryLower) ||
			strings.Contains(strings.ToLower(entry.Description), queryLower) {
			builder.WriteString(fmt.Sprintf("* %s\n", entry.Name))
			builder.WriteString(fmt.Sprintf("\tURL: %s\n", entry.URL))
			if entry.Description != "" {
				builder.WriteString(fmt.Sprintf("\tDescription: %s\n", entry.Description))
			}
			count++
		}
	}
	if count == 0 {
		return "", errors.New("未找到匹配的 Python 模块")
	}
	return fmt.Sprintf("Found %d matching Python modules:\n%s", count, builder.String()), nil
}

// ReadPyDocPaged 读取 Python 模块文档并分页。moduleName 形如 "json"、"os.path"。
func ReadPyDocPaged(moduleName string, offset, limit int) (string, int, bool, error) {
	document, err := fetchPyDoc(moduleName)
	if err != nil {
		return "", 0, false, err
	}
	content, total, hasMore := pageLines(document, offset, limit)
	return content, total, hasMore, nil
}

// PyDocSearchResult 模块内关键词检索结果。
type PyDocSearchResult struct {
	ModuleName string
	Matches    []SearchMatch
	Truncated  bool
}

// SearchWithinPyDoc 在指定模块文档内检索关键词。
func SearchWithinPyDoc(moduleName, keyword string, maxMatches int) (*PyDocSearchResult, error) {
	document, err := fetchPyDoc(moduleName)
	if err != nil {
		return nil, err
	}
	matches, truncated, err := SearchInContent(strings.NewReader(document), keyword, maxMatches)
	if err != nil {
		return nil, err
	}
	return &PyDocSearchResult{ModuleName: moduleName, Matches: matches, Truncated: truncated}, nil
}

func loadPyModIndex() ([]pyModEntry, error) {
	if cached, ok := pyModCache.get("pydoc:modindex"); ok {
		return cached, nil
	}
	body, err := httpGet("https://docs.python.org/3/py-modindex.html")
	if err != nil {
		return nil, err
	}
	defer body.Close()

	doc, err := html.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("解析 HTML 失败: %w", err)
	}
	matched, entries := parsePyModIndex(doc)
	if !matched {
		return nil, errors.New("未找到 Python 模块索引")
	}
	pyModCache.set("pydoc:modindex", entries)
	return entries, nil
}

func fetchPyDoc(moduleName string) (string, error) {
	if err := validateDocSlug("module_name", moduleName); err != nil {
		return "", err
	}
	cacheKey := "pydoc:" + moduleName
	if cached, ok := docCache.get(cacheKey); ok {
		return cached, nil
	}
	body, err := httpGet(buildPyDocURL(moduleName))
	if err != nil {
		return "", err
	}
	defer body.Close()

	doc, err := html.Parse(body)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}
	matched, parsed := parsePyDocPage(doc)
	if !matched || strings.TrimSpace(parsed) == "" {
		return "", ErrNotFound
	}
	docCache.set(cacheKey, parsed)
	return parsed, nil
}

// buildPyDocURL 由模块名构造文档地址：
//
//	"json"    → https://docs.python.org/3/library/json.html
//	"os.path" → https://docs.python.org/3/library/os.path.html
func buildPyDocURL(moduleName string) string {
	return fmt.Sprintf("https://docs.python.org/3/library/%s.html", moduleName)
}

// cleanPyDocHeading 去掉 Python 文档标题里的 ¶ 锚点。
func cleanPyDocHeading(text string) string {
	text = strings.ReplaceAll(text, "¶", "")
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return strings.TrimSpace(text)
}

// cleanPyDocSignature 清理 dt 签名文本（¶ 与 [source]）。
func cleanPyDocSignature(text string) string {
	text = strings.ReplaceAll(text, "¶", "")
	text = strings.ReplaceAll(text, "[source]", "")
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	return strings.TrimSpace(text)
}

func parsePyModIndex(doc *html.Node) (bool, []pyModEntry) {
	var entries []pyModEntry

	trMatcher := NewNodeMatcher(
		NewMatchFunc("tr"),
		func(n *html.Node) {
			entry := extractModuleFromRow(n)
			if entry != nil {
				entries = append(entries, *entry)
			}
		},
	)

	var matched bool
	rootMatcher := NewNodeMatcher(
		NewMatchFunc("table.modindextable"),
		func(n *html.Node) {
			matched = true
		},
		trMatcher,
	)

	Traverse(doc, []Matcher{rootMatcher})
	return matched, entries
}

// extractModuleFromRow 从模块索引表格行提取条目；非模块行返回 nil。
func extractModuleFromRow(tr *html.Node) *pyModEntry {
	tds := FindAll(tr, "td")
	if len(tds) < 3 {
		return nil
	}
	a := FindDirectChild(tds[1], "a[href*=#module-]")
	if a == nil {
		return nil
	}
	moduleName := strings.TrimSpace(InnerText(a, true))
	if moduleName == "" {
		return nil
	}
	moduleHref := GetHref(a)
	description := strings.TrimSpace(InnerText(tds[2], true))
	return &pyModEntry{
		Name:        moduleName,
		Description: description,
		URL:         fmt.Sprintf("https://docs.python.org/3/%s", moduleHref),
	}
}

func parsePyDocPage(doc *html.Node) (bool, string) {
	builder := strings.Builder{}

	h1Matcher := NewNodeMatcher(
		NewMatchFunc("h1"),
		func(n *html.Node) {
			text := cleanPyDocHeading(InnerText(n, true))
			builder.WriteString(fmt.Sprintf("# %s\n\n", text))
		},
	)

	headingMatcher := NewNodeMatcher(
		NewMatchFunc("h2,h3,h4"),
		func(n *html.Node) {
			text := cleanPyDocHeading(InnerText(n, true))
			h := strings.TrimPrefix(n.Data, "h")
			hn, _ := strconv.Atoi(h)
			builder.WriteString(fmt.Sprintf("\n%s %s\n", strings.Repeat("#", hn), text))
		},
	)

	pMatcher := NewNodeMatcher(
		NewMatchFunc("p"),
		func(n *html.Node) {
			text := strings.TrimSpace(InnerText(n, true))
			if text != "" {
				builder.WriteString(fmt.Sprintf("%s\n", text))
			}
		},
	)

	preMatcher := NewNodeMatcher(
		NewMatchFunc("pre"),
		func(n *html.Node) {
			code := strings.TrimRight(RawInnerText(n, true), "\n")
			if code != "" {
				builder.WriteString(fmt.Sprintf("```\n%s\n```\n", code))
			}
		},
	)

	dtMatcher := NewNodeMatcher(
		NewMatchFunc("dt.sig-object"),
		func(n *html.Node) {
			sig := cleanPyDocSignature(RawInnerText(n, true))
			builder.WriteString(fmt.Sprintf("\n%s\n", sig))
		},
	)

	innerDdMatcher := NewNodeMatcher(
		NewMatchFunc("dd"),
		nil,
		pMatcher,
		preMatcher,
	)
	innerDlMatcher := NewNodeMatcher(
		NewMatchFunc("dl.py"),
		nil,
		dtMatcher,
		innerDdMatcher,
	)
	ddMatcher := NewNodeMatcher(
		NewMatchFunc("dd"),
		nil,
		pMatcher,
		preMatcher,
		innerDlMatcher,
	)
	dlMatcher := NewNodeMatcher(
		NewMatchFunc("dl.py"),
		nil,
		dtMatcher,
		ddMatcher,
	)
	sectionMatcher := NewNodeMatcher(
		NewMatchFunc("section"),
		nil,
		headingMatcher,
	)

	var matched bool
	rootMatcher := NewNodeMatcher(
		NewMatchFunc("section#module-*"),
		func(n *html.Node) {
			matched = true
		},
		h1Matcher,
		headingMatcher,
		sectionMatcher,
		dlMatcher,
		pMatcher,
		preMatcher,
	)

	Traverse(doc, []Matcher{rootMatcher})
	return matched, builder.String()
}
