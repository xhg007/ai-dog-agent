// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/app/tree.go + internal/infra/filesystem.go — 目录树输出。
package devtools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TreeDir 输出目录树（连接线风格），maxDepth<=0 时默认 4 层。
// 始终跳过 .git；ignoreDot 为 true 时跳过所有点文件/点目录。
func TreeDir(root string, ignoreDot bool, maxDepth int) (string, error) {
	if maxDepth <= 0 {
		maxDepth = 4
	}
	var sb strings.Builder
	if err := walkTree(&sb, root, "", ignoreDot, maxDepth, 0); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func walkTree(sb *strings.Builder, path, prefix string, ignoreDot bool, maxDepth, depth int) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("读取目录失败: %w", err)
	}
	filtered := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == ".git" {
			continue
		}
		if ignoreDot && strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		filtered = append(filtered, entry)
	}
	for i, entry := range filtered {
		last := i == len(filtered)-1
		connector := "|-- "
		if last {
			connector = "└-- "
		}
		fmt.Fprintf(sb, "%s%s%s\n", prefix, connector, entry.Name())
		if !entry.IsDir() || depth >= maxDepth {
			continue
		}
		nextPrefix := prefix + "|   "
		if last {
			nextPrefix = prefix + "    "
		}
		if err := walkTree(sb, filepath.Join(path, entry.Name()), nextPrefix, ignoreDot, maxDepth, depth+1); err != nil {
			return err
		}
	}
	return nil
}
