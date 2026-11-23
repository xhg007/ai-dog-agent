package service

import (
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/tools"
	"ai-dog-agent/internal/workspace"
)

// ApprovalService 审批服务（Wails Service）：前端审批卡片决策与文件回滚。
type ApprovalService struct {
	mgr        *approval.Manager
	ws         *workspace.Manager
	backupRoot string
}

// NewApprovalService 创建审批服务。
func NewApprovalService(mgr *approval.Manager, ws *workspace.Manager, backupRoot string) *ApprovalService {
	return &ApprovalService{mgr: mgr, ws: ws, backupRoot: backupRoot}
}

// ListPending 返回当前全部待审批请求。
func (s *ApprovalService) ListPending() []eventbus.ApprovalRequestData {
	return s.mgr.ListPending()
}

// Decide 决策审批请求：approved 为是否批准；remember 为本次会话记住同路径。
func (s *ApprovalService) Decide(id string, approved, remember bool) error {
	return s.mgr.Decide(id, approved, remember)
}

// RollbackFile 从备份恢复指定相对路径的文件（FR-4.5 失败回滚）。
func (s *ApprovalService) RollbackFile(relPath string) error {
	return tools.RestoreFromBackup(s.ws, s.backupRoot, relPath)
}
