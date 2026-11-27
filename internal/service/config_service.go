package service

import (
	"errors"
	"fmt"
	"strings"

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

// SaveConfig 保存设置页配置（模型 / Skills / MCP / 白名单等）。
// 工作区与项目信任状态由 WorkspaceService / TrustProject 单独维护，设置页不编辑；
// 前端全量保存时这些字段会缺失，这里保留现有值，避免被空值覆盖。
func (c *ConfigService) SaveConfig(cfg config.Config) error {
	current := c.mgr.Get()
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		cfg.WorkspacePath = current.WorkspacePath
	}
	if cfg.TrustedProjects == nil {
		cfg.TrustedProjects = current.TrustedProjects
	}
	if cfg.BlockedProjects == nil {
		cfg.BlockedProjects = current.BlockedProjects
	}
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

// GetProjectStatus 返回项目配置信任状态与审计报告。
func (c *ConfigService) GetProjectStatus() (config.ProjectStatus, error) {
	if !c.ws.IsSet() {
		return config.ProjectStatus{}, errors.New("未设置工作区")
	}
	return c.mgr.ProjectStatus(c.ws.Root())
}

// TrustProject 信任当前项目配置；已拉黑项目需先解除拉黑。
func (c *ConfigService) TrustProject() error {
	if !c.ws.IsSet() {
		return errors.New("未设置工作区")
	}
	return c.mgr.TrustProject(c.ws.Root())
}

// UntrustProject 取消当前项目信任。
func (c *ConfigService) UntrustProject() error {
	if !c.ws.IsSet() {
		return errors.New("未设置工作区")
	}
	return c.mgr.UntrustProject(c.ws.Root())
}

// UnblockProject 解除当前项目拉黑（不会自动信任）。
func (c *ConfigService) UnblockProject() error {
	if !c.ws.IsSet() {
		return errors.New("未设置工作区")
	}
	return c.mgr.UnblockProject(c.ws.Root())
}

// GetProjectConfig 返回项目级配置（.aidog 目录；未设置工作区时返回空配置）。
func (c *ConfigService) GetProjectConfig() (config.ProjectConfig, error) {
	if !c.ws.IsSet() {
		return config.ProjectConfig{}, nil
	}
	pc, err := config.LoadProject(c.ws.Root())
	if err != nil {
		return config.ProjectConfig{}, err
	}
	return *pc, nil
}

// SaveProjectConfig 保存项目级主 Agent 覆盖与项目专属 MCP/Skill。
func (c *ConfigService) SaveProjectConfig(pc config.ProjectConfig) error {
	if !c.ws.IsSet() {
		return errors.New("未设置工作区，无法保存项目配置")
	}
	return config.SaveProject(c.ws.Root(), pc)
}

// GetProjectMCPServers 返回项目专属 MCP Server。
func (c *ConfigService) GetProjectMCPServers() ([]config.MCPServerConfig, error) {
	pc, err := c.GetProjectConfig()
	if err != nil {
		return nil, err
	}
	return pc.MCPServers, nil
}

// SaveProjectMCPServers 覆盖保存项目专属 MCP Server（保留主 Agent 覆盖与 Skill）。
func (c *ConfigService) SaveProjectMCPServers(servers []config.MCPServerConfig) error {
	pc, err := c.GetProjectConfig()
	if err != nil {
		return err
	}
	pc.MCPServers = servers
	return c.SaveProjectConfig(pc)
}

// GetProjectSkills 返回项目专属 Skill。
func (c *ConfigService) GetProjectSkills() ([]config.SkillConfig, error) {
	pc, err := c.GetProjectConfig()
	if err != nil {
		return nil, err
	}
	return pc.Skills, nil
}

// SaveProjectSkills 覆盖保存项目专属 Skill（保留主 Agent 覆盖与 MCP）。
func (c *ConfigService) SaveProjectSkills(skills []config.SkillConfig) error {
	pc, err := c.GetProjectConfig()
	if err != nil {
		return err
	}
	pc.Skills = skills
	return c.SaveProjectConfig(pc)
}

// ListAgentBackups 返回指定项目 Agent 的配置备份（最新在前）。
func (c *ConfigService) ListAgentBackups(name string) ([]agentspec.BackupInfo, error) {
	if !c.ws.IsSet() {
		return []agentspec.BackupInfo{}, nil
	}
	return agentspec.ListBackups(c.ws.Root(), name)
}

// RestoreProjectAgent 用指定备份恢复 Agent 配置（恢复前会备份当前配置）。
func (c *ConfigService) RestoreProjectAgent(name, backupPath string) error {
	if !c.ws.IsSet() {
		return errors.New("未设置工作区")
	}
	return agentspec.Restore(c.ws.Root(), name, backupPath)
}
