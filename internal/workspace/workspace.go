// Package workspace 管理 Agent 的工作区（本地项目目录），
// 提供路径解析与逃逸防护：所有文件操作必须落在 workspace 内。
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	// ErrNoWorkspace 尚未设置工作区。
	ErrNoWorkspace = errors.New("尚未设置 workspace，请先选择项目目录")
	// ErrOutsideWorkspace 路径越出工作区（含 .. 逃逸与符号链接逃逸）。
	ErrOutsideWorkspace = errors.New("路径越出 workspace 范围，已拒绝")
)

// Manager 工作区管理器，线程安全。
type Manager struct {
	mu   sync.RWMutex
	root string // 已解析（EvalSymlinks）的根目录绝对路径
}

// New 创建空管理器。
func New() *Manager { return &Manager{} }

// SetRoot 设置工作区根目录，要求为已存在的目录；内部解析符号链接。
func (m *Manager) SetRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("workspace 目录不可访问: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace 必须是目录: %s", root)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("解析 workspace 路径失败: %w", err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return fmt.Errorf("转换绝对路径失败: %w", err)
	}
	m.mu.Lock()
	m.root = filepath.Clean(abs)
	m.mu.Unlock()
	return nil
}

// Root 返回当前工作区根目录；未设置时返回空串。
func (m *Manager) Root() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root
}

// IsSet 是否已设置工作区。
func (m *Manager) IsSet() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root != ""
}

// Resolve 将 rel（相对或绝对）解析为 workspace 内的绝对路径。
// 拒绝：未设置 workspace、.. 逃逸、越界绝对路径、符号链接逃逸。
// 目标不存在也能解析（路径本身合法即可）。
func (m *Manager) Resolve(rel string) (string, error) {
	m.mu.RLock()
	root := m.root
	m.mu.RUnlock()
	if root == "" {
		return "", ErrNoWorkspace
	}

	p := rel
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if !within(root, p) {
		return "", fmt.Errorf("%w: %s", ErrOutsideWorkspace, rel)
	}

	// 符号链接逃逸检查：
	//   - 目标已存在：解析真实路径再校验；
	//   - 目标不存在（如写入新文件）：解析「最近的已存在祖先」再校验。
	//     否则「符号链接目录 + 新文件名」会绕过检查，写到 workspace 之外。
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		if !within(root, resolved) {
			return "", fmt.Errorf("%w（符号链接逃逸）: %s", ErrOutsideWorkspace, rel)
		}
		return resolved, nil
	}
	ancestor, rest, err := resolveExistingAncestor(p)
	if err != nil {
		return "", err
	}
	if !within(root, ancestor) {
		return "", fmt.Errorf("%w（符号链接逃逸）: %s", ErrOutsideWorkspace, rel)
	}
	return filepath.Join(ancestor, rest), nil
}

// resolveExistingAncestor 返回 p 最近的已存在祖先的真实路径，以及 p 相对该祖先的剩余路径。
// 剩余路径不含 ".."（p 已 Clean 且通过 within 校验），拼接结果仍位于真实祖先之下。
func resolveExistingAncestor(p string) (ancestor, rest string, err error) {
	cur := p
	for {
		if real, e := filepath.EvalSymlinks(cur); e == nil {
			r, e := filepath.Rel(cur, p)
			if e != nil {
				return "", "", fmt.Errorf("解析路径失败: %w", e)
			}
			return real, r, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", fmt.Errorf("无法解析路径的已有祖先: %s", p)
		}
		cur = parent
	}
}

// within 判断 p 是否位于 root 内（含相等）。
func within(root, p string) bool {
	relToRoot, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if relToRoot == ".." {
		return false
	}
	// Windows 下根目录为盘符时 Rel 可能返回 "..\\x" 形式
	return !strings.HasPrefix(relToRoot, ".."+string(filepath.Separator))
}
