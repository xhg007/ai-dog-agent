// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/app/localsearch.go + internal/infra/filesystem.go — 按扩展名检索文件内容。
package devtools

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const headerSize = 16

// SearchLocalFiles 在 root 下按扩展名（如 ".go"，空表示全部）检索字面子串，
// 每个文件最多返回 maxMatches 条；返回按文件聚合的结果。
func SearchLocalFiles(root, extension, query string, maxMatches int) ([]SearchResult, error) {
	if maxMatches <= 0 {
		maxMatches = 10
	}
	var results []SearchResult
	err := walkFiles(root, extension, func(filePath string) error {
		matches, truncated, err := searchInFile(filePath, query, maxMatches)
		if err != nil {
			return err
		}
		if len(matches) > 0 {
			results = append(results, SearchResult{Filename: filePath, Matches: matches, Truncated: truncated})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func searchInFile(filename, query string, maxMatches int) ([]SearchMatch, bool, error) {
	fp, err := os.Open(filename)
	if err != nil {
		return nil, false, err
	}
	defer fp.Close()

	reader := bufio.NewReader(fp)
	if !isTextFile(reader) {
		return nil, false, nil
	}
	return SearchInContent(reader, query, maxMatches)
}

func isTextFile(reader *bufio.Reader) bool {
	buf, err := reader.Peek(headerSize)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return utf8.ValidString(string(buf))
}

// walkFiles 递归遍历 root，跳过点文件/点目录与 .git，按扩展名过滤后回调。
func walkFiles(root, extension string, fn func(string) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("读取目录失败: %w", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		p := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			if err := walkFiles(p, extension, fn); err != nil {
				return err
			}
			continue
		}
		if extension != "" && filepath.Ext(entry.Name()) != extension {
			continue
		}
		if err := fn(p); err != nil {
			return err
		}
	}
	return nil
}
