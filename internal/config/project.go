package config

// 项目级配置分层（<workspace>/.aidog）：
//   - main.yml         主 Agent 覆盖与项目稳定 agent_id（模型 / 工具 / Skill / MCP / 预算 / 压缩）
//   - mcp_servers.yml  项目专属 MCP Server
//   - skills.yml       项目专属 Skill
//
// 项目级只在运行时与全局配置合并，不写回全局 config.yml。
// .aidog 可随仓库分发，加载时记录信任告警（信任确认 UI 留阶段 E）。

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"ai-dog-agent/internal/agentspec"
)

// ProjectMain 主 Agent 的项目级覆盖（.aidog/main.yml）。
type ProjectMain struct {
	// AgentID 项目主 Agent 稳定 ID（旧版 .aidog/agent_id 会自动迁移到此）。
	AgentID    string                  `json:"agent_id,omitempty" yaml:"agent_id,omitempty"`
	Model      string                  `json:"model,omitempty" yaml:"model,omitempty"`             // 覆盖激活模型（ID/Name/Model）
	Tools      []string                `json:"tools,omitempty" yaml:"tools,omitempty"`             // 主 Agent 工具白名单
	Skills     []string                `json:"skills,omitempty" yaml:"skills,omitempty"`           // 引用的 Skill（ID/Name）
	MCPServers []string                `json:"mcp_servers,omitempty" yaml:"mcp_servers,omitempty"` // 引用的 MCP Server（ID/Name）
	Budget     agentspec.Budget        `json:"budget,omitzero" yaml:"budget,omitempty"`
	Context    agentspec.ContextConfig `json:"context,omitzero" yaml:"context,omitempty"`
}

// ProjectConfig 项目级配置集合。
type ProjectConfig struct {
	Root       string            `json:"-" yaml:"-"`
	Main       ProjectMain       `json:"main,omitzero" yaml:"main,omitempty"`
	MCPServers []MCPServerConfig `json:"mcpServers,omitempty" yaml:"mcpServers,omitempty"`
	Skills     []SkillConfig     `json:"skills,omitempty" yaml:"skills,omitempty"`
}

// ProjectDir 返回项目级配置目录（<root>/.aidog）。
func ProjectDir(root string) string { return filepath.Join(root, ".aidog") }

// LoadProject 读取项目级配置。目录/文件缺失视为未配置；解析失败返回错误，
// 避免把损坏的配置静默当作空配置处理。
func LoadProject(root string) (*ProjectConfig, error) {
	pc := &ProjectConfig{Root: root}
	if strings.TrimSpace(root) == "" {
		return pc, nil
	}
	dir := ProjectDir(root)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pc, nil
		}
		return nil, fmt.Errorf("检查项目配置目录失败: %w", err)
	}
	// 信任边界：.aidog 可被仓库携带，MCP command / Skill 指令等同可执行配置。
	slog.Warn("检测到项目级配置目录 .aidog，其内容可随仓库分发，请确认项目来源可信",
		"root", root)
	if err := loadProjectFile(dir, "main", &pc.Main); err != nil {
		return nil, err
	}
	if err := loadProjectFile(dir, "mcp_servers", &pc.MCPServers); err != nil {
		return nil, err
	}
	if err := loadProjectFile(dir, "skills", &pc.Skills); err != nil {
		return nil, err
	}
	return pc, nil
}

// loadProjectFile 读取项目配置（.yml）；不存在时尝试旧 .json 并自动迁移。
func loadProjectFile(dir, base string, dst any) error {
	target := filepath.Join(dir, base+".yml")
	exists, err := readYAMLFile(target, dst)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = migrateJSONToYAML(target, filepath.Join(dir, base+".json"), dst)
	return err
}

// SaveProject 写入项目级配置（main.yml / mcp_servers.yml / skills.yml）。
// 写入前按全局配置同样的规则校验 MCP/Skill；采用临时文件 + rename 原子替换。
func SaveProject(root string, pc ProjectConfig) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("未设置工作区，无法保存项目配置")
	}
	// 目录扫描的 Skill 是只读来源，写盘前剔除，避免前端全量回写污染 skills.yml。
	pc.Skills = FilterPersistedSkills(pc.Skills)
	if err := validateConfig(Config{MCPServers: pc.MCPServers, Skills: pc.Skills}); err != nil {
		return err
	}
	dir := ProjectDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建项目配置目录失败: %w", err)
	}
	if err := writeYAMLAtomic(filepath.Join(dir, "main.yml"), pc.Main); err != nil {
		return err
	}
	if err := writeYAMLAtomic(filepath.Join(dir, "mcp_servers.yml"), pc.MCPServers); err != nil {
		return err
	}
	if err := writeYAMLAtomic(filepath.Join(dir, "skills.yml"), pc.Skills); err != nil {
		return err
	}
	// 旧 JSON 保存后统一改名备份，避免下次加载误读
	for _, base := range []string{"main", "mcp_servers", "skills"} {
		legacy := filepath.Join(dir, base+".json")
		if _, err := os.Stat(legacy); err == nil {
			if err := os.Rename(legacy, legacy+".bak"); err != nil {
				slog.Warn("旧项目配置备份失败", "file", legacy, "err", err)
			}
		}
	}
	return nil
}

