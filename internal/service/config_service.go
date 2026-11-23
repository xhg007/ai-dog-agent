package service

import (
	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/workspace"
)

// ConfigService 配置服务（Wails Service）：应用配置 + 项目级 Agent 配置读写。
type ConfigService struct {
	mgr *config.Manager
	ws  *workspace.Manager
}

// NewConfigService 创建配置服务。
func NewConfigService(mgr *config.Manager, ws *workspace.Manager) *ConfigService {
	return &ConfigService{mgr: mgr, ws: ws}
}

// GetConfig 返回当前配置（含模型 / Skills / MCP Server）。
func (c *ConfigService) GetConfig() config.Config {
	return c.mgr.Get()
}

// SaveConfig 保存配置。
func (c *ConfigService) SaveConfig(cfg config.Config) error {
	return c.mgr.Save(cfg)
}

// GetConfigDir 返回配置目录路径（便于前端提示）。
func (c *ConfigService) GetConfigDir() string {
	dir, _ := config.Dir()
	return dir
}

// GetProjectAgentDir 返回项目级 Agent 配置目录；未设置工作区时为空串。
func (c *ConfigService) GetProjectAgentDir() string {
	if !c.ws.IsSet() {
		return ""
	}
	return agentspec.Dir(c.ws.Root())
}

// GetProjectAgents 返回当前项目 .aidog/agents/*.json 中的全部子 Agent 定义。
func (c *ConfigService) GetProjectAgents() ([]agentspec.Spec, error) {
	return agentspec.List(c.ws.Root())
}

// SaveProjectAgent 写入（覆盖）一个项目级子 Agent 配置。
func (c *ConfigService) SaveProjectAgent(spec agentspec.Spec) error {
	return agentspec.Save(c.ws.Root(), spec)
}

// DeleteProjectAgent 删除一个项目级子 Agent 配置。
func (c *ConfigService) DeleteProjectAgent(name string) error {
	return agentspec.Delete(c.ws.Root(), name)
}
