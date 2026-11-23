package service

import (
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/tools"
	"ai-dog-agent/internal/workspace"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// WorkspaceService 工作区服务（Wails Service）。
type WorkspaceService struct {
	mgr *config.Manager
	ws  *workspace.Manager
}

// NewWorkspaceService 创建工作区服务。
func NewWorkspaceService(mgr *config.Manager, ws *workspace.Manager) *WorkspaceService {
	return &WorkspaceService{mgr: mgr, ws: ws}
}

// SetWorkspace 设置工作区（校验并持久化到配置）。
func (s *WorkspaceService) SetWorkspace(path string) error {
	if err := s.ws.SetRoot(path); err != nil {
		return err
	}
	cfg := s.mgr.Get()
	cfg.WorkspacePath = s.ws.Root()
	return s.mgr.Save(cfg)
}

// GetWorkspace 返回当前工作区路径（未设置为空串）。
func (s *WorkspaceService) GetWorkspace() string {
	return s.ws.Root()
}

// BrowseWorkspace 打开系统目录选择对话框并设置为工作区。
func (s *WorkspaceService) BrowseWorkspace() (string, error) {
	path, err := application.Get().Dialog.
		OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(false).
		SetTitle("选择项目目录").
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil // 用户取消
	}
	if err := s.SetWorkspace(path); err != nil {
		return "", err
	}
	return s.ws.Root(), nil
}

// GitStatus 返回工作区 git 状态（porcelain 格式），供审查面板展示。
func (s *WorkspaceService) GitStatus() (string, error) {
	return tools.GitStatus(s.ws)
}

// GitDiff 返回工作区相对 HEAD 的变更 diff，供审查面板展示。
func (s *WorkspaceService) GitDiff() (string, error) {
	return tools.GitDiff(s.ws)
}
