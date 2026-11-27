package treesitter

// 索引缓存：按 workspace root 维护文件 mtime/size 与符号表，按需增量刷新。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxFileSize  = 1 << 20 // 1MB，超出不解析
	maxFiles     = 5000
	maxSymbols   = 50000
	maxWorkers   = 8
	scanCooldown = time.Second
)

// skipDirs 与 internal/indexer 的忽略规则保持一致，并补充 AI/编辑器目录。
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "vendor": true,
	"__pycache__": true, ".idea": true, ".vscode": true, "bin": true,
	".zed": true, ".tokensave": true, ".reasonix": true, ".aidog": true,
}

type fileEntry struct {
	mtime   int64
	size    int64
	symbols []Symbol
}

// Stats 索引统计。
type Stats struct {
	Files      int            `json:"files"`
	Symbols    int            `json:"symbols"`
	ByLanguage map[string]int `json:"byLanguage"`
	Skipped    int            `json:"skipped"`
	Truncated  bool           `json:"truncated"`
}

// Index 一个 workspace 的符号索引。
type Index struct {
	root string

	mu        sync.RWMutex
	files     map[string]fileEntry
	symbols   []Symbol
	skipped   int
	truncated bool
	lastScan  time.Time
}

// New 创建索引；root 会被规范化为绝对路径。
func New(root string) *Index {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Index{root: root, files: make(map[string]fileEntry)}
}

// Root 返回索引根目录。
func (i *Index) Root() string { return i.root }

var (
	registryMu sync.Mutex
	registry   = map[string]*Index{}
)

// ForWorkspace 返回 root 对应的共享索引（按绝对路径）。
func ForWorkspace(root string) *Index {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if idx := registry[root]; idx != nil {
		return idx
	}
	idx := New(root)
	registry[root] = idx
	return idx
}

// Release 释放 root 的索引（workspace 切换时调用）。
func Release(root string) {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	registryMu.Lock()
	delete(registry, root)
	registryMu.Unlock()
}

// Invalidate 清空扫描冷却，强制下一次 EnsureFresh 重新遍历（测试与显式刷新用）。
func (i *Index) Invalidate() {
	i.mu.Lock()
	i.lastScan = time.Time{}
	i.mu.Unlock()
}

// EnsureFresh 按 mtime+size 增量刷新索引。
// 单文件读取/解析失败只计入 skipped，不返回错误；仅根目录遍历失败或 ctx 取消时返回错误。
func (i *Index) EnsureFresh(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.root == "" {
		return nil
	}
	if !i.lastScan.IsZero() && time.Since(i.lastScan) < scanCooldown {
		return nil
	}
	i.lastScan = time.Now()

	type candidate struct {
		rel, abs    string
		mtime, size int64
	}
	var jobs []candidate
	seen := make(map[string]bool)
	truncated := false
	skipped := 0

	err := filepath.WalkDir(i.root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != i.root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !supportedExt(p) {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			skipped++
			return nil
		}
		if info.Size() > maxFileSize {
			skipped++
			return nil
		}
		rel, relErr := filepath.Rel(i.root, p)
		if relErr != nil {
			skipped++
			return nil
		}
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			return nil
		}
		if len(seen) >= maxFiles {
			truncated = true
			return filepath.SkipAll
		}
		seen[rel] = true

		if old, ok := i.files[rel]; ok && old.mtime == info.ModTime().UnixNano() && old.size == info.Size() {
			return nil // 未变化：复用现有符号
		}
		jobs = append(jobs, candidate{rel: rel, abs: p, mtime: info.ModTime().UnixNano(), size: info.Size()})
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if len(jobs) > 0 {
		type result struct {
			rel     string
			mtime   int64
			size    int64
			symbols []Symbol
		}
		jobCh := make(chan candidate)
		resCh := make(chan result, len(jobs))
		workers := maxWorkers
		if n := runtime.NumCPU(); n < workers {
			workers = n
		}
		if workers > len(jobs) {
			workers = len(jobs)
		}
		var wg sync.WaitGroup
		wg.Add(workers)
		for w := 0; w < workers; w++ {
			go func() {
				defer wg.Done()
				for j := range jobCh {
					data, readErr := os.ReadFile(j.abs)
					var syms []Symbol
					if readErr == nil {
						syms = extractSymbols(ctx, specForFile(j.abs), j.rel, data)
					}
					resCh <- result{rel: j.rel, mtime: j.mtime, size: j.size, symbols: syms}
				}
			}()
		}
		go func() {
			defer close(resCh)
			for _, j := range jobs {
				jobCh <- j
			}
			close(jobCh)
			wg.Wait()
		}()
		for r := range resCh {
			i.files[r.rel] = fileEntry{mtime: r.mtime, size: r.size, symbols: r.symbols}
			if len(r.symbols) == 0 {
				skipped++
			}
		}
	}

	// 移除已删除的文件
	for rel := range i.files {
		if !seen[rel] {
			delete(i.files, rel)
		}
	}

	all := make([]Symbol, 0, len(i.symbols))
	for _, entry := range i.files {
		all = append(all, entry.symbols...)
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].File != all[b].File {
			return all[a].File < all[b].File
		}
		if all[a].Line != all[b].Line {
			return all[a].Line < all[b].Line
		}
		return all[a].Name < all[b].Name
	})
	if len(all) > maxSymbols {
		all = all[:maxSymbols]
		truncated = true
	}

	i.symbols = all
	i.skipped = skipped
	i.truncated = truncated
	return nil
}

// Symbols 返回符号副本（按文件、行号排序）。
func (i *Index) Symbols() []Symbol {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make([]Symbol, len(i.symbols))
	copy(out, i.symbols)
	return out
}

// SearchSymbols 按名称子串（大小写不敏感）、kind 与语言过滤符号。
func (i *Index) SearchSymbols(pattern string, kind SymbolKind, language string, max int) []Symbol {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if max <= 0 {
		max = 100
	}
	if max > 500 {
		max = 500
	}
	p := strings.ToLower(strings.TrimSpace(pattern))
	kind = SymbolKind(strings.TrimSpace(string(kind)))
	language = strings.ToLower(strings.TrimSpace(language))
	out := make([]Symbol, 0, max)
	for _, s := range i.symbols {
		if p != "" && !strings.Contains(strings.ToLower(s.Name), p) {
			continue
		}
		if kind != "" && s.Kind != kind {
			continue
		}
		if language != "" && strings.ToLower(s.Language) != language {
			continue
		}
		out = append(out, s)
		if len(out) >= max {
			break
		}
	}
	return out
}

// SymbolAt 返回包含指定行的最小符号（跨行跨度最小）。
func (i *Index) SymbolAt(file string, line int) *Symbol {
	i.mu.RLock()
	defer i.mu.RUnlock()
	file = filepath.ToSlash(file)
	var best *Symbol
	bestSpan := 0
	for idx := range i.symbols {
		s := &i.symbols[idx]
		if s.File != file || line < s.StartLine || line > s.EndLine {
			continue
		}
		span := s.EndLine - s.StartLine
		if best == nil || span < bestSpan {
			cp := *s
			best = &cp
			bestSpan = span
		}
	}
	return best
}

// Stats 返回索引统计。
func (i *Index) Stats() Stats {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := Stats{
		Files:      len(i.files),
		Symbols:    len(i.symbols),
		ByLanguage: make(map[string]int),
		Skipped:    i.skipped,
		Truncated:  i.truncated,
	}
	for _, s := range i.symbols {
		out.ByLanguage[s.Language]++
	}
	return out
}
