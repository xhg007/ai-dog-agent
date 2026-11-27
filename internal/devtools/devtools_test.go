// 离线 golden 与 HTTP 行为测试：只读本地 testdata，不访问外网。
package devtools

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func TestGoldenParseDocument(t *testing.T) {
	tests := []struct{ name, fixture, golden string }{
		{"net/http", "doc_net_http.html", "doc_net_http.golden"},
		{"database/sql", "doc_database_sql.html", "doc_database_sql.golden"},
		{"regexp", "doc_regexp.html", "doc_regexp.golden"},
		{"io/fs", "doc_io_fs.html", "doc_io_fs.golden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, got := parseDocument(loadHTMLFixture(t, tt.fixture))
			if !matched {
				t.Fatal("parseDocument 未命中 div.Documentation")
			}
			assertGolden(t, tt.golden, got)
		})
	}
}

func TestGoldenParseSearchResult(t *testing.T) {
	matched, got := parseSearchResult(loadHTMLFixture(t, "search_mcp_go.html"))
	if !matched {
		t.Fatal("parseSearchResult 未命中 div.SearchResults")
	}
	assertGolden(t, "search_mcp_go.golden", got)
}

func TestGoldenParseReadme(t *testing.T) {
	matched, got := parseReadme(loadHTMLFixture(t, "readme_testify.html"))
	if !matched {
		t.Fatal("parseReadme 未命中 div.Overview-readmeContent")
	}
	assertGolden(t, "readme_testify.golden", got)
}

func TestGoldenParseRustDoc(t *testing.T) {
	tests := []struct{ name, fixture, golden string }{
		{"serde", "rustdoc_doc_serde.html", "rustdoc_doc_serde.golden"},
		{"tokio", "rustdoc_doc_tokio.html", "rustdoc_doc_tokio.golden"},
		{"regex", "rustdoc_doc_regex.html", "rustdoc_doc_regex.golden"},
		{"serdejson_map", "rustdoc_doc_serdejson_map.html", "rustdoc_doc_serdejson_map.golden"},
		{"httparse_request", "rustdoc_doc_httparse_request.html", "rustdoc_doc_httparse_request.golden"},
		{"serdejson_value", "rustdoc_doc_serdejson_value.html", "rustdoc_doc_serdejson_value.golden"},
		{"serdejson_tostring", "rustdoc_doc_serdejson_tostring.html", "rustdoc_doc_serdejson_tostring.golden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, got := parseDocsRsDocument(loadHTMLFixture(t, tt.fixture))
			if !matched {
				t.Fatal("parseDocsRsDocument 未命中 section#main-content")
			}
			assertGolden(t, tt.golden, got)
		})
	}
}

func TestGoldenParseRustDocSearch(t *testing.T) {
	matched, got := parseDocsRsSearchResult(loadHTMLFixture(t, "rustdoc_search_serde.html"))
	if !matched {
		t.Fatal("parseDocsRsSearchResult 未命中 div.recent-releases-container")
	}
	assertGolden(t, "rustdoc_search_serde.golden", got)
}

func TestGoldenParsePyDocPage(t *testing.T) {
	tests := []struct{ name, fixture, golden string }{
		{"abc", "pydoc_doc_abc.html", "pydoc_doc_abc.golden"},
		{"json", "pydoc_doc_json.html", "pydoc_doc_json.golden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, got := parsePyDocPage(loadHTMLFixture(t, tt.fixture))
			if !matched {
				t.Fatal("parsePyDocPage 未命中 section#module-*")
			}
			assertGolden(t, tt.golden, got)
		})
	}
}

func TestGoldenParsePyModIndex(t *testing.T) {
	matched, entries := parsePyModIndex(loadHTMLFixture(t, "pydoc_modindex.html"))
	if !matched {
		t.Fatal("parsePyModIndex 未命中 table.modindextable")
	}
	if len(entries) == 0 {
		t.Fatal("模块索引不应为空")
	}
	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "* %s\n\tURL: %s\n", e.Name, e.URL)
		if e.Description != "" {
			fmt.Fprintf(&sb, "\tDescription: %s\n", e.Description)
		}
	}
	assertGolden(t, "pydoc_modindex.golden", sb.String())
}

func TestValidateDocSlug(t *testing.T) {
	valid := []string{"golang.org/x/net/html", "serde", "serde-json/de", "os.path", "a_b~c"}
	for _, s := range valid {
		if err := validateDocSlug("input", s); err != nil {
			t.Errorf("应接受 %q: %v", s, err)
		}
	}
	invalid := []string{"", "../etc/passwd", "https://evil.example/x", "/absolute", "a b", "a?b=c", "x/../../y"}
	for _, s := range invalid {
		if err := validateDocSlug("input", s); err == nil {
			t.Errorf("应拒绝 %q", s)
		}
	}
}

func TestPageLines(t *testing.T) {
	doc := "l1\nl2\nl3\nl4"
	got, total, hasMore := pageLines(doc, 1, 2)
	if got != "l2\nl3" || total != 4 || !hasMore {
		t.Fatalf("分页结果不符: got=%q total=%d hasMore=%v", got, total, hasMore)
	}
	if got, _, hasMore := pageLines(doc, 4, 2); got != "" || hasMore {
		t.Fatalf("越界分页应返回空且无更多: got=%q hasMore=%v", got, hasMore)
	}
}

func TestHTTPGetStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := httpGet(srv.URL); err == nil {
		t.Fatal("非 200 响应应返回错误")
	}
}

func TestReadGoDocPagedWithFakeHTTP(t *testing.T) {
	docCache = newTTLCache[string](30 * time.Minute)
	withHTTPFixture(t, "doc_net_http.html")

	content, total, hasMore, err := ReadGoDocPaged("example.com/fake", 0, 5)
	if err != nil {
		t.Fatalf("ReadGoDocPaged 失败: %v", err)
	}
	if total <= 5 || !hasMore {
		t.Fatalf("fixture 文档应超过 5 行: total=%d hasMore=%v", total, hasMore)
	}
	golden := readGolden(t, "doc_net_http.golden")
	want := strings.Join(strings.Split(golden, "\n")[:5], "\n")
	if content != want {
		t.Fatalf("首分页内容与 golden 不一致\nwant: %q\ngot:  %q", want, content)
	}
}

func TestSearchWithinGoDocWithFakeHTTP(t *testing.T) {
	docCache = newTTLCache[string](30 * time.Minute)
	withHTTPFixture(t, "doc_net_http.html")

	res, err := SearchWithinGoDoc("example.com/fake2", "func", 3)
	if err != nil {
		t.Fatalf("SearchWithinGoDoc 失败: %v", err)
	}
	if len(res.Matches) == 0 || len(res.Matches) > 3 {
		t.Fatalf("命中数量异常: %d", len(res.Matches))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// withHTTPFixture 用本地 fixture 替换 HTTP 客户端，测试结束恢复。
func withHTTPFixture(t *testing.T, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取 fixture %s 失败: %v", name, err)
	}
	old := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { httpClient = old })
}

func loadHTMLFixture(t *testing.T, name string) *html.Node {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取 fixture %s 失败: %v", name, err)
	}
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("解析 fixture %s 失败: %v", name, err)
	}
	return doc
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取 golden %s 失败: %v", name, err)
	}
	// 仓库在 Windows 上可能以 CRLF 检出，解析器统一输出 LF。
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	if want := readGolden(t, name); want != got {
		t.Fatalf("输出与 golden %s 不一致\nwant:\n%s\n\ngot:\n%s", name, want, got)
	}
}