// EnsureProjectAgentID 读取/生成项目主 Agent 稳定 ID，并写入 .aidog/main.yml。
// 旧版 .aidog/agent_id 会迁移进 main.yml，原文件重命名为 .bak。
func EnsureProjectAgentID(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", agentspec.ErrNoWorkspace
	}
	pc, err := LoadProject(root)
	if err != nil {
		return "", err
	}
	if id := strings.TrimSpace(pc.Main.AgentID); agentspec.IsValidAgentID(id) {
		return id, nil
	}
	legacy := filepath.Join(ProjectDir(root), "agent_id")
	id := ""
	if b, err := os.ReadFile(legacy); err == nil {
		if v := strings.TrimSpace(string(b)); agentspec.IsValidAgentID(v) {
			id = v
		}
	}
	if id == "" {
		id = agentspec.NewAgentID()
	}
	pc.Main.AgentID = id
	dir := ProjectDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建项目配置目录失败: %w", err)
	}
	if err := writeYAMLAtomic(filepath.Join(dir, "main.yml"), pc.Main); err != nil {
		return "", err
	}
	if _, err := os.Stat(legacy); err == nil {
		if err := os.Rename(legacy, legacy+".bak"); err != nil {
			slog.Warn("旧 agent_id 已迁移，但备份重命名失败", "file", legacy, "err", err)
		}
	}
	return id, nil
}

// MergeProject 把项目级配置合并进全局配置副本：
//   - MCP Server / Skill：项目级与全局同 ID/Name 时项目级优先，其余追加；
//   - 主 Agent 覆盖通过 Config.Project 暴露，由 agent 层解析；
//   - 主 Agent 引用了不存在的 Skill/MCP 时返回 warning（不视为错误）。
func MergeProject(global Config, pc *ProjectConfig) (Config, []string) {
	out := clone(global)
	if pc == nil {
		return out, nil
	}
	out.MCPServers = mergeMCPServers(out.MCPServers, pc.MCPServers)
	out.Skills = mergeSkills(out.Skills, pc.Skills)
	out.Project = pc
	return out, projectWarnings(out, pc)
}

func mergeMCPServers(global, project []MCPServerConfig) []MCPServerConfig {
	out := append([]MCPServerConfig(nil), global...)
	for _, p := range project {
		replaced := false
		for i := range out {
			if sameMCPServer(out[i], p) {
				out[i] = p
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, p)
		}
	}
	return out
}

func sameMCPServer(a, b MCPServerConfig) bool {
	if a.ID != "" && b.ID != "" && a.ID == b.ID {
		return true
	}
	return a.Name != "" && b.Name != "" && a.Name == b.Name
}

func mergeSkills(global, project []SkillConfig) []SkillConfig {
	out := append([]SkillConfig(nil), global...)
	for _, p := range project {
		replaced := false
		for i := range out {
			if sameSkill(out[i], p) {
				out[i] = p
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, p)
		}
	}
	return out
}

func sameSkill(a, b SkillConfig) bool {
	if a.ID != "" && b.ID != "" && a.ID == b.ID {
		return true
	}
	return a.Name != "" && b.Name != "" && a.Name == b.Name
}

// projectWarnings 检查主 Agent 覆盖里的 Skill/MCP 引用是否存在。
func projectWarnings(merged Config, pc *ProjectConfig) []string {
	var warnings []string
	for _, ref := range pc.Main.Skills {
		if !hasSkill(merged.Skills, ref) {
			warnings = append(warnings,
				fmt.Sprintf("项目主 Agent 引用的 Skill 不存在: %s", ref))
		}
	}
	for _, ref := range pc.Main.MCPServers {
		if !hasMCPServer(merged.MCPServers, ref) {
			warnings = append(warnings,
				fmt.Sprintf("项目主 Agent 引用的 MCP Server 不存在: %s", ref))
		}
	}
	return warnings
}

func hasSkill(skills []SkillConfig, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	for _, sk := range skills {
		if sk.ID == ref || sk.Name == ref {
			return true
		}
	}
	return false
}

func hasMCPServer(servers []MCPServerConfig, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	for _, s := range servers {
		if s.ID == ref || s.Name == ref {
			return true
		}
	}
	return false
}
