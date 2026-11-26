package agent

// work agent 运行身份注入：
//   - approval.WithAgentID：工具调用审批请求显示发起 Agent；
//   - obsInfo.AgentID/Model：tool_call 事件与 LLM 用量按实际 Agent/模型归因。

import (
	"context"

	"github.com/cloudwego/eino/adk"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
)

// withAgentIdentity 为 work agent 运行上下文注入 AgentID 与实际模型信息。
func withAgentIdentity(ctx context.Context, agentID string, mc config.ModelConfig) context.Context {
	ctx = approval.WithAgentID(ctx, agentID)
	if oi := obsFromCtx(ctx); oi != nil {
		cp := *oi
		cp.AgentID = agentID
		if mc.ID != "" {
			cp.ModelID = mc.ID
			cp.ModelName = mc.Name
			cp.PriceIn = mc.PriceIn
			cp.PriceOut = mc.PriceOut
		}
		ctx = withObsInfo(ctx, &cp)
	}
	return ctx
}

// agentWithIdentity 包装 adk.Agent，在 Run 时注入身份。
// 用于 dispatch/explorer：其内部工具调用与审批请求归属对应子 Agent。
type agentWithIdentity struct {
	adk.Agent
	agentID string
	model   config.ModelConfig
}

func (a *agentWithIdentity) Run(ctx context.Context, input *adk.AgentInput,
	opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.Agent.Run(withAgentIdentity(ctx, a.agentID, a.model), input, opts...)
}
