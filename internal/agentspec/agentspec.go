// Package agentspec 读写项目级子 Agent 配置：workspace/.aidog/agents/*.json。
package agentspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Spec 子 Agent 定义（需求文档 §3.1 约定的 Schema）。
// AgentID 是该子 Agent 的唯一不变 ID（UUID 无连字符）：
// 保存时缺省自动生成并固化；后续实例化/归因/缓存均以此为准。
type Spec struct {
	AgentID      string   `json:"agent_id,omitempty"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"system_prompt"`
	Model        string   `json:"model,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	Skills       []string `json:"skills,omitempty"`
	MCPServers   []string `json:"mcp_servers,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
}

// Dir 返回项目级 Agent 配置目录。
func Dir(root string) string {
	return filepath.Join(root, ".aidog", "agents")
}

func validateName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return errors.New("Agent 名称不能为空")
	}
	if n == "." || n == ".." || strings.ContainsAny(n, `/\`) || strings.Contains(n, "..") {
		return fmt.Errorf("非法 Agent 名称: %q", name)
	}
	return nil
}

func validate(spec Spec) error {
	if err := validateName(spec.Name); err != nil {
		return err
	}
	if strings.TrimSpace(spec.SystemPrompt) == "" {
		return errors.New("system_prompt 不能为空")
	}
	return nil
}

// Save 写入（覆盖）一个子 Agent 配置；agent_id 缺省时自动生成并固化。
// 已有同名配置则沿用其 ID（编辑保存不换 ID），仅全新配置生成新 ID。
func Save(root string, spec Spec) error {
	if err := validate(spec); err != nil {
		return err
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("未设置工作区，无法保存项目 Agent 配置")
	}
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	if !IsValidAgentID(spec.AgentID) {
		if old := readSpecFile(filepath.Join(dir, strings.TrimSpace(spec.Name)+".json")); old != nil && IsValidAgentID(old.AgentID) {
			spec.AgentID = old.AgentID // 编辑已有配置：ID 保持不变
		} else {
			spec.AgentID = NewAgentID()
		}
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(dir, strings.TrimSpace(spec.Name)+".json")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("替换配置失败: %w", err)
	}
	return nil
}

// readSpecFile 读取单个配置文件；损坏或不存在返回 nil。
func readSpecFile(path string) *Spec {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil || strings.TrimSpace(spec.Name) == "" {
		return nil
	}
	return &spec
}

// List 返回全部子 Agent 配置，按名称排序；目录不存在时返回空列表。
// 手工配置缺 agent_id 时自动生成并写回固化（迁移）。
func List(root string) ([]Spec, error) {
	if strings.TrimSpace(root) == "" {
		return []Spec{}, nil
	}
	entries, err := os.ReadDir(Dir(root))
	if errors.Is(err, os.ErrNotExist) {
		return []Spec{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Spec, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		spec := readSpecFile(filepath.Join(Dir(root), e.Name()))
		if spec == nil {
			continue // 跳过损坏文件，不影响其余配置
		}
		if !IsValidAgentID(spec.AgentID) {
			spec.AgentID = NewAgentID()
			if fixed, err := json.MarshalIndent(spec, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(Dir(root), e.Name()), fixed, 0o600)
			}
		}
		out = append(out, *spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete 删除指定名称的子 Agent 配置。
func Delete(root, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("未设置工作区")
	}
	if err := os.Remove(filepath.Join(Dir(root), strings.TrimSpace(name)+".json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("配置不存在: %s", name)
		}
		return err
	}
	return nil
}
