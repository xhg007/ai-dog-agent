package config

// 项目配置信任边界（阶段 E）：
//   - 项目配置默认不加载，需用户显式 TrustProject；
//   - 高危审计命中自动 BlockProject（并移除信任）；
//   - 解除拉黑（UnblockProject）不会自动恢复信任，需再次确认。

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// ProjectTrustStatus 项目配置信任状态。
type ProjectTrustStatus string

const (
	ProjectTrusted   ProjectTrustStatus = "trusted"
	ProjectUntrusted ProjectTrustStatus = "untrusted"
	ProjectBlocked   ProjectTrustStatus = "blocked"
)

// ProjectStatus 项目配置状态与审计报告。
type ProjectStatus struct {
	Root   string             `json:"root"`
	Status ProjectTrustStatus `json:"status"`
	Audit  AuditReport        `json:"audit"`
}

// NormalizeProjectPath 规范化项目根：绝对路径 + Clean + Windows 小写。
func NormalizeProjectPath(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs, nil
}

// ProjectStatus 返回信任状态并执行审计；高危项目自动拉黑。
func (m *Manager) ProjectStatus(root string) (ProjectStatus, error) {
	norm, err := NormalizeProjectPath(root)
	if err != nil {
		return ProjectStatus{}, err
	}
	report := AuditProject(norm)
	status := ProjectUntrusted
	m.mu.RLock()
	trusted := containsPath(m.cfg.TrustedProjects, norm)
	blocked := containsPath(m.cfg.BlockedProjects, norm)
	m.mu.RUnlock()
	switch {
	case blocked:
		status = ProjectBlocked
	case trusted:
		// 已显式信任的项目即使审计命中高危也保持 trusted（用户已二次确认）
		status = ProjectTrusted
	case report.Blocked:
		if err := m.BlockProject(norm); err != nil {
			return ProjectStatus{}, err
		}
		status = ProjectBlocked
	}
	return ProjectStatus{Root: norm, Status: status, Audit: report}, nil
}

// TrustProject 标记项目配置为可信；已拉黑项目需先解除拉黑。
func (m *Manager) TrustProject(root string) error {
	norm, err := NormalizeProjectPath(root)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if containsPath(m.cfg.BlockedProjects, norm) {
		return fmt.Errorf("项目因高危审计已被拉黑，请先解除拉黑: %s", norm)
	}
	if containsPath(m.cfg.TrustedProjects, norm) {
		return nil
	}
	prev := clone(m.cfg)
	m.cfg.TrustedProjects = append(m.cfg.TrustedProjects, norm)
	if err := m.persistLocked(); err != nil {
		m.cfg = prev
		return err
	}
	m.version++
	return nil
}

// UntrustProject 取消项目信任。
func (m *Manager) UntrustProject(root string) error {
	norm, err := NormalizeProjectPath(root)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !containsPath(m.cfg.TrustedProjects, norm) {
		return nil
	}
	prev := clone(m.cfg)
	m.cfg.TrustedProjects = removePath(m.cfg.TrustedProjects, norm)
	if err := m.persistLocked(); err != nil {
		m.cfg = prev
		return err
	}
	m.version++
	return nil
}

// BlockProject 拉黑项目（审计高危自动调用）；同时移除信任。
func (m *Manager) BlockProject(root string) error {
	norm, err := NormalizeProjectPath(root)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	blocked := containsPath(m.cfg.BlockedProjects, norm)
	trusted := containsPath(m.cfg.TrustedProjects, norm)
	if blocked && !trusted {
		return nil
	}
	prev := clone(m.cfg)
	if !blocked {
		m.cfg.BlockedProjects = append(m.cfg.BlockedProjects, norm)
	}
	m.cfg.TrustedProjects = removePath(m.cfg.TrustedProjects, norm)
	if err := m.persistLocked(); err != nil {
		m.cfg = prev
		return err
	}
	m.version++
	return nil
}

// UnblockProject 解除拉黑（不会自动信任）。
func (m *Manager) UnblockProject(root string) error {
	norm, err := NormalizeProjectPath(root)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !containsPath(m.cfg.BlockedProjects, norm) {
		return nil
	}
	prev := clone(m.cfg)
	m.cfg.BlockedProjects = removePath(m.cfg.BlockedProjects, norm)
	if err := m.persistLocked(); err != nil {
		m.cfg = prev
		return err
	}
	m.version++
	return nil
}

func containsPath(list []string, path string) bool {
	for _, p := range list {
		if strings.EqualFold(p, path) {
			return true
		}
	}
	return false
}

func removePath(list []string, path string) []string {
	out := list[:0]
	for _, p := range list {
		if !strings.EqualFold(p, path) {
			out = append(out, p)
		}
	}
	return out
}
