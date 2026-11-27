// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/app/markdown.go — 用 goldmark 扫描 Markdown 标题与行号。
package devtools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// MarkdownHeading 一个 Markdown 标题。
type MarkdownHeading struct {
	Title string
	Level int
	Line  int
}

// MarkdownFile 单个 Markdown 文件的标题列表。
type MarkdownFile struct {
	FileName string
	Headings []MarkdownHeading
}

// ScanMarkdown 扫描单个 .md 文件或目录下的所有 .md 文件，提取标题与行号。
func ScanMarkdown(path string) ([]MarkdownFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("读取路径失败 %s: %w", path, err)
	}
	var results []MarkdownFile
	if info.IsDir() {
		err = walkFiles(path, ".md", func(filePath string) error {
			headings, err := extractMarkdownHeadings(filePath)
			if err != nil {
				return nil // 跳过无法解析的文件
			}
			if len(headings) > 0 {
				results = append(results, MarkdownFile{FileName: filePath, Headings: headings})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return results, nil
	}
	if !strings.HasSuffix(strings.ToLower(path), ".md") {
		return nil, fmt.Errorf("文件不是 Markdown（.md）: %s", path)
	}
	headings, err := extractMarkdownHeadings(path)
	if err != nil {
		return nil, fmt.Errorf("解析 Markdown 失败 %s: %w", path, err)
	}
	if len(headings) > 0 {
		results = append(results, MarkdownFile{FileName: path, Headings: headings})
	}
	return results, nil
}

func extractMarkdownHeadings(filePath string) ([]MarkdownHeading, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	md := goldmark.New()
	doc := md.Parser().Parse(text.NewReader(content))

	var headings []MarkdownHeading
	err = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		heading, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		title := extractHeadingText(heading, content)
		lineNum := 1
		if lines := heading.Lines(); lines.Len() > 0 {
			lineNum = calculateLineNumber(content, lines.At(0).Start)
		}
		headings = append(headings, MarkdownHeading{Title: title, Level: heading.Level, Line: lineNum})
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}
	return headings, nil
}

func extractHeadingText(heading *ast.Heading, source []byte) string {
	var buf bytes.Buffer
	for child := heading.FirstChild(); child != nil; child = child.NextSibling() {
		if textNode, ok := child.(*ast.Text); ok {
			buf.Write(textNode.Segment.Value(source))
		}
	}
	return strings.TrimSpace(buf.String())
}

// calculateLineNumber 把字节位置转换为 1 基行号。
func calculateLineNumber(content []byte, pos int) int {
	if pos > len(content) {
		pos = len(content)
	}
	lineNum := 1
	for i := 0; i < pos; i++ {
		if content[i] == '\n' {
			lineNum++
		}
	}
	return lineNum
}

// FormatMarkdownScanResult 输出人类可读的标题树。
func FormatMarkdownScanResult(files []MarkdownFile) string {
	if len(files) == 0 {
		return "未找到 Markdown 文件。"
	}
	var result strings.Builder
	fmt.Fprintf(&result, "共 %d 个 Markdown 文件:\n\n", len(files))
	for _, file := range files {
		absPath, err := filepath.Abs(file.FileName)
		if err != nil {
			absPath = file.FileName
		}
		result.WriteString(absPath)
		result.WriteString("\n")
		for _, heading := range file.Headings {
			indent := strings.Repeat("  ", heading.Level-1)
			fmt.Fprintf(&result, "%s%s %s (line %d)\n",
				indent, strings.Repeat("#", heading.Level), heading.Title, heading.Line)
		}
		result.WriteString("\n")
	}
	return result.String()
}
