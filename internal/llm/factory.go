// Package llm 模型工厂：按配置构建 Eino ChatModel（OpenAI 兼容协议）。
// 每个 Agent 构建模型时携带唯一 Agent ID，注入请求体自定义字段 agent_id，
// 用于供应商侧限流归因、调用追踪与成本核算。
package llm

import (
	"context"
	"fmt"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"ai-dog-agent/internal/config"
)

// BodyAgentIDField Agent 唯一 ID 在请求体中的自定义字段名
//（经 OpenAI 兼容协议的扩展字段随每次 chat.completions 请求上送）。
const BodyAgentIDField = "agent_id"
const BodyUserIDField = "user_id"

// BuildModel 根据模型配置构建 eino ChatModel；agentID 非空时注入请求体。
func BuildModel(ctx context.Context, mc config.ModelConfig, agentID string) (model.ChatModel, error) {
	if mc.APIKey == "" {
		return nil, fmt.Errorf("模型 %q 缺少 API Key", mc.Name)
	}
	if mc.Model == "" {
		return nil, fmt.Errorf("模型 %q 缺少 Model ID", mc.Name)
	}
	extra := map[string]any{}
	if agentID != "" {
		extra[BodyAgentIDField] = agentID
		extra[BodyUserIDField] = agentID
	}
	cm, err := openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey:      mc.APIKey,
		BaseURL:     mc.BaseURL, // 非空时切换到自定义 OpenAI 兼容端点
		Model:       mc.Model,
		Timeout:     0,
		ExtraFields: extra,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化模型失败(%s): %w", mc.Name, err)
	}
	return cm, nil
}
