package service

import (
	"fmt"

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
// ShellWhitelist 为空时填充默认白名单，保证前端设置页展示生效值。
func (c *ConfigService) GetConfig() config.Config {
	cfg := c.mgr.Get()
	if len(cfg.ShellWhitelist) == 0 {
		cfg.ShellWhitelist = config.DefaultShellWhitelist()
	}
	return cfg
}

// SaveConfig 保存配置（含校验：模型字段/ID 唯一/activeModelId 有效性）。
func (c *ConfigService) SaveConfig(cfg config.Config) error {
	return c.mgr.Save(cfg)
}

// SetActiveModel 切换当前激活模型。
// 前端只需上送模型 ID，避免「读整份配置 → 改一个字段 → 覆盖保存」丢失并发修改。
func (c *ConfigService) SetActiveModel(id string) error {
	cfg := c.mgr.Get()
	if id != "" {
		found := false
		for _, m := range cfg.Models {
			if m.ID == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("模型不存在: %s", id)
		}
	}
	cfg.ActiveModelID = id
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
