package agent

import (
	"strings"
	"testing"

	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/tools"
)

func newSmithTestConfig(t *testing.T) *config.Manager {
	t.Helper()
	m, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	if err := m.Save(config.Config{
		Models: []config.ModelConfig{{ID: "main", Name: "Main", Model: "m"}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return m
}

func testSmithInput() *SmithInput {
	return &SmithInput{
		Skills:     []config.SkillConfig{{ID: "s1", Name: "review", Description: "审查"}},
		MCPServers: []config.MCPServerConfig{{ID: "m1", Name: "fs"}},
	}
}

// TestParseSmithProposal 容忍代码块与前后说明文字。
func TestParseSmithProposal(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", `{"action":"skip","reason":"ok"}`, "skip"},
		{"code-fence", "```json\n{\"action\":\"create\",\"name\":\"x\"}\n```", "create"},
		{"extra-text", "说明\n{\"action\":\"update\",\"name\":\"x\"}\n尾部", "update"},
	}
	for _, c := range cases {
		p, err := parseSmithProposal(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.Action != c.want {
			t.Fatalf("%s: action=%q, want %q", c.name, p.Action, c.want)
		}
	}
}

// TestParseSmithProposalInvalid 空内容 / 无 JSON / 非法 JSON。
func TestParseSmithProposalInvalid(t *testing.T) {
	for _, raw := range []string{"", "no json here", "```json\n{bad}\n```"} {
		if _, err := parseSmithProposal(raw); err == nil {
			t.Fatalf("应解析失败: %q", raw)
		}
	}
}

// TestParseAndValidateSkip action=skip 直接返回，不校验 Spec。
func TestParseAndValidateSkip(t *testing.T) {
	s := &Smith{cfg: newSmithTestConfig(t)}
	p, err := s.parseAndValidate(testSmithInput(), `{"action":"skip","reason":"无需修改"}`)
	if err != nil {
		t.Fatalf("skip 应成功: %v", err)
	}
	if p.Action != "skip" || p.Reason != "无需修改" {
		t.Fatalf("skip 结果不符: %+v", p)
	}
}

// TestParseAndValidateValid 合法 create/update。
func TestParseAndValidateValid(t *testing.T) {
	s := &Smith{cfg: newSmithTestConfig(t)}
	raw := `{"action":"create","name":"coder","spec":{
		"name":"coder","description":"编码","system_prompt":"你是 coder",
		"model":"main","tools":["read_file","edit_file"],
		"skills":["review"],"mcp_servers":["fs"],
		"budget":{"max_steps":12},"context":{"compress_threshold":0.7,"keep_recent":6}
	},"reason":"需要编码 Agent"}`
	p, err := s.parseAndValidate(testSmithInput(), raw)
	if err != nil {
		t.Fatalf("合法输出应通过: %v", err)
	}
	if p.Spec.Name != "coder" || len(p.Spec.Tools) != 2 || p.Spec.Model != "main" {
		t.Fatalf("解析结果不符: %+v", p.Spec)
	}
}

// TestParseAndValidateErrors 各类非法输出。
func TestParseAndValidateErrors(t *testing.T) {
	s := &Smith{cfg: newSmithTestConfig(t)}
	cases := []struct {
		name string
		raw  string
	}{
		{"bad-action", `{"action":"delete","name":"x"}`},
		{"missing-name", `{"action":"create","spec":{"system_prompt":"x"}}`},
		{"missing-prompt", `{"action":"create","name":"x","spec":{"name":"x"}}`},
		{"unknown-tool", `{"action":"create","name":"x","spec":{"name":"x","system_prompt":"x","tools":["nope"]}}`},
		{"unknown-model", `{"action":"create","name":"x","spec":{"name":"x","system_prompt":"x","model":"nope"}}`},
		{"unknown-skill", `{"action":"create","name":"x","spec":{"name":"x","system_prompt":"x","skills":["nope"]}}`},
		{"unknown-mcp", `{"action":"create","name":"x","spec":{"name":"x","system_prompt":"x","mcp_servers":["nope"]}}`},
		{"bad-budget", `{"action":"create","name":"x","spec":{"name":"x","system_prompt":"x","budget":{"max_steps":-1}}}`},
	}
	for _, c := range cases {
		if _, err := s.parseAndValidate(testSmithInput(), c.raw); err == nil {
			t.Fatalf("%s: 应校验失败", c.name)
		}
	}
}

// TestBuildSmithPromptIncludesCatalogs 工具目录、Skill 指令摘要与 MCP 类型进入 smith 提示词。
func TestBuildSmithPromptIncludesCatalogs(t *testing.T) {
	in := &SmithInput{
		Tools: []tools.ToolOption{
			{Name: "dev_validate_go_code", Description: "Go 静态检查", Group: tools.ToolGroupDevTools},
		},
		Models: []config.ModelConfig{{ID: "main", Name: "Main", Model: "m"}},
		Skills: []config.SkillConfig{
			{ID: "s1", Name: "review", Description: "审查", Prompt: "检查错误处理与并发安全", Enabled: true},
		},
		MCPServers: []config.MCPServerConfig{
			{ID: "m1", Name: "fs", Command: "npx", Enabled: true},
		},
	}
	system, user, err := buildSmithPrompt(in)
	if err != nil {
		t.Fatalf("buildSmithPrompt: %v", err)
	}
	if !strings.Contains(system, tools.ToolGroupDevTools) {
		t.Fatalf("系统提示应说明 devtools 组: %s", system)
	}
	for _, want := range []string{
		"dev_validate_go_code [devtools]：Go 静态检查",
		"main / Main / m",
		"指令摘要：检查错误处理与并发安全",
		"fs（stdio，enabled=true）",
	} {
		if !strings.Contains(user, want) {
			t.Fatalf("用户提示缺少 %q:\n%s", want, user)
		}
	}
}

// TestParseAndValidateDevtoolsGroup smith 可直接引用 devtools 组。
func TestParseAndValidateDevtoolsGroup(t *testing.T) {
	s := &Smith{cfg: newSmithTestConfig(t)}
	raw := `{"action":"create","name":"x","spec":{"name":"x","system_prompt":"x","tools":["devtools"]}}`
	if _, err := s.parseAndValidate(&SmithInput{}, raw); err != nil {
		t.Fatalf("devtools 组应通过校验: %v", err)
	}
}
