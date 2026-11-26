package agent

// agent-to-agent 私聊（阶段 C）：
//   - send_message 工具：主 Agent 与 work agent 均可调用；
//   - 调用栈经 ctx 传递，限制嵌套深度并拒绝 A→B→A 循环；
//   - 第一版同步：Registry.SendMessage 等待目标完成并返回文本，带超时；
//   - 同一 (session, agent) 串行（agentSession.mu），不同 Agent 可并行。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/eventbus"
)

const (
	// MaxMessageDepth 私聊最大嵌套深度（设计文档 §3.7）。
	MaxMessageDepth = 4
	// SendMessageTimeout 单次同步私聊的最长等待时间。
	SendMessageTimeout = 120 * time.Second
	// maxHistoryMessages 单个 Agent 私聊历史保留的消息条数上限。
	maxHistoryMessages = 40
)

var (
	ErrMessageDepth = errors.New("私聊嵌套层数超过上限")
	ErrMessageCycle = errors.New("检测到私聊循环调用")
)

// messageContext 私聊调用链（ctx 传递，按值派生避免并发共享）。
type messageContext struct {
	depth        int
	participants []string // 已参与私聊的 agentID
}

type messageCtxKey struct{}

func messageContextFrom(ctx context.Context) *messageContext {
	if mc, ok := ctx.Value(messageCtxKey{}).(*messageContext); ok && mc != nil {
		return mc
	}
	return &messageContext{}
}

// pushMessageContext 校验深度与循环后派生新的调用链。
func pushMessageContext(ctx context.Context, selfID, targetID string) (context.Context, error) {
	if targetID == "" || selfID == targetID {
		return ctx, errors.New("私聊目标不能是自己")
	}
	parent := messageContextFrom(ctx)
	if parent.depth+1 > MaxMessageDepth {
		return ctx, fmt.Errorf("%w: 上限 %d", ErrMessageDepth, MaxMessageDepth)
	}
	for _, p := range parent.participants {
		if p == targetID {
			return ctx, fmt.Errorf("%w: 目标 %s 已在调用链中", ErrMessageCycle, targetID)
		}
	}
	participants := append(append([]string(nil), parent.participants...), selfID, targetID)
	next := &messageContext{depth: parent.depth + 1, participants: participants}
	return context.WithValue(ctx, messageCtxKey{}, next), nil
}

// SendMessageArgs send_message 工具参数。
type SendMessageArgs struct {
	To      string `json:"to" jsonschema:"目标 Agent：agent_id、名称或 dispatch 工具名"`
	Content string `json:"content" jsonschema:"发送给目标 Agent 的消息内容"`
}

// SendMessageTool 构建 send_message 工具。
// selfID/selfName 为调用方 Agent 身份（主 Agent 用主 ID 与 "orchestrator"），
// 目标按 sessionID 在 Registry 中查找。事件通过 bus 推送 agent_message。
func SendMessageTool(registry *Registry, bus *eventbus.Bus,
	sessionID, selfID, selfName string) (tool.BaseTool, error) {
	return utils.InferTool("send_message",
		"向同一会话中的另一个 Agent 发送消息并同步等待其回复。目标用 agent_id、名称或 dispatch 工具名指定。",
		func(ctx context.Context, args SendMessageArgs) (string, error) {
			if registry == nil {
				return "", errors.New("Agent 注册表不可用")
			}
			to := strings.TrimSpace(args.To)
			content := strings.TrimSpace(args.Content)
			if to == "" || content == "" {
				return "", errors.New("to 与 content 不能为空")
			}
			data := eventbus.AgentMessageData{
				From: selfID, FromName: selfName, Content: content,
			}
			target, ok := registry.Lookup(sessionID, to)
			if !ok {
				data.To = to
				data.Error = fmt.Sprintf("目标 Agent 不存在: %s", to)
				emitAgentMessage(bus, sessionID, data)
				return "", errors.New(data.Error)
			}
			data.To = target.spec.AgentID
			data.ToName = target.spec.Name

			nextCtx, err := pushMessageContext(ctx, selfID, target.spec.AgentID)
			if err != nil {
				data.Error = err.Error()
				emitAgentMessage(bus, sessionID, data)
				return "", err
			}
			data.Depth = messageContextFrom(nextCtx).depth

			reply, err := registry.SendMessage(nextCtx, sessionID, selfID, target, content)
			data.Reply = reply
			if err != nil {
				data.Error = err.Error()
			}
			emitAgentMessage(bus, sessionID, data)
			return reply, err
		})
}

