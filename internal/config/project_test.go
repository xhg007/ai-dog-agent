package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMergeProject_OverrideAndAppend 项目级同 ID/Name 覆盖全局，项目专属项追加，
// 且不修改传入的全局配置（不污染）。
func TestMergeProject_OverrideAndAppend(t *testing.T) {
	global := Config{
		Skills: []SkillConfig{
			{ID: "g1", Name: "Global", Prompt: "global"},
			{ID: "g2", Name: "Keep", Prompt: "keep"},
		},
		MCPServers: []MCPServerConfig{
			{ID: "m1", Name: "FS", Command: "old"},
			{ID: "m2", Name: "KeepMCP", Command: "keep"},
		},
	}
	pc := &ProjectConfig{
		Main: ProjectMain{Skills: []string{"g1"}, MCPServers: []string{"m1"}},
		Skills: []SkillConfig{
			{ID: "g1", Name: "Global", Prompt: "project"},
			{ID: "p1", Name: "Project", Prompt: "p"},
		},
		MCPServers: []MCPServerConfig{
			{ID: "m1", Name: "FS", Command: "new"},
			{ID: "p2", Name: "ProjMCP", Command: "p"},
		},
	}

	merged, warnings := MergeProject(global, pc)
	if len(warnings) != 0 {
		t.Fatalf("不应有引用 warning: %v", warnings)
	}
	if len(merged.Skills) != 3 {
		t.Fatalf("Skill 数量不符: %+v", merged.Skills)
	}
	if merged.Skills[0].ID != "g1" || merged.Skills[0].Prompt != "project" {
		t.Fatalf("项目级 Skill 应覆盖全局: %+v", merged.Skills[0])
	}
	if merged.Skills[1].ID != "g2" || merged.Skills[2].ID != "p1" {
		t.Fatalf("保留全局 + 追加项目专属不符: %+v", merged.Skills)
	}
	if len(merged.MCPServers) != 3 || merged.MCPServers[0].Command != "new" {
		t.Fatalf("MCP 合并不符: %+v", merged.MCPServers)
	}
	if merged.Project != pc {
		t.Fatal("合并结果应携带项目配置视图")
	}
	// 全局配置不得被就地修改
	if global.Skills[0].Prompt != "global" || global.MCPServers[0].Command != "old" {
		t.Fatalf("全局配置被污染: skills=%+v mcp=%+v", global.Skills, global.MCPServers)
	}
}

// TestMergeProject_DanglingRefWarnings 主 Agent 引用不存在的 Skill/MCP 时给出 warning。
func TestMergeProject_DanglingRefWarnings(t *testing.T) {
	global := Config{}
	pc := &ProjectConfig{Main: ProjectMain{
		Skills:     []string{"missing-skill"},
		MCPServers: []string{"missing-mcp"},
	}}
	_, warnings := MergeProject(global, pc)
	if len(warnings) != 2 {
		t.Fatalf("应返回两条悬空引用 warning，实际: %v", warnings)
	}
	if !strings.Contains(warnings[0], "missing-skill") || !strings.Contains(warnings[1], "missing-mcp") {
		t.Fatalf("warning 内容不符: %v", warnings)
	}
}

// TestSaveLoadProject 项目配置三个文件原子写入并可回读。
func TestSaveLoadProject(t *testing.T) {
	root := t.TempDir()
	pc := ProjectConfig{
		Main: ProjectMain{
			Model: "coder",
			Tools: []string{"read_file", "dispatch_coder"},
		},
		Skills:     []SkillConfig{{ID: "p1", Name: "P", Prompt: "p", Enabled: true}},
		MCPServers: []MCPServerConfig{{ID: "p2", Name: "Proj", Command: "echo"}},
	}
	if err := SaveProject(root, pc); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	for _, name := range []string{"main.yml", "mcp_servers.yml", "skills.yml"} {
		if _, err := os.Stat(filepath.Join(ProjectDir(root), name)); err != nil {
			t.Fatalf("缺少 %s: %v", name, err)
		}
	}
	got, err := LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if got.Main.Model != "coder" || len(got.Main.Tools) != 2 {
		t.Fatalf("main.yml 回读不符: %+v", got.Main)
	}
	if len(got.Skills) != 1 || got.Skills[0].ID != "p1" {
		t.Fatalf("skills.yml 回读不符: %+v", got.Skills)
	}
	if len(got.MCPServers) != 1 || got.MCPServers[0].ID != "p2" {
		t.Fatalf("mcp_servers.yml 回读不符: %+v", got.MCPServers)
	}
}

// TestManagerEffective 合并视图生效，项目配置不写入全局 config.yml。
func TestManagerEffective(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerAt(dir)
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	// 隔离真实 ~/.agents/skills，避免测试结果受本机技能目录影响。
	m.userSkillsDir = t.TempDir()
	global := Config{
		Models:        []ModelConfig{{ID: "x", Name: "X", Model: "xm"}},
		ActiveModelID: "x",
		Skills:        []SkillConfig{{ID: "g1", Name: "G", Prompt: "g", Enabled: true}},
	}
	if err := m.Save(global); err != nil {
		t.Fatalf("Save: %v", err)
	}
	root := t.TempDir()
	if err := SaveProject(root, ProjectConfig{
		Main:   ProjectMain{Model: "x"},
		Skills: []SkillConfig{{ID: "p1", Name: "P", Prompt: "p", Enabled: true}},
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := m.TrustProject(root); err != nil {
		t.Fatalf("TrustProject: %v", err)
	}

	eff := m.Effective(root)
	if len(eff.Skills) != 2 || eff.Project == nil || eff.Project.Main.Model != "x" {
		t.Fatalf("Effective 合并视图不符: skills=%+v project=%+v", eff.Skills, eff.Project)
	}
	// 全局配置保持不变
	if len(m.Get().Skills) != 1 || m.Get().Project != nil {
		t.Fatalf("全局配置被项目配置污染: %+v", m.Get())
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatalf("读取 config.yml: %v", err)
	}
	if strings.Contains(string(data), `"Project"`) || strings.Contains(string(data), `"project"`) {
		t.Fatalf("项目视图不应写入全局 config.yml: %s", data)
	}
	// 无工作区时直接返回全局配置
	if eff := m.Effective(""); len(eff.Skills) != 1 || eff.Project != nil {
		t.Fatalf("空工作区应返回全局配置: %+v", eff)
	}
}

// TestModelConfigContextWindowJSON ContextWindow/IncludeUsage 序列化与默认窗口。
func TestModelConfigContextWindowJSON(t *testing.T) {
	yes := true
	mc := ModelConfig{
		ID: "x", Name: "X", Model: "xm",
		ContextWindow: 128000, IncludeUsage: &yes,
	}
	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back ModelConfig
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.ContextWindow != 128000 || back.IncludeUsage == nil || !*back.IncludeUsage {
		t.Fatalf("ContextWindow/IncludeUsage round-trip 不符: %+v", back)
	}
	if back.EffectiveContextWindow() != 128000 {
		t.Fatalf("EffectiveContextWindow 应为显式值: %d", back.EffectiveContextWindow())
	}
	if (ModelConfig{}).EffectiveContextWindow() != DefaultContextWindow {
		t.Fatalf("未配置时应回退默认窗口 %d", DefaultContextWindow)
	}
	// 负数窗口被拒绝
	bad := Config{Models: []ModelConfig{{ID: "x", Name: "X", Model: "xm", ContextWindow: -1}}}
	if err := validateConfig(bad); err == nil {
		t.Fatal("负 ContextWindow 应校验失败")
	}
}
