// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/contentsearch — line-oriented substring search with limits.
package devtools

import (
	"bufio"
	"io"
	"strings"
)

// SearchMatch 一行命中。
type SearchMatch struct {
	LineNo int
	Text   string
}

// SearchResult 单个文件的检索结果。
type SearchResult struct {
	Filename  string
	Matches   []SearchMatch
	Truncated bool
}

// SearchInContent 按行检索字面子串，最多返回 maxMatches 条。
func SearchInContent(reader io.Reader, query string, maxMatches int) ([]SearchMatch, bool, error) {
	lineNo := 1
	var matches []SearchMatch
	truncated := false

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), query) {
			if len(matches) >= maxMatches {
				truncated = true
				break
			}
			matches = append(matches, SearchMatch{LineNo: lineNo, Text: scanner.Text()})
		}
		lineNo++
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return matches, truncated, nil
}
