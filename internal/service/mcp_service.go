package service

import (
	"context"
	"time"

	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/mcp"
	"ai-dog-agent/internal/workspace"
)

// MCPService MCP Server 连接诊断（设置页「测试连接」）。
// 使用一次性连接，不影响会话中已复用的长连接。
type MCPService struct {
	ws  *workspace.Manager
	mgr *mcp.Manager
}

// NewMCPService 创建 MCP 诊断服务。
func NewMCPService(ws *workspace.Manager, mgr *mcp.Manager) *MCPService {
	return &MCPService{ws: ws, mgr: mgr}
}

// TestConnection 按传入配置临时连接 MCP Server，返回其工具名列表。
// 直接接收配置（而非已保存的 ID），因此设置页可以在保存前试连。
func (s *MCPService) TestConnection(sc config.MCPServerConfig) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.mgr.TestConnection(ctx, sc, s.ws.Root())
}
