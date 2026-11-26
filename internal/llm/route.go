package llm

// work agent 模型路由：Spec.Model 命中时作为主模型，其余全局模型兜底。

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/model"

	"ai-dog-agent/internal/config"
)

// ResolveModel 按 ID → Name → Model 的优先级解析模型引用。
// 只有某一层级恰好命中一个配置时才返回；该层多处命中视为歧义，继续尝试下一层。
func ResolveModel(cfg config.Config, ref string) (config.ModelConfig, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return config.ModelConfig{}, false
	}
	fields := []func(config.ModelConfig) string{
		func(mc config.ModelConfig) string { return mc.ID },
		func(mc config.ModelConfig) string { return mc.Name },
		func(mc config.ModelConfig) string { return mc.Model },
	}
	for _, field := range fields {
		var hit []config.ModelConfig
		for _, mc := range cfg.Models {
			if strings.TrimSpace(field(mc)) == ref {
				hit = append(hit, mc)
			}
		}
		if len(hit) == 1 {
			return hit[0], true
		}
	}
	return config.ModelConfig{}, false
}

// BuildWorkModelChain 构建 work agent 的模型链。
//   - specModel 命中：该模型为主，其余全局模型按配置顺序兜底（含主 Agent 激活模型）。
//   - specModel 为空或找不到：回退主 Agent 激活模型并记录警告。
//
// 返回模型链、主模型显示名、实际主模型配置（用量按实际模型归因）、错误。
// 缺 Key/Model ID 的配置跳过；全部不可用时返回错误。
func BuildWorkModelChain(ctx context.Context, mgr *config.Manager, agentID, userID, specModel string,
	notify func(from, to, reason string)) (model.ChatModel, string, config.ModelConfig, error) {
	cfg := mgr.Get()
	var primary config.ModelConfig
	found := false
	if strings.TrimSpace(specModel) != "" {
		primary, found = ResolveModel(cfg, specModel)
		if !found {
			slog.Warn("work agent 指定模型不存在，回退主 Agent 激活模型",
				"agentId", agentID, "model", specModel)
		}
	}
	if !found {
		for _, mc := range cfg.Models {
			if mc.ID == cfg.ActiveModelID {
				primary = mc
				found = true
				break
			}
		}
	}

	ordered := make([]config.ModelConfig, 0, len(cfg.Models))
	if found {
		ordered = append(ordered, primary)
	}
	for _, mc := range cfg.Models {
		if found && mc.ID == primary.ID {
			continue
		}
		ordered = append(ordered, mc)
	}

	var chain []namedModel
	var actualPrimary config.ModelConfig
	for _, mc := range ordered {
		cm, err := BuildModel(ctx, mc, agentID, userID)
		if err != nil {
			continue // 缺 Key/Model ID 的配置跳过
		}
		if len(chain) == 0 {
			actualPrimary = mc
		}
		chain = append(chain, namedModel{Name: mc.Name, CM: cm})
	}
	switch len(chain) {
	case 0:
		return nil, "", config.ModelConfig{}, fmt.Errorf("无可用模型配置（检查 API Key 与 Model ID）")
	case 1:
		return chain[0].CM, chain[0].Name, actualPrimary, nil
	default:
		return NewFallbackModel(chain, notify), chain[0].Name, actualPrimary, nil
	}
}
