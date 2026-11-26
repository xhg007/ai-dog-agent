package agent

import (
	"context"
	"strings"
	"testing"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/workspace"
)

func newWorkAgentTestConfig(t *testing.T, active string, models ...config.ModelConfig) *config.Manager {
	t.Helper()
	m, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	if err := m.Save(config.Config{Models: models, ActiveModelID: active}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return m
}

// TestBuildWorkInstruction 指令顺序：SystemPrompt → Skills → 项目上下文尾部。
func TestBuildWorkInstruction(t *testing.T) {
	spec := agentspec.Spec{Name: "coder", SystemPrompt: "你是 coder"}
	skills := []config.SkillConfig{{Name: "go-review", Description: "审查 Go", Prompt: "检查错误处理"}}
	got := buildWorkInstruction(spec, skills, "# 当前项目\nworkspace: /tmp/demo")

	iSystem := strings.Index(got, "你是 coder")
	iSkill := strings.Index(got, "go-review")
	iProject := strings.Index(got, "# 当前项目")
	if iSystem < 0 || iSkill < 0 || iProject < 0 {
		t.Fatalf("指令缺少必要段落: %q", got)
	}
	if !(iSystem < iSkill && iSkill < iProject) {
		t.Fatalf("指令顺序应为 SystemPrompt → Skills → 项目上下文: %q", got)
	}
	if !strings.Contains(got, "检查错误处理") {
		t.Fatalf("Skills 指令未写入: %q", got)
	}
}

// TestSelectMCPServersAndSkills 引用过滤与缺失引用容错。
func TestSelectMCPServersAndSkills(t *testing.T) {
	servers := []config.MCPServerConfig{{ID: "m1", Name: "fs"}, {ID: "m2", Name: "db"}}
	got := selectMCPServers(servers, []string{"fs", "m2", "missing"})
	if len(got) != 2 || got[0].ID != "m1" || got[1].ID != "m2" {
		t.Fatalf("MCP 过滤不符: %+v", got)
	}
	if selectMCPServers(servers, nil) != nil {
		t.Fatal("未声明 MCP 时应返回 nil（不暴露任何 server）")
	}

	skills := []config.SkillConfig{{ID: "s1", Name: "review"}}
	gotSkills := selectSkills(skills, []string{"s1", "missing"})
	if len(gotSkills) != 1 || gotSkills[0].ID != "s1" {
		t.Fatalf("Skill 过滤不符: %+v", gotSkills)
	}
}

// TestDispatchToolName 工具名合法化、中文回退与长度限制。
func TestDispatchToolName(t *testing.T) {
	cases := []struct {
		spec agentspec.Spec
		want string
	}{
		{agentspec.Spec{Name: "Coder", AgentID: "abc"}, "dispatch_coder"},
		{agentspec.Spec{Name: "code reviewer", AgentID: "abc"}, "dispatch_code_reviewer"},
		{agentspec.Spec{Name: "coder!!", AgentID: "abc"}, "dispatch_coder"},
		{agentspec.Spec{Name: "代码审查", AgentID: "0123456789abcdef"}, "dispatch_agent_01234567"},
	}
	for _, c := range cases {
		if got := DispatchToolName(c.spec); got != c.want {
			t.Fatalf("DispatchToolName(%q)=%q, want %q", c.spec.Name, got, c.want)
		}
	}
	a := DispatchToolName(agentspec.Spec{Name: "代码审查", AgentID: "aaaaaaaa1111"})
	b := DispatchToolName(agentspec.Spec{Name: "代码审查", AgentID: "bbbbbbbb2222"})
	if a == b {
		t.Fatalf("中文名称无法转写时应按 agent_id 区分: %q", a)
	}
	long := DispatchToolName(agentspec.Spec{Name: strings.Repeat("a", 100)})
	if len(long) > 64 {
		t.Fatalf("工具名超过 64 字符: %d", len(long))
	}
}

// TestBuildWorkAgent_ModelFallbackAndDefaults Spec.Model 找不到时回退主激活模型，
// 空 Tools 默认只读（由 tools.Catalog 保证），空 Budget 使用 work 默认上限。
func TestBuildWorkAgent_ModelFallbackAndDefaults(t *testing.T) {
	ws := setupProjectWS(t)
	cfg := newWorkAgentTestConfig(t, "main",
		config.ModelConfig{ID: "main", Name: "Main", BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk", Model: "main-model"},
		config.ModelConfig{ID: "coder", Name: "Coder", BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk", Model: "coder-model"},
	)
	spec := agentspec.Spec{
		AgentID: "agent-12345678", Name: "Coder", Description: "编码子 Agent",
		SystemPrompt: "你是 coder", Model: "missing-model",
	}
	wa, err := BuildWorkAgent(context.Background(), WorkAgentDeps{Config: cfg, Workspace: ws},
		spec, "main-agent", nil)
	if err != nil {
		t.Fatalf("BuildWorkAgent: %v", err)
	}
	if wa.Model.ID != "main" {
		t.Fatalf("Spec.Model 不存在时应回退主激活模型: %+v", wa.Model)
	}
	if wa.ToolName != "dispatch_coder" {
		t.Fatalf("dispatch 工具名不符: %q", wa.ToolName)
	}
	if wa.Limits.MaxSteps != DefaultWorkMaxSteps || wa.Limits.MaxTokens != DefaultWorkMaxTokens {
		t.Fatalf("空 Budget 应使用 work 默认上限: %+v", wa.Limits)
	}
	// Spec.Model 命中时使用指定模型
	spec.Model = "coder"
	wa2, err := BuildWorkAgent(context.Background(), WorkAgentDeps{Config: cfg, Workspace: ws}, spec, "main-agent", nil)
	if err != nil {
		t.Fatalf("BuildWorkAgent(coder): %v", err)
	}
	if wa2.Model.ID != "coder" {
		t.Fatalf("Spec.Model 应生效: %+v", wa2.Model)
	}
}

// TestBuildWorkAgent_Validation 缺少 agent_id 或 workspace 时返回错误。
func TestBuildWorkAgent_Validation(t *testing.T) {
	ws := setupProjectWS(t)
	cfg := newWorkAgentTestConfig(t, "main",
		config.ModelConfig{ID: "main", Name: "Main", BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk", Model: "m"},
	)
	if _, err := BuildWorkAgent(context.Background(), WorkAgentDeps{Config: cfg, Workspace: ws},
		agentspec.Spec{Name: "no-id", SystemPrompt: "x"}, "main", nil); err == nil {
		t.Fatal("缺少 agent_id 应报错")
	}
	if _, err := BuildWorkAgent(context.Background(), WorkAgentDeps{Config: cfg, Workspace: workspace.New()},
		agentspec.Spec{AgentID: "a1", Name: "x", SystemPrompt: "x"}, "main", nil); err == nil {
		t.Fatal("缺少 workspace 应报错")
	}
}
