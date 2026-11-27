// Package tools 实现 Agent 工具集。
// 本文件为核心函数（供测试与工具封装复用）；所有文件操作经 workspace.Manager 校验，
// 保证操作被限制在 workspace 内。
package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"ai-dog-agent/internal/indexer/treesitter"
	"ai-dog-agent/internal/workspace"
)

// 默认忽略目录（不进入遍历/检索）。
var ignoreDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "vendor": true,
	"__pycache__": true, ".idea": true, ".vscode": true, "bin": true,
}

const (
	maxReadLines    = 5000    // 单次读取行数上限
	binarySniffSize = 8192    // 二进制判定取样字节数
	maxLineSize     = 1 << 20 // 单行上限（超长行报错，避免 Scanner 无限缓冲）
)

// FileEntry 文件条目。
type FileEntry struct {
	Path  string `json:"path"` // 相对 workspace 的路径
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// SearchHit 检索命中。
type SearchHit struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Text      string `json:"text"`
	Kind      string `json:"kind,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

// ReadFile 读取 workspace 内文本文件，返回带行号内容。
// offset 从 1 开始的起始行（0 表示从头）；limit 行数（0 表示默认 2000）。
// 按行流式读取：大文件也能用 offset/limit 分页，不会整文件载入内存（FR-2.1）。
func ReadFile(ws *workspace.Manager, path string, offset, limit int) (string, error) {
	abs, err := ws.Resolve(path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	defer f.Close()

	// 二进制检测只看文件头，避免把大文件整个读进内存
	head := make([]byte, binarySniffSize)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	if isBinary(head[:n]) {
		return "", fmt.Errorf("疑似二进制文件，拒绝读取: %s", path)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}

	if limit <= 0 {
		limit = 2000
	}
	if limit > maxReadLines {
		limit = maxReadLines
	}
	start := offset
	if start <= 0 {
		start = 1
	}

	var sb strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo < start {
			continue
		}
		if lineNo >= start+limit {
			break
		}
		fmt.Fprintf(&sb, "%d: %s\n", lineNo, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	return sb.String(), nil
}

// isBinary 通过 NUL 字节判断是否二进制。
func isBinary(data []byte) bool {
	n := len(data)
	if n > binarySniffSize {
		n = binarySniffSize
	}
	for _, b := range data[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}

// ListFiles 列出 workspace 内目录内容。
// dir 为相对 workspace 的目录；recursive 递归；maxEntries 结果上限。
func ListFiles(ws *workspace.Manager, dir string, recursive bool, maxEntries int) ([]FileEntry, error) {
	abs, err := ws.Resolve(dir)
	if err != nil {
		return nil, err
	}
	if maxEntries <= 0 {
		maxEntries = 500
	}
	var entries []FileEntry
	root := ws.Root()

	if recursive {
		err = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // 无权限等错误跳过
			}
			if p == root {
				return nil
			}
			if d.IsDir() {
				if ignoreDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			entries = append(entries, FileEntry{Path: rel, Size: info.Size()})
			if len(entries) >= maxEntries {
				return filepath.SkipAll
			}
			return nil
		})
	} else {
		dirEntries, err2 := os.ReadDir(abs)
		if err2 != nil {
			return nil, fmt.Errorf("读取目录失败: %w", err2)
		}
		for _, d := range dirEntries {
			if d.Name() == ".git" || ignoreDirs[d.Name()] && d.IsDir() {
				continue
			}
			// Path 始终是相对 workspace 根的路径（与递归分支一致），
			// 否则模型拿到的 "fs.go" 无法再喂给 read_file。
			rel, rerr := filepath.Rel(root, filepath.Join(abs, d.Name()))
			if rerr != nil {
				rel = d.Name()
			}
			e := FileEntry{Path: rel, IsDir: d.IsDir()}
			if !d.IsDir() {
				if info, err := d.Info(); err == nil {
					e.Size = info.Size()
				}
			}
			entries = append(entries, e)
			if len(entries) >= maxEntries {
				break
			}
		}
	}
	if err != nil && err != filepath.SkipAll {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// SearchCode 在 workspace 内检索代码。
// pattern 为字面串或正则（isRegex）；glob 过滤文件名（如 "*.go"，空为全部）。
func SearchCode(ws *workspace.Manager, pattern, glob string, isRegex bool, maxResults int) ([]SearchHit, error) {
	if maxResults <= 0 {
		maxResults = 100
	}
	if maxResults > 500 {
		maxResults = 500
	}
	root := ws.Root()
	if root == "" {
		return nil, workspace.ErrNoWorkspace
	}

	// 符号索引用于给命中行附加所属函数/类；索引不可用时降级为纯文本检索。
	idx := treesitter.ForWorkspace(root)
	if err := idx.EnsureFresh(context.Background()); err != nil {
		idx = nil
	}

	var re *regexp.Regexp
	if isRegex {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("非法正则: %w", err)
		}
	} else {
		re = regexp.MustCompile(regexp.QuoteMeta(pattern))
	}

	var globRe *regexp.Regexp
	if glob != "" {
		globRe = regexp.MustCompile(globToRegexp(glob))
	}

	var hits []SearchHit
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if globRe != nil && !globRe.MatchString(d.Name()) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || isBinary(data) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				hit := SearchHit{File: rel, Line: i + 1, Text: truncate(line, 300)}
				if idx != nil {
					if sym := idx.SymbolAt(rel, i+1); sym != nil {
						hit.Kind = string(sym.Kind)
						hit.Symbol = sym.Name
						hit.StartLine = sym.StartLine
						hit.EndLine = sym.EndLine
					}
				}
				hits = append(hits, hit)
				if len(hits) >= maxResults {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return nil, err
	}
	return hits, nil
}

func globToRegexp(glob string) string {
	var sb strings.Builder
	sb.WriteString("^")
	for _, c := range glob {
		switch c {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return sb.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return truncateUTF8(s, n) + "…"
}

// truncateUTF8 按 UTF-8 边界截断到 max 字节以内，避免把多字节字符切一半。
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