// emitAgentMessage 推送私聊事件（bus 为 nil 时静默跳过，便于单测）。
// 仅用于前端事件流展示，不写入会话消息历史与 task_events：
// 刷新/重连后卡片不保留，避免与主对话混排。
func emitAgentMessage(bus *eventbus.Bus, sessionID string, data eventbus.AgentMessageData) {
	if bus == nil {
		return
	}
	bus.Emit(eventbus.Event{Type: eventbus.EvAgentMessage, SessionID: sessionID, Data: data})
}

// SendMessage 同步向目标 Agent 发送消息并等待回复。
// 同一目标串行执行；历史保留最近 maxHistoryMessages 条消息。
func (r *Registry) SendMessage(ctx context.Context, sessionID, from string,
	target *agentSession, content string) (string, error) {
	if target == nil {
		return "", errors.New("目标 Agent 为空")
	}
	target.mu.Lock()
	defer target.mu.Unlock()

	// 注入 work agent 身份：工具事件/审批归属与实际模型用量
	ctx = withAgentIdentity(ctx, target.spec.AgentID, target.model)

	// 私聊历史超过窗口阈值时先压缩（保留最近轮，早期消息摘要）
	if ShouldCompress(target.history, target.model.EffectiveContextWindow(), target.spec.Context.EffectiveThreshold()) {
		if compressed, res := compressAgentHistory(ctx, target); compressed != nil {
			target.history = compressed
			if res != nil {
				if r.bus != nil {
					r.bus.Emit(eventbus.Event{
						Type: eventbus.EvContextCompressed, SessionID: sessionID,
						Data: eventbus.ContextCompressedData{
							AgentID: target.spec.AgentID, BeforeTokens: res.BeforeTokens,
							AfterTokens: res.AfterTokens, Strategy: res.Strategy,
							Summarized: res.Summarized,
						},
					})
				}
				if res.Usage != nil {
					limits := WorkLimits(target.spec.Budget)
					inTok := int64(res.Usage.PromptTokens)
					outTok := int64(res.Usage.CompletionTokens)
					costIn := float64(inTok) / 1e6 * target.model.PriceIn
					costOut := float64(outTok) / 1e6 * target.model.PriceOut
					target.compressTokens += inTok + outTok
					target.compressCost += costIn + costOut
					if r.bus != nil {
						r.bus.Emit(eventbus.Event{
							Type: eventbus.EvLLMUsage, SessionID: sessionID,
							Data: eventbus.LLMUsageData{
								AgentID: target.spec.AgentID, Model: target.model.Name,
								ModelID: target.model.ID, InputTokens: inTok, OutputTokens: outTok,
								CostIn: costIn, CostOut: costOut,
							},
						})
					}
					// 压缩本身消耗预算：超过该 Agent 的 max_tokens / max_cost 时终止本次私聊
					if limits.MaxTokens > 0 && target.compressTokens > limits.MaxTokens {
						return "", &LimitError{Kind: LimitTokens, Limit: float64(limits.MaxTokens),
							Used: float64(target.compressTokens), Sentinel: ErrMaxTokens}
					}
					if limits.MaxCost > 0 && target.compressCost > limits.MaxCost {
						return "", &LimitError{Kind: LimitCost, Limit: limits.MaxCost,
							Used: target.compressCost, Sentinel: ErrBudgetExceeded}
					}
				}
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, SendMessageTimeout)
	defer cancel()

	input := make([]*schema.Message, 0, len(target.history)+1)
	input = append(input, target.history...)
	input = append(input, schema.UserMessage(content))

	reply, assistant, err := runSession(ctx, target.runner, input)
	if err != nil {
		return reply, err
	}
	target.history = append(target.history,
		schema.UserMessage(content), assistant)
	if len(target.history) > maxHistoryMessages {
		target.history = target.history[len(target.history)-maxHistoryMessages:]
	}
	return reply, nil
}

// runSession 执行一次 Agent 会话并收集助手文本。
func runSession(ctx context.Context, runner *adk.Runner, input []*schema.Message) (string, *schema.Message, error) {
	iter := runner.Run(ctx, input)
	var sb strings.Builder
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return strings.TrimSpace(sb.String()), nil, ev.Err
		}
		if ev.Output == nil {
			continue
		}
		mo := ev.Output.MessageOutput
		if mo == nil || mo.Role == schema.Tool {
			continue
		}
		if mo.IsStreaming {
			if mo.MessageStream == nil {
				continue
			}
			for {
				msg, err := mo.MessageStream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return strings.TrimSpace(sb.String()), nil, err
				}
				if msg != nil {
					sb.WriteString(msg.Content)
				}
			}
			continue
		}
		if mo.Message != nil {
			sb.WriteString(mo.Message.Content)
		}
	}
	out := strings.TrimSpace(sb.String())
	return out, schema.AssistantMessage(out, nil), nil
}
