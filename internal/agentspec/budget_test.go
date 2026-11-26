package agentspec

import (
	"testing"
)

// TestSpecBudgetAndContextValidation 预算/压缩配置校验与默认值。
func TestSpecBudgetAndContextValidation(t *testing.T) {
	root := t.TempDir()
	base := Spec{Name: "reviewer", SystemPrompt: "你是 reviewer"}

	bad := base
	bad.Name = "bad-budget"
	bad.Budget.MaxSteps = -1
	if err := Save(root, bad); err == nil {
		t.Fatal("负 budget 应校验失败")
	}

	badCtx := base
	badCtx.Name = "bad-threshold"
	badCtx.Context.CompressThreshold = 1.5
	if err := Save(root, badCtx); err == nil {
		t.Fatal("compress_threshold > 1 应校验失败")
	}

	badCtx2 := base
	badCtx2.Name = "bad-threshold-2"
	badCtx2.Context.CompressThreshold = -0.1
	if err := Save(root, badCtx2); err == nil {
		t.Fatal("负 compress_threshold 应校验失败")
	}

	ok := base
	ok.Budget = Budget{MaxSteps: 12, MaxTokens: 150000, MaxDurationSeconds: 600,
		MaxToolCalls: 30, MaxCost: 1.5, MaxRetries: 3}
	ok.Context = ContextConfig{CompressThreshold: 0.7, KeepRecent: 6}
	if err := Save(root, ok); err != nil {
		t.Fatalf("合法配置应保存成功: %v", err)
	}
	specs, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(specs) != 1 || specs[0].Budget.MaxSteps != 12 || specs[0].Context.KeepRecent != 6 {
		t.Fatalf("预算/压缩配置回读不符: %+v", specs)
	}
	// threshold=1 允许
	upper := base
	upper.Name = "upper"
	upper.Context.CompressThreshold = 1
	if err := Save(root, upper); err != nil {
		t.Fatalf("threshold=1 应允许: %v", err)
	}
}

// TestContextDefaults 未配置压缩参数时使用默认值。
func TestContextDefaults(t *testing.T) {
	var c ContextConfig
	if c.EffectiveThreshold() != DefaultCompressThreshold {
		t.Fatalf("默认阈值应为 %v", DefaultCompressThreshold)
	}
	if c.EffectiveKeepRecent() != DefaultKeepRecent {
		t.Fatalf("默认保留轮数应为 %d", DefaultKeepRecent)
	}
	c = ContextConfig{CompressThreshold: 0.5, KeepRecent: 3}
	if c.EffectiveThreshold() != 0.5 || c.EffectiveKeepRecent() != 3 {
		t.Fatalf("显式值应生效: %+v", c)
	}
}
