package llm

import (
	"context"
	"testing"

	"ai-dog-agent/internal/config"
)

// TestResolveModel 验证 ID → Name → Model 的解析优先级与歧义处理。
func TestResolveModel(t *testing.T) {
	cfg := config.Config{Models: []config.ModelConfig{
		{ID: "a", Name: "Alpha", Model: "alpha-model"},
		{ID: "b", Name: "Beta", Model: "beta-model"},
		{ID: "dup1", Name: "Same", Model: "m1"},
		{ID: "dup2", Name: "Same", Model: "m2"},
	}}
	cases := []struct {
		ref       string
		wantID    string
		wantFound bool
	}{
		{"a", "a", true},          // ID 命中
		{"Alpha", "a", true},      // Name 命中
		{"beta-model", "b", true}, // Model 命中
		{"Same", "", false},       // Name 层歧义，Model 层无匹配
		{"missing", "", false},    // 完全找不到
		{"", "", false},           // 空引用
		{"  a  ", "a", true},      // 去除首尾空白
	}
	for _, c := range cases {
		got, ok := ResolveModel(cfg, c.ref)
		if ok != c.wantFound {
			t.Fatalf("ResolveModel(%q) found=%v, want %v", c.ref, ok, c.wantFound)
		}
		if ok && got.ID != c.wantID {
			t.Fatalf("ResolveModel(%q) id=%q, want %q", c.ref, got.ID, c.wantID)
		}
	}
}

func newRouteTestManager(t *testing.T, models []config.ModelConfig, active string) *config.Manager {
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

func routeTestModels() []config.ModelConfig {
	return []config.ModelConfig{
		{ID: "main", Name: "Main", BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk", Model: "main-model"},
		{ID: "coder", Name: "Coder", BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk", Model: "coder-model"},
		{ID: "other", Name: "Other", BaseURL: "http://127.0.0.1:1/v1", APIKey: "sk", Model: "other-model"},
	}
}

// TestBuildWorkModelChain_PrimaryAndFallbackOrder 验证 Spec.Model 命中与回退，
// 以及备用链顺序（主模型优先、其余全局模型按配置顺序，包含主激活模型）。
func TestBuildWorkModelChain_PrimaryAndFallbackOrder(t *testing.T) {
	m := newRouteTestManager(t, routeTestModels(), "main")
	cases := []struct {
		spec        string
		wantPrimary string
		wantFirst   string
	}{
		{"coder", "coder", "Coder"}, // ID 命中
		{"Coder", "coder", "Coder"}, // Name 命中
		{"", "main", "Main"},        // 未配置：跟随主激活模型
		{"missing", "main", "Main"}, // 找不到：回退主激活模型
	}
	for _, c := range cases {
		cm, name, primary, err := BuildWorkModelChain(context.Background(), m,
			"agent-w", "user-main", c.spec, nil)
		if err != nil {
			t.Fatalf("spec=%q: BuildWorkModelChain: %v", c.spec, err)
		}
		if primary.ID != c.wantPrimary {
			t.Fatalf("spec=%q: primary=%q, want %q", c.spec, primary.ID, c.wantPrimary)
		}
		if name != c.wantFirst {
			t.Fatalf("spec=%q: name=%q, want %q", c.spec, name, c.wantFirst)
		}
		f, ok := cm.(*FallbackModel)
		if !ok {
			t.Fatalf("spec=%q: 应为 FallbackModel，实际 %T", c.spec, cm)
		}
		if len(f.chain) != 3 || f.chain[0].Name != c.wantFirst {
			t.Fatalf("spec=%q: 链顺序不符: %+v", c.spec, f.chain)
		}
		foundActive := false
		for _, nm := range f.chain {
			if nm.Name == "Main" {
				foundActive = true
			}
		}
		if !foundActive {
			t.Fatalf("spec=%q: 备用链必须包含主激活模型: %+v", c.spec, f.chain)
		}
	}
}

// TestBuildWorkModelChain_NoUsableModel 全部模型缺 Key 时返回错误。
func TestBuildWorkModelChain_NoUsableModel(t *testing.T) {
	m := newRouteTestManager(t, []config.ModelConfig{
		{ID: "x", Name: "X", Model: "x-model"}, // 无 APIKey
	}, "x")
	if _, _, _, err := BuildWorkModelChain(context.Background(), m,
		"agent-w", "user-main", "x", nil); err == nil {
		t.Fatal("无可用模型时应返回错误")
	}
}
