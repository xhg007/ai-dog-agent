// Package tools 实现 Agent 工具集。
// 本文件为核心函数（供测试与工具封装复用）；所有文件操作经 workspace.Manager 校验，
// 保证操作被限制在 workspace 内。
package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"ai-dog-agent/internal/workspace"
)

// 默认忽略目录（不进入遍历/检索）。
var ignoreDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "vendor": true,
	"__pycache__": true, ".idea": true, ".vscode": true, "bin": true,
}

const (
	maxReadSize  = 1 << 20 // 1MB：单次读取内容上限
	maxReadLines = 5000    // 单次读取行数上限
)

// FileEntry 文件条目。
type FileEntry struct {
	Path  string `json:"path"`  // 相对 workspace 的路径
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// SearchHit 检索命中。
type SearchHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// ReadFile 读取 workspace 内文本文件，返回带行号内容。
// offset 从 1 开始的起始行（0 表示从头）；limit 行数（0 表示默认 2000）。
func ReadFile(ws *workspace.Manager, path string, offset, limit int) (string, error) {
	abs, err := ws.Resolve(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	if len(data) > maxReadSize {
		return "", fmt.Errorf("文件过大(%d 字节)，请用 offset/limit 分页读取", len(data))
	}
	if isBinary(data) {
		return "", fmt.Errorf("疑似二进制文件，拒绝读取: %s", path)
	}
	if limit <= 0 {
		limit = 2000
	}
	if limit > maxReadLines {
		limit = maxReadLines
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	start := offset
	if start <= 0 {
		start = 1
	}
	var sb strings.Builder
	for i := start; i <= len(lines) && i < start+limit; i++ {
		fmt.Fprintf(&sb, "%d: %s\n", i, lines[i-1])
	}
	return sb.String(), nil
}

// isBinary 通过 NUL 字节判断是否二进制。
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
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
			e := FileEntry{Path: d.Name(), IsDir: d.IsDir()}
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
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				hits = append(hits, SearchHit{File: rel, Line: i + 1, Text: truncate(line, 300)})
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
	return s[:n] + "…"
}
