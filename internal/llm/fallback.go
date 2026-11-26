package llm

// 模型故障切换（M5 FR-5.6）：主模型遇瞬态错误（网络错误/429/5xx/超时）时
// 按配置顺序切换到备用模型；4xx（Key/参数配置错误）不切换，原样返回。
// 切换通过 notify 回调上报（ChatService 注入 → eventbus.model_fallback 事件）。

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/config"
)

// namedModel 带名字的模型（用于切换事件）。
type namedModel struct {
	Name string
	CM   model.ChatModel
}

// FallbackModel 故障切换装饰器。
type FallbackModel struct {
	chain []namedModel
	// notify 模型切换上报（from/to 为模型配置名，reason 为触发错误）。
	notify func(from, to, reason string)
}

// NewFallbackModel 构建 fallback 链（chain[0] 为主模型）。
func NewFallbackModel(chain []namedModel, notify func(from, to, reason string)) *FallbackModel {
	return &FallbackModel{chain: chain, notify: notify}
}

// IsTransientError 判断是否为可切换的瞬态错误（网络/限流/5xx/超时）。
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	transient := []string{
		"429", "500", "502", "503", "504", "bad gateway",
		"rate limit", "too many requests", "overloaded",
		"timeout", "deadline exceeded", "canceled",
		"connection", "connect", "dial", "no such host",
		"broken pipe", "eof", "reset by peer", "unreachable",
	}
	for _, k := range transient {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

func (f *FallbackModel) tryChain(ctx context.Context, invoke func(cm model.ChatModel) error) error {
	var lastErr error
	for i, nm := range f.chain {
		err := invoke(nm.CM)
		if err == nil {
			return nil
		}
		lastErr = err
		// 用户主动取消（ctx 已结束）不切换：否则中断会被当成瞬态故障，
		// 白白再打一次注定失败的备用模型，并误报 model_fallback 事件。
		if ctx.Err() != nil || !IsTransientError(err) || i == len(f.chain)-1 {
			return err
		}
		next := f.chain[i+1]
		if f.notify != nil {
			f.notify(nm.Name, next.Name, err.Error())
		}
	}
	return lastErr
}

// Generate 生成（eino ChatModel 接口）。
func (f *FallbackModel) Generate(ctx context.Context, input []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	var out *schema.Message
	err := f.tryChain(ctx, func(cm model.ChatModel) error {
		m, e := cm.Generate(ctx, input, opts...)
		if e != nil {
			return e
		}
		out = m
		return nil
	})
	return out, err
}

// Stream 流式生成（eino ChatModel 接口）。
func (f *FallbackModel) Stream(ctx context.Context, input []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	var out *schema.StreamReader[*schema.Message]
	err := f.tryChain(ctx, func(cm model.ChatModel) error {
		sr, e := cm.Stream(ctx, input, opts...)
		if e != nil {
			return e
		}
		out = sr
		return nil
	})
	return out, err
}

// BindTools 工具绑定转发到链上第一个支持者。
func (f *FallbackModel) BindTools(tools []*schema.ToolInfo) error {
	var lastErr error
	for _, nm := range f.chain {
		err := nm.CM.BindTools(tools)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

// BuildModelChain 按 Models 配置构建带故障切换的模型链：
// 激活模型优先，其余按列表顺序作为备用。仅一个可用模型时直接返回（无装饰）。
// 返回（模型, 主模型名, 实际主模型配置, 错误）。构建失败（缺 Key 等）的模型跳过；
// 全部失败返回错误。实际主模型配置用于用量按真实模型归因。
// agentID 标识调用方 Agent，userID 标识所属用户；主 Agent 两者相同，
// work agent 使用自身 agent_id + 主 Agent 的 user_id。
func BuildModelChain(ctx context.Context, mgr *config.Manager, agentID, userID string,
	notify func(from, to, reason string)) (model.ChatModel, string, config.ModelConfig, error) {
	cfg := mgr.Get()
	activeID := cfg.ActiveModelID
	var ordered []config.ModelConfig
	for _, mc := range cfg.Models { // 激活模型排最前
		if mc.ID == activeID {
			ordered = append(ordered, mc)
		}
	}
	for _, mc := range cfg.Models {
		if mc.ID != activeID {
			ordered = append(ordered, mc)
		}
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
