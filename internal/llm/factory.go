// Package llm 模型工厂：按配置构建 Eino ChatModel（OpenAI 兼容协议）。
// 每个 Agent 构建模型时携带唯一 Agent ID，注入请求体自定义字段 agent_id，
// 用于供应商侧限流归因、调用追踪与成本核算。
package llm

import (
	"context"
	"fmt"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"ai-dog-agent/internal/config"
)

// BodyAgentIDField Agent 唯一 ID 在请求体中的自定义字段名
//（经 OpenAI 兼容协议的扩展字段随每次 chat.completions 请求上送）。
const BodyAgentIDField = "agent_id"
const BodyUserIDField = "user_id"

// defaultModelTimeout 单次模型请求的默认超时（网络挂起时避免无限等待）。
const defaultModelTimeout = 300 * time.Second

// modelTimeout 返回配置的请求超时；0 或负值使用默认 300s。
func modelTimeout(mc config.ModelConfig) time.Duration {
	if mc.TimeoutSeconds <= 0 {
		return defaultModelTimeout
	}
	return time.Duration(mc.TimeoutSeconds) * time.Second
}

// extraFields 构造请求体扩展字段；关闭开关或 agentID 为空时返回空 map。
func extraFields(mc config.ModelConfig, agentID string) map[string]any {
	extra := map[string]any{}
	if agentID != "" && !mc.DisableExtraFields {
		extra[BodyAgentIDField] = agentID
		extra[BodyUserIDField] = agentID
	}
	return extra
}

// BuildModel 根据模型配置构建 eino ChatModel；agentID 非空时注入请求体。
func BuildModel(ctx context.Context, mc config.ModelConfig, agentID string) (model.ChatModel, error) {
	if mc.APIKey == "" {
		return nil, fmt.Errorf("模型 %q 缺少 API Key", mc.Name)
	}
	if mc.Model == "" {
		return nil, fmt.Errorf("模型 %q 缺少 Model ID", mc.Name)
	}
	cm, err := openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey:      mc.APIKey,
		BaseURL:     mc.BaseURL, // 非空时切换到自定义 OpenAI 兼容端点
		Model:       mc.Model,
		Timeout:     modelTimeout(mc),
		ExtraFields: extraFields(mc, agentID),
	})
	if err != nil {
		return nil, fmt.Errorf("初始化模型失败(%s): %w", mc.Name, err)
	}
	return cm, nil
}
