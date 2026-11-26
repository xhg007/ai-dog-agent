package config

// 项目级配置分层（<workspace>/.aidog）：
//   - main.json        主 Agent 覆盖（模型引用 / 工具 / Skill / MCP / 预算 / 压缩）
//   - mcp_servers.json 项目专属 MCP Server
//   - skills.json      项目专属 Skill
//
// 项目级只在运行时与全局配置合并，不写回全局 config.json。
// .aidog 可随仓库分发，加载时记录信任告警（信任确认 UI 留阶段 E）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"ai-dog-agent/internal/agentspec"
)

// ProjectMain 主 Agent 的项目级覆盖（.aidog/main.json）。
type ProjectMain struct {
	Model      string                  `json:"model,omitempty"`       // 覆盖激活模型（ID/Name/Model）
	Tools      []string                `json:"tools,omitempty"`       // 主 Agent 工具白名单
	Skills     []string                `json:"skills,omitempty"`      // 引用的 Skill（ID/Name）
	MCPServers []string                `json:"mcp_servers,omitempty"` // 引用的 MCP Server（ID/Name）
	Budget     agentspec.Budget        `json:"budget,omitzero"`
	Context    agentspec.ContextConfig `json:"context,omitzero"`
}

// ProjectConfig 项目级配置集合。
type ProjectConfig struct {
	Root       string            `json:"-"`
	Main       ProjectMain       `json:"main,omitzero"`
	MCPServers []MCPServerConfig `json:"mcpServers,omitempty"`
	Skills     []SkillConfig     `json:"skills,omitempty"`
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
	if err := readJSONFile(filepath.Join(dir, "main.json"), &pc.Main); err != nil {
		return nil, err
	}
	if err := readJSONFile(filepath.Join(dir, "mcp_servers.json"), &pc.MCPServers); err != nil {
		return nil, err
	}
	if err := readJSONFile(filepath.Join(dir, "skills.json"), &pc.Skills); err != nil {
		return nil, err
	}
	return pc, nil
}

// readJSONFile 读取 JSON 文件到 dst；文件不存在或为空视为未配置。
func readJSONFile(path string, dst any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return nil
}

// SaveProject 写入项目级配置（main.json / mcp_servers.json / skills.json）。
// 写入前按全局配置同样的规则校验 MCP/Skill；采用临时文件 + rename 原子替换。
func SaveProject(root string, pc ProjectConfig) error {
	if strings.TrimSpace(root) == "" {
		return errors.New("未设置工作区，无法保存项目配置")
	}
	if err := validateConfig(Config{MCPServers: pc.MCPServers, Skills: pc.Skills}); err != nil {
		return err
	}
	dir := ProjectDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建项目配置目录失败: %w", err)
	}
	if err := writeJSONAtomic(filepath.Join(dir, "main.json"), pc.Main); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(dir, "mcp_servers.json"), pc.MCPServers); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(dir, "skills.json"), pc.Skills)
}

// writeJSONAtomic 以 0600 权限原子写入 JSON（空切片写为 []，便于人工编辑）。
func writeJSONAtomic(path string, v any) error {
	var data []byte
	var err error
	switch t := v.(type) {
	case []MCPServerConfig:
		if t == nil {
			data = []byte("[]")
		}
	case []SkillConfig:
		if t == nil {
			data = []byte("[]")
		}
	}
	if data == nil {
		data, err = json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("序列化 %s 失败: %w", filepath.Base(path), err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换 %s 失败: %w", filepath.Base(path), err)
	}
	return nil
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
