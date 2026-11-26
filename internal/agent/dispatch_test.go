package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
)

// TestRenderOrchestratorDispatches instruction 使用实际 dispatch 工具名；
// 无可用工具时给出明确提示。
func TestRenderOrchestratorDispatches(t *testing.T) {
	out, err := renderOrchestratorPrompt(PromptData{Dispatches: []DispatchInfo{
		{Name: "dispatch_coder", Description: "编码"},
	}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "dispatch_coder") || !strings.Contains(out, "编码") {
		t.Fatalf("instruction 未包含实际工具名: %s", out)
	}
	empty, err := renderOrchestratorPrompt(PromptData{})
	if err != nil {
		t.Fatalf("render(empty): %v", err)
	}
	if !strings.Contains(empty, "没有可用的子 Agent 分派工具") {
		t.Fatalf("无分派工具时应明确提示: %s", empty)
	}
}

// TestEnsureBuiltinExplorer 内置 explorer 只出现一次。
func TestEnsureBuiltinExplorer(t *testing.T) {
	got := ensureBuiltinExplorer(nil)
	if len(got) != 1 || got[0].Name != BuiltinExplorerTool {
		t.Fatalf("应补充内置 explorer: %+v", got)
	}
	got = ensureBuiltinExplorer([]DispatchInfo{{Name: BuiltinExplorerTool, Description: "project"}})
	if len(got) != 1 || got[0].Description != "project" {
		t.Fatalf("项目已覆盖时不应重复: %+v", got)
	}
}

// TestBuildDispatchToolsDedup 同名 Agent 只生成一个 dispatch 工具。
func TestBuildDispatchToolsDedup(t *testing.T) {
	ws := setupProjectWS(t)
	cfg := newWorkAgentTestConfig(t, "main",
		config.ModelConfig{ID: "main", Name: "Main", BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk", Model: "m"},
	)
	svc := &ChatService{
		cfg: cfg, ws: ws, bus: eventbus.New(),
		sessions: map[string]*chatSession{"s1": {id: "s1", agentID: "main-agent"}},
	}
	sess := svc.sessions["s1"]
	specs := []agentspec.Spec{
		{AgentID: "a1", Name: "coder", Description: "编码", SystemPrompt: "你是 coder"},
		{AgentID: "a2", Name: "coder", Description: "重复", SystemPrompt: "你是第二个 coder"},
	}
	tools, infos := svc.buildDispatchTools(context.Background(), sess, specs)
	if len(tools) != 1 || len(infos) != 1 {
		t.Fatalf("同名工具应去重: tools=%d infos=%d", len(tools), len(infos))
	}
	if infos[0].Name != "dispatch_coder" {
		t.Fatalf("工具名不符: %+v", infos)
	}
}

// TestOrchestratorToolsExplorerOverride extraTools 已含 dispatch_explorer 时
// 不再注册内置 explorer（项目配置覆盖）。
func TestOrchestratorToolsExplorerOverride(t *testing.T) {
	ws := setupProjectWS(t)
	fake, err := utils.InferTool("dispatch_explorer", "项目自定义 explorer",
		func(ctx context.Context, _ struct{}) (string, error) { return "", nil })
	if err != nil {
		t.Fatalf("InferTool: %v", err)
	}
	list, err := OrchestratorTools(context.Background(), ws, &fakeModel{}, nil, "", "s1", nil,
		[]tool.BaseTool{fake})
	if err != nil {
		t.Fatalf("OrchestratorTools: %v", err)
	}
	count := 0
	for _, tl := range list {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if info.Name == BuiltinExplorerTool {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("dispatch_explorer 应只注册一次（项目覆盖内置），实际 %d", count)
	}
}

// TestOrchestratorToolsDefaultExplorer 未提供覆盖时保留内置 explorer。
func TestOrchestratorToolsDefaultExplorer(t *testing.T) {
	ws := setupProjectWS(t)
	list, err := OrchestratorTools(context.Background(), ws, &fakeModel{}, nil, "", "s1", nil, nil)
	if err != nil {
		t.Fatalf("OrchestratorTools: %v", err)
	}
	found := false
	for _, tl := range list {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if info.Name == BuiltinExplorerTool {
			found = true
		}
	}
	if !found {
		t.Fatal("未提供覆盖时应注册内置 explorer")
	}
}

// TestFreshSessionUsesDynamicDispatch 会话装配把项目 Agent 转为 dispatch 工具，
// 且提示词与实际工具名一致。
func TestFreshSessionUsesDynamicDispatch(t *testing.T) {
	ws := setupProjectWS(t)
	cfg := newWorkAgentTestConfig(t, "main",
		config.ModelConfig{ID: "main", Name: "Main", BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk", Model: "m"},
	)
	svc := &ChatService{cfg: cfg, ws: ws, bus: eventbus.New()}
	sess := &chatSession{id: "s1", agentID: "main-agent"}
	dispatches, infos := svc.buildDispatchTools(context.Background(), sess, []agentspec.Spec{
		{AgentID: "a1", Name: "coder", Description: "编码", SystemPrompt: "你是 coder"},
	})
	if len(dispatches) != 1 || len(infos) != 1 {
		t.Fatalf("应生成一个 dispatch 工具: %d", len(dispatches))
	}
	infos = ensureBuiltinExplorer(infos)
	if len(infos) != 2 || infos[0].Name != BuiltinExplorerTool || infos[1].Name != "dispatch_coder" {
		t.Fatalf("分派工具列表不符: %+v", infos)
	}
	out, err := renderOrchestratorPrompt(PromptData{Dispatches: infos})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, name := range []string{BuiltinExplorerTool, "dispatch_coder"} {
		if !strings.Contains(out, name) {
			t.Fatalf("instruction 缺少工具名 %q", name)
		}
	}
}
